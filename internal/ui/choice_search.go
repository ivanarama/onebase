package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Поиск подбора функцией конфигурации — choice_search_proc справочника, как
// ОбработкаПолученияДанныхВыбора модуля менеджера 1С.
//
// Подстрока наименования не годится для адресов: «15к1» не входит в
// «д.15, корп.1», а «15» находит 80 домов вперемешку с «д.150». При НЕПУСТОМ
// вводе в подборе (поле, ячейка табличной части, окно выбора) сервер вместо
// ILIKE зовёт `Модуль.Функция(Текст, Параметры)`, и та возвращает
// упорядоченный Массив ссылок. Дальше — обычный путь подбора, ограниченный
// этими id: права чтения, RLS, отборы flt/choice_filter, активность, группы,
// маски и подписи применяет платформа, функция их обойти не может. Порядок
// функции сохраняется, страницы (limit/offset) и total считает платформа по
// уже отфильтрованному списку — поэтому они сходятся с тем, что видно.
//
// Окружение и транзакция — те же, что у choice_preview_proc (план 168):
// позитивный allowlist (Запрос, Перечисления, Предопределённые, get-only
// Константы, текущий пользователь), host-owned транзакция, которая
// откатывается всегда. Ошибка функции — контролируемый отказ (422), тихого
// отката к поиску подстрокой нет: иначе сломанная функция выглядела бы как
// «ничего не нашлось».

// choiceSearchMax — сколько ссылок функция может вернуть: больше подбор всё
// равно не покажет. Совпадает с пределом страницы списка.
const choiceSearchMax = storage.MaxListPageSize

// errChoiceSearch — маркер ошибки функции поиска или её результата.
var errChoiceSearch = errors.New("choice search unavailable")

// choiceSearchActive — подбор этой сущности по этому тексту идёт функцией.
func choiceSearchActive(ent *metadata.Entity, search string) bool {
	return ent != nil && strings.TrimSpace(ent.ChoiceSearchProc) != "" && strings.TrimSpace(search) != ""
}

// choiceSearchPage — страница подбора по результату функции поиска.
func (s *Server) choiceSearchPage(ctx context.Context, ent *metadata.Entity, search string, limit, offset int, extra storage.ListParams) ([]map[string]any, int, error) {
	ids, err := s.runChoiceSearchProc(ctx, ent, strings.TrimSpace(search), extra)
	if err != nil {
		return nil, 0, err
	}
	params := extra
	params.Search = ""
	params.Limit = 0
	params.Offset = 0
	params.Sort = ""
	params.ChoicePredicates = append(append([]storage.ChoicePredicate(nil), extra.ChoicePredicates...),
		storage.ChoicePredicate{ByIDs: true, IDs: ids})
	rows, err := s.referenceOptionsWithParams(ctx, ent, refOptionsChoice, params)
	if err != nil {
		return nil, 0, err
	}
	rank := make(map[string]int, len(ids))
	for i, id := range ids {
		rank[id.String()] = i
	}
	sort.SliceStable(rows, func(a, b int) bool {
		return rank[refValueString(rows[a]["id"])] < rank[refValueString(rows[b]["id"])]
	})
	total := len(rows)
	if offset >= total {
		return []map[string]any{}, total, nil
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return rows[offset:end], total, nil
}

// runChoiceSearchProc вызывает функцию поиска и приводит результат к
// упорядоченному списку id без повторов.
func (s *Server) runChoiceSearchProc(ctx context.Context, ent *metadata.Entity, text string, extra storage.ListParams) ([]uuid.UUID, error) {
	name := strings.TrimSpace(ent.ChoiceSearchProc)
	proc, err := resolveChoiceSearchProc(s.reg, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errChoiceSearch, err)
	}
	tx, txCtx, berr := s.store.BeginTx(ctx)
	if berr != nil {
		return nil, fmt.Errorf("%w: %w", errChoiceSearch, berr)
	}
	defer func() { _ = tx.Rollback(txCtx) }()

	vars := s.buildChoiceSearchVars(txCtx, ent)
	params := choiceSearchParams(ent, extra)
	result, runErr := s.interp.CallSandboxed(proc, &interpreter.MapThis{M: map[string]any{}}, []any{text, params},
		interpreter.SandboxProfile{Context: txCtx, MaxWallClock: s.operationTimeout(opProcessorRun)}, vars)
	if runErr != nil {
		uiLog().Warn("choice_search_proc: ошибка исполнения", "proc", name, "entity", ent.Name, "err", runErr)
		return nil, fmt.Errorf("%w: функция поиска завершилась ошибкой", errChoiceSearch)
	}
	ids, err := choiceSearchIDs(result)
	if err != nil {
		uiLog().Warn("choice_search_proc: неверный результат", "proc", name, "entity", ent.Name, "err", err)
		return nil, fmt.Errorf("%w: %w", errChoiceSearch, err)
	}
	if len(ids) > choiceSearchMax {
		uiLog().Warn("choice_search_proc: результат обрезан", "proc", name, "entity", ent.Name, "returned", len(ids), "max", choiceSearchMax)
		ids = ids[:choiceSearchMax]
	}
	return ids, nil
}

