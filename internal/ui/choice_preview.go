package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Область просмотра формы выбора, зависящая от КОНТЕКСТА подбора.
//
// choice_preview показывает реквизит строки — этого хватает, пока текст зависит
// только от самого элемента. Памятка по направлению обслуживания так не
// устроена: у филиала МСК свои ограничения («по АВИТО не дальше 20 км от МКАД»),
// у региона другие, и знать, ДЛЯ КАКОГО филиала идёт подбор, может только
// вызывающая форма. Поэтому она присылает контекст (FormElement.ChoiceContext),
// а конфигурация собирает тексты процедурой choice_preview_proc:
//
//	Функция ПамяткиДляПодбора(Ссылки, Контекст) Экспорт  // → Соответствие
//
// Одна процедура на страницу подбора, а не вызов на строку: пятьдесят вызовов на
// каждое нажатие в поиске — это пятьдесят запросов к базе, и подбор из бодрого
// становится задумчивым.

// choicePreviewKey — служебный ключ строки, под которым уезжает собранный текст.
// Начинается с подчёркивания, как _label: у реквизита конфигурации такого имени
// быть не может, и подмены настоящего значения не выйдет.
const choicePreviewKey = "_preview"

// choiceContextFromRequest — контекст подбора из запроса: JSON-объект строк
// («Филиал» → uuid). Чужой или битый параметр — пустой контекст, а не ошибка:
// подбор обязан открыться в любом случае.
func choiceContextFromRequest(r *http.Request) map[string]any {
	raw := strings.TrimSpace(r.URL.Query().Get("ctx"))
	if raw == "" || len(raw) > 4096 {
		return map[string]any{}
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(parsed))
	for k, v := range parsed {
		out[k] = v
	}
	return out
}

// applyChoicePreviewProc зовёт процедуру конфигурации и раскладывает её ответ по
// строкам под ключом choicePreviewKey. Возвращает, удалось ли: не нашли
// процедуру или она упала — вызывающий оставляет прежний choice_preview.
func (s *Server) applyChoicePreviewProc(r *http.Request, ent *metadata.Entity, items []map[string]any) bool {
	if s == nil || ent == nil || len(items) == 0 {
		return false
	}
	name := strings.TrimSpace(ent.ChoicePreviewProc)
	if name == "" {
		return false
	}
	proc := s.reg.GetModuleProc(name)
	if proc == nil {
		if mod, fn, ok := strings.Cut(name, "."); ok {
			proc = s.reg.GetModuleNamespacedProc(mod, fn)
		}
	}
	if proc == nil {
		uiLog().Warn("choice_preview_proc: процедура не найдена", "proc", name, "entity", ent.Name)
		return false
	}

	ids := &interpreter.Array{}
	for _, row := range items {
		ids.CallMethod("добавить", []any{refValueString(row["id"])})
	}
	// Контекст едет Соответствием, а не служебной обёрткой: конфигурация читает
	// его обычным Контекст.Получить("Филиал"), как любое Соответствие в DSL.
	ctxMap := &interpreter.Map{}
	for k, v := range choiceContextFromRequest(r) {
		ctxMap.CallMethod("вставить", []any{k, v})
	}

	mc := runtime.NewMovementsCollector("choice-preview", uuid.Nil)
	dslVars, txState := s.buildDSLVarsTx(r.Context(), mc)
	defer rollbackDSLExecution(txState)
	dslVars["Ссылки"] = ids
	dslVars["Refs"] = ids
	dslVars["Контекст"] = ctxMap
	dslVars["Context"] = ctxMap

	var result any
	runErr := s.interp.RunWithResult(proc, &interpreter.MapThis{M: map[string]any{}}, &result, dslVars)
	if runErr = finishDSLExecution(txState, runErr); runErr != nil {
		uiLog().Warn("choice_preview_proc: ошибка исполнения", "proc", name, "entity", ent.Name, "err", runErr)
		return false
	}
	texts := choicePreviewTexts(result)
	if texts == nil {
		return false
	}
	for _, row := range items {
		row[choicePreviewKey] = texts[refValueString(row["id"])]
	}
	return true
}

// choicePreviewTexts приводит ответ процедуры к «идентификатор → текст».
// Ожидается Соответствие; всё прочее — отказ, а не молчаливая пустая панель.
func choicePreviewTexts(result any) map[string]string {
	m, ok := result.(*interpreter.Map)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, key := range m.Keys() {
		val := m.Get(key)
		if val == nil {
			continue
		}
		out[fmt.Sprintf("%v", key)] = fmt.Sprintf("%v", val)
	}
	return out
}

// choicePreviewIsRich — показывать ли текст просмотра с оформлением. Признак
// берётся из ТИПА реквизита, а не из содержимого строки: «похоже на HTML» —
// негодный критерий, по нему обычный текст с угловой скобкой стал бы разметкой.
// Текст, собранный процедурой (choicePreviewKey), оформления не получает: она
// возвращает строку, а не размеченный реквизит.
func choicePreviewIsRich(ent *metadata.Entity, previewField string) bool {
	if ent == nil || previewField == "" || previewField == choicePreviewKey {
		return false
	}
	for _, f := range ent.Fields {
		if strings.EqualFold(f.Name, previewField) {
			return f.Type == metadata.FieldTypeRichText
		}
	}
	return false
}

// sanitizeChoicePreview чистит разметку строк перед отправкой в браузер. Гоняем
// через тот же санитайзер, что и остальной richtext: значения приходят из базы,
// а в базу их мог положить кто угодно с правом записи в справочник.
func sanitizeChoicePreview(items []map[string]any, previewField string) {
	for _, row := range items {
		if raw, ok := row[previewField]; ok && raw != nil {
			row[previewField] = richtext.Sanitize(fmt.Sprintf("%v", raw))
		}
	}
}
