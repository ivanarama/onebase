package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Область просмотра формы выбора (план 168).
//
// Статический preview (`choice_preview`) — имя реквизита; значения уже лежат в
// items и прошли object read → row filter → field mask, отдельного запроса нет.
// Динамический (`choice_preview_proc`) — строго квалифицированная экспортная
// функция `Модуль.Функция(Ссылки, Контекст)`, вызывается ОДИН раз на страницу
// в allowlist-окружении (choice_preview_env.go) внутри host-owned транзакции,
// которая откатывается ВСЕГДА. Ошибка функции — контролируемый 422 с безопасным
// сообщением: выбор блокируется (fail-closed), тихий откат к старому списку
// запрещён инвариантом 10.
//
// Preview всегда обычный текст: HTML/richtext не интерпретируются никогда
// (инвариант 5), санитайзер разметки не нужен — клиент пишет textContent.

// choicePreviewKey — служебный ключ строки, под которым уезжает собранный текст.
// Начинается с подчёркивания, как _label: у реквизита конфигурации такого имени
// быть не может, и подмены настоящего значения не выйдет.
const choicePreviewKey = "_preview"

// errChoicePreview — маркер ошибки функции/контракта preview: наружу отдаётся
// контролируемый 422 с безопасным сообщением, а не 500.
var errChoicePreview = errors.New("choice preview unavailable")

// canonicalChoicePreviewField возвращает имя реквизита в том регистре, в
// котором оно объявлено в metadata.Entity.Fields. Validate допускает ссылки на
// реквизиты без учёта регистра, а строки результата индексируются каноничными
// именами — отдавать исходное написание choice_preview клиенту нельзя.
func canonicalChoicePreviewField(ent *metadata.Entity) string {
	if ent == nil {
		return ""
	}
	declared := strings.TrimSpace(ent.ChoicePreview)
	for _, f := range ent.Fields {
		if strings.EqualFold(f.Name, declared) {
			return f.Name
		}
	}
	return declared
}

// runChoicePreviewProc вызывает функцию конфигурации и раскладывает ответ по
// строкам под choicePreviewKey. Контракт (план 168):
//   - вызов идёт целиком внутри host-owned транзакции, которая откатывается
//     ВСЕГДА — и при успехе, и при ошибке (инвариант 9);
//   - окружение — позитивный allowlist (buildChoicePreviewVars), полный
//     buildDSLVarsTx сюда запрещён;
//   - запись из результата ПЕРЕКРЫВАЕТ статический preview, ОТСУТСТВУЮЩАЯ —
//     оставляет его (инвариант: «отсутствующая запись оставляет статическое
//     значение»); ключ не из Ссылок игнорируется.
//
// Ошибка функции/контракта оборачивается в errChoicePreview — вызывающий
// отдаёт 422 и блокирует выбор.
func (s *Server) runChoicePreviewProc(ctx context.Context, ent *metadata.Entity, items []map[string]any, ctxVals map[string]any) error {
	if len(items) == 0 {
		return nil
	}
	name := strings.TrimSpace(ent.ChoicePreviewProc)
	if name == "" {
		return nil
	}
	proc, err := resolveChoicePreviewProc(s.reg, name)
	if err != nil {
		return fmt.Errorf("%w: %w", errChoicePreview, err)
	}

	// Host-owned транзакция: сервер сам её открывает и ВСЕГДА откатывает.
	// finishDSLExecution тут не используется: откатывать «оставленную открытой
	// DSL-транзакцию» — не доказательство отсутствия стойких эффектов, а
	// безусловный rollback хоста — доказательство.
	tx, txCtx, berr := s.store.BeginTx(ctx)
	if berr != nil {
		return fmt.Errorf("%w: %w", errChoicePreview, berr)
	}
	defer func() { _ = tx.Rollback(txCtx) }()

	dslVars := s.buildChoicePreviewVars(txCtx, items, ent, canonicalChoicePreviewField(ent), ctxVals)
	this := &interpreter.MapThis{M: map[string]any{}}
	var result any
	if runErr := s.interp.RunWithResult(proc, this, &result, dslVars); runErr != nil {
		uiLog().Warn("choice_preview_proc: ошибка исполнения", "proc", name, "entity", ent.Name, "err", runErr)
		return fmt.Errorf("%w: функция просмотра завершилась ошибкой", errChoicePreview)
	}
	// DEBUG(result=%T keys=%v)
	texts, err := choicePreviewTexts(result)
	if err != nil {
		uiLog().Warn("choice_preview_proc: неверный результат", "proc", name, "entity", ent.Name, "err", err)
		return fmt.Errorf("%w: %w", errChoicePreview, err)
	}
	// Merge-семантика: запись функции перекрывает статический fallback,
	// отсутствующая запись оставляет его (инвариант плана 168).
	staticField := canonicalChoicePreviewField(ent)
	for _, row := range items {
		id := refValueString(row["id"])
		if text, ok := texts[id]; ok {
			row[choicePreviewKey] = text
			continue
		}
		row[choicePreviewKey] = refValueString(row[staticField])
	}
	return nil
}