// choiceSearchParams — второй аргумент функции: Структура {Справочник, Отбор,
// Предел}. Отбор — равенства, которые уже наложены на подбор (владелец из flt,
// choice_filter с eq): по ним функция сужает свой запрос (дома этой улицы).
// Остальные условия функции знать не обязательно — платформа применит их к
// результату сама.
func choiceSearchParams(ent *metadata.Entity, extra storage.ListParams) *interpreter.Struct {
	filter := &interpreter.Map{}
	keys := make([]string, 0, len(extra.Filters))
	for k := range extra.Filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := extra.Filters[k]; v.Value != "" {
			filter.CallMethod("вставить", []any{k, v.Value})
		}
	}
	for _, p := range extra.ChoicePredicates {
		if p.ByIDs || p.Op != metadata.FormChoiceOpEqual || strings.TrimSpace(p.Field) == "" {
			continue
		}
		filter.CallMethod("вставить", []any{p.Field, p.Value})
	}
	return interpreter.NewStructFromMap(map[string]any{
		"Справочник": ent.Name,
		"Отбор":      filter,
		"Предел":     choiceSearchMax,
	})
}

// choiceSearchIDs приводит ответ функции к списку id: Массив ссылок или
// строк-идентификаторов. Неопределено — «ничего не найдено». Повторы
// схлопываются по первому вхождению; не-id — ошибка контракта.
func choiceSearchIDs(result any) ([]uuid.UUID, error) {
	if result == nil {
		return nil, nil
	}
	arr, ok := result.(*interpreter.Array)
	if !ok {
		return nil, fmt.Errorf("результат должен быть Массивом ссылок")
	}
	seen := make(map[uuid.UUID]bool)
	items := arr.Iterate()
	out := make([]uuid.UUID, 0, len(items))
	for i, raw := range items {
		var s string
		switch v := raw.(type) {
		case *interpreter.Ref:
			if v == nil {
				continue
			}
			s = v.UUID
		case string:
			s = v
		case nil:
			continue
		default:
			return nil, fmt.Errorf("элемент %d результата — %T, а нужна ссылка", i+1, raw)
		}
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("элемент %d результата — не ссылка: %q", i+1, s)
		}
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// buildChoiceSearchVars — allowlist-окружение choice_preview_proc без
// переменных просмотра (Ссылки, Контекст): у поиска их нет.
func (s *Server) buildChoiceSearchVars(ctx context.Context, ent *metadata.Entity) map[string]any {
	vars := s.buildChoicePreviewVars(ctx, nil, ent, "", nil)
	for _, k := range []string{"Ссылки", "Refs", "Контекст", "Context"} {
		delete(vars, k)
	}
	return vars
}

// resolveChoiceSearchProc — строго квалифицированная экспортная функция
// `Модуль.Функция` ровно с двумя параметрами (Текст, Параметры).
func resolveChoiceSearchProc(reg interface {
	GetModuleNamespacedProc(moduleName, procName string) *ast.ProcedureDecl
}, name string) (*ast.ProcedureDecl, error) {
	mod, fn, ok := strings.Cut(strings.TrimSpace(name), ".")
	if !ok || strings.TrimSpace(mod) == "" || strings.TrimSpace(fn) == "" || strings.Contains(fn, ".") {
		return nil, fmt.Errorf("choice_search_proc должен быть вида Модуль.Функция, получено %q", name)
	}
	proc := reg.GetModuleNamespacedProc(mod, fn)
	if proc == nil {
		return nil, fmt.Errorf("функция %s не найдена в модуле %s", fn, mod)
	}
	if !proc.Export {
		return nil, fmt.Errorf("функция %s.%s должна быть Экспорт", mod, fn)
	}
	if len(proc.Params) != 2 {
		return nil, fmt.Errorf("функция %s.%s должна принимать ровно два параметра (Текст, Параметры), сейчас %d", mod, fn, len(proc.Params))
	}
	return proc, nil
}

// choiceSearchUnavailable — контролируемый 422: поиск сломан в конфигурации.
// Клиент показывает пустую выдачу с сообщением, а не результат подстроки.
func (s *Server) choiceSearchUnavailable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": s.tr(s.resolveLang(r), "Поиск недоступен: ошибка функции поиска"),
		"items": []any{},
		"total": 0,
	})
}