// choicePreviewTexts приводит ответ функции к «идентификатор → текст». Только
// Соответствие (interpreter.Map); значение НЕопределено/nil — запись без
// текста (статический fallback остаётся), всё прочее — ошибка контракта.
func choicePreviewTexts(result any) (map[string]string, error) {
	m, ok := result.(*interpreter.Map)
	if !ok {
		return nil, fmt.Errorf("результат должен быть Соответствием")
	}
	out := map[string]string{}
	for _, key := range m.Keys() {
		val := m.Get(key)
		if val == nil {
			continue
		}
		out[fmt.Sprintf("%v", key)] = fmt.Sprintf("%v", val)
	}
	return out, nil
}

// choicePreviewPageBody — тело POST /ui/_ref-options/{entity}/page.
type choicePreviewPageBody struct {
	Q      string `json:"q"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Source struct {
		Entity  string `json:"entity"`
		Form    string `json:"form"`
		Element string `json:"element"`
	} `json:"source"`
	Context map[string]string `json:"context"`
}

// choicePreviewPage — POST /ui/_ref-options/{entity}/page (план 168, HTTP-контракт).
// POST выбран, чтобы значения незаписанной формы не попадали в URL, access log
// и историю браузера. Имя функции, поля preview и состав контекста браузер НЕ
// присылает: только идентичность form/entity/element и значения объявленных
// источников; сервер сам находит форму и элемент, проверяет ссылочный
// data_path и восстанавливает allowlist choice_context.
func (s *Server) choicePreviewPageHandler(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "entity")
	ent := s.reg.GetEntity(name)
	if ent == nil {
		http.Error(w, s.tr(s.resolveLang(r), "Сущность не найдена")+": "+name, http.StatusNotFound)
		return
	}
	if !s.can(r, string(ent.Kind), ent.Name, "read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.choicePreviewPage(w, r, ent)
}

func (s *Server) choicePreviewPage(w http.ResponseWriter, r *http.Request, ent *metadata.Entity) {
	var body choicePreviewPageBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	limit := body.Limit
	if limit <= 0 {
		limit = refPickerDefaultLimit
	}
	if limit > refPickerMaxLimit {
		limit = refPickerMaxLimit
	}

	// Server-resolved source (блокер 3 круга 2): форма и элемент восстанавливаются
	// из метаданных, элемент обязан быть ссылочным ПолеВвода на целевую сущность.
	owner := s.reg.GetEntity(body.Source.Entity)
	if owner == nil {
		http.Error(w, "unknown form entity", http.StatusBadRequest)
		return
	}
	element := findPreviewElementByName(owner, body.Source.Element)
	if element == nil {
		http.Error(w, "unknown preview element", http.StatusBadRequest)
		return
	}
	field := entityField(owner, formChoicePathName(strings.TrimSpace(element.DataPath)))
	if field == nil || field.RefEntity == "" || !strings.EqualFold(field.RefEntity, ent.Name) {
		http.Error(w, "element does not reference this entity", http.StatusBadRequest)
		return
	}

	// Контекст: только объявленные в choice_context параметры; значение типизируется
	// по реквизиту-источнику (блокер 3: произвольный клиентский JSON больше не
	// доходит до функции). Скрытые/замаскированные источники отклоняются на
	// этапе валидации формы (configcheck), здесь — типы и ссылки.
	ctxVals := map[string]any{}
	for alias, path := range element.ChoiceContext {
		srcField := entityField(owner, formChoicePathName(strings.TrimSpace(path)))
		if srcField == nil {
			http.Error(w, "invalid choice context source", http.StatusBadRequest)
			return
		}
		raw := strings.TrimSpace(body.Context[alias])
		if raw == "" {
			continue
		}
		if srcField.RefEntity != "" {
			id, perr := uuid.Parse(raw)
			if perr != nil || id == uuid.Nil {
				http.Error(w, "invalid context reference: "+alias, http.StatusBadRequest)
				return
			}
			refEnt := s.reg.GetEntity(srcField.RefEntity)
			if refEnt == nil {
				http.Error(w, "invalid context reference entity: "+alias, http.StatusBadRequest)
				return
			}
			// Ссылка требует object read и допуска строки (инвариант 7) —
			// тот же гейт, что и selected_allowed у choice.
			// Последний аргумент — наш ключ choice_folders: здесь проверяется
			// ссылка контекста, а не выбор пользователя, поэтому группы
			// допускаются как обычные записи.
			okAllowed, aerr := s.choiceSelectedAllowed(r.Context(), refEnt, id, nil, true)
			if aerr != nil {
				s.serverError(w, r, aerr)
				return
			}
			if !okAllowed {
				http.Error(w, "context reference not allowed: "+alias, http.StatusForbidden)
				return
			}
			ctxVals[alias] = &interpreter.Ref{UUID: id.String(), Type: refEnt.Name, Kind: refEnt.Kind}
			continue
		}
		ctxVals[alias] = raw
	}

	// Страница — тем же путём, что и обычный подбор: object read → row filter →
	// field mask → _label (инварианты 2 и 11). Только затем вызывается функция.
	items, total, err := s.referenceOptionsPageWithParams(r.Context(), ent, body.Q, limit, body.Offset, storage.ListParams{})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.runChoicePreviewProc(r.Context(), ent, items, ctxVals); err != nil {
		if errors.Is(err, errChoicePreview) {
			// Контролируемый 422 с безопасным сообщением (инвариант 10):
			// клиент блокирует выбор и показывает «Пояснение недоступно».
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   s.tr(s.resolveLang(r), "Пояснение недоступно: повторите запрос"),
				"preview": false,
			})
			return
		}
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":   items,
		"total":   total,
		"limit":   limit,
		"offset":  body.Offset,
		"preview": choicePreviewKey,
		"canPick": true,
	})
}

// findPreviewElementByName — элемент с choice_context по имени среди ВСЕХ форм
// сущности; для preview годится только ссылочное ПолеВвода. Найдено больше
// одного — неоднозначно: fail-closed, а не «возьмём первую попавшуюся связь».
// formChoicePathName — имя реквизита из пути `Объект.<Реквизит>`.
func formChoicePathName(path string) string {
	root, fieldName, ok := formChoicePath(path)
	if !ok || !strings.EqualFold(root, "Объект") && !strings.EqualFold(root, "Object") {
		return ""
	}
	return fieldName
}

func findPreviewElementByName(owner *metadata.Entity, name string) *metadata.FormElement {
	if owner == nil || strings.TrimSpace(name) == "" {
		return nil
	}
	var matches []*metadata.FormElement
	for _, form := range owner.Forms {
		if form == nil {
			continue
		}
		form.Walk(func(el *metadata.FormElement) bool {
			if el != nil && strings.EqualFold(el.Name, name) && el.Kind == "ПолеВвода" && len(el.ChoiceContext) > 0 {
				matches = append(matches, el)
			}
			return true
		})
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return nil // нет ни одного — неизвестный элемент; больше одного — неоднозначно
}
