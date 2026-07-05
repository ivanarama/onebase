package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// ── Рантайм событий управляемых форм (план 37, этап 8) ───────────────────
//
// POST /ui/{kind}/{entity}/form-event
//
// Form-data:
//   _id       — uuid документа (пусто = новый)
//   _element  — имя элемента формы (пусто = form-level event)
//   _event    — "Нажатие" | "ПриИзменении" | "ПриОткрытии" | ...
//   _form     — имя формы (опционально; берётся первая managed object form)
//   <field>   — текущие значения формы (как при сохранении)
//   tp.X.Y.Z  — значения табличных частей
//
// Response (JSON):
//   { ok: true, values: {field: val}, tableparts: {tp: [rows]}, messages: [str], error: "" }
//
// Логика: строим *runtime.Object из form-data → находим *ast.ProcedureDecl
// в form.ProgramAST по имени из form.Handlers[event] или element.Handlers[event]
// → запускаем через s.interp.Run(proc, obj, vars) с buildDSLVarsWithMessages
// для сбора Сообщить() → сериализуем обратно obj.Fields / obj.TablePartRows.

// formEventResponse — структура ответа JSON.
type formEventResponse struct {
	OK         bool                        `json:"ok"`
	Values     map[string]any              `json:"values,omitempty"`
	TableParts map[string][]map[string]any `json:"tableparts,omitempty"`
	FormTables map[string][]map[string]any `json:"formTables,omitempty"`
	Messages   []string                    `json:"messages,omitempty"`
	Error      string                      `json:"error,omitempty"`
	// PickerData != nil — обработчик фазы 1 вызвал ПоказатьПодбор: клиент
	// открывает модальный диалог мультивыбора вместо применения ТЧ (план 46).
	PickerData *pickerPayload `json:"pickerData,omitempty"`
	// ChoiceList — динамический список значений для элемента ПолеСписка,
	// сформированный обработчиком НачалоВыбора (билтин ДобавитьЗначениеСписка).
	// Клиент заполняет им <select> того элемента, что инициировал событие.
	ChoiceList []choiceListItem `json:"choiceList,omitempty"`
}

// handleManagedFormEvent — единая точка обработки событий managed-форм.
func (s *Server) handleManagedFormEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		// ParseMultipartForm обрабатывает и URL-encoded, и multipart;
		// fallback на ParseForm для GET/простых POST.
		if err := r.ParseForm(); err != nil {
			enc.Encode(formEventResponse{Error: "bad form: " + err.Error()})
			return
		}
	}

	entityName := chi.URLParam(r, "entity")
	if entityName == "" {
		enc.Encode(formEventResponse{Error: "entity required"})
		return
	}
	entity := s.reg.GetEntity(entityName)
	if entity == nil {
		enc.Encode(formEventResponse{Error: "entity not found: " + entityName})
		return
	}

	formKind := strings.ToLower(strings.TrimSpace(r.FormValue("_kind")))
	if formKind == "" {
		formKind = "object"
	}
	form := pickManagedForm(entity, formKind)
	if form == nil {
		enc.Encode(formEventResponse{Error: "managed form not found for " + entityName})
		return
	}
	progAny := form.ProgramAST
	if progAny == nil {
		// .form.os не загружен или не распарсен — обработчики недоступны.
		enc.Encode(formEventResponse{OK: true})
		return
	}
	program, ok := progAny.(*ast.Program)
	if !ok || program == nil {
		enc.Encode(formEventResponse{Error: "form AST type mismatch"})
		return
	}

	elementName := strings.TrimSpace(r.FormValue("_element"))
	eventName := strings.TrimSpace(r.FormValue("_event"))
	if eventName == "" {
		enc.Encode(formEventResponse{Error: "_event required"})
		return
	}

	// Найти имя процедуры, которая привязана к событию.
	procName := resolveHandlerProc(form, elementName, eventName)
	if procName == "" {
		// Нет привязки — это не ошибка, просто событие декларативное.
		enc.Encode(formEventResponse{OK: true})
		return
	}

	// Найти AST процедуры.
	var decl *ast.ProcedureDecl
	for _, p := range program.Procedures {
		if strings.EqualFold(p.Name.Literal, procName) {
			decl = p
			break
		}
	}
	if decl == nil {
		enc.Encode(formEventResponse{OK: true, Messages: []string{
			"⚠ Процедура «" + procName + "» не найдена в .form.os",
		}})
		return
	}

	// Лимит richtext проверяем по СЫРОМУ значению формы до санитайза, как в
	// parseSubmitForm/handlers_entity — иначе мега-blob обходит лимит через
	// событийный путь managed-формы (DoS/раздувание БД), XSS при этом нет.
	if err := checkRichTextLimits(r, entity); err != nil {
		enc.Encode(formEventResponse{Error: s.errText(r, err)})
		return
	}

	// Построить объект из текущих form-values.
	obj := buildObjectFromForm(r, entity)

	// Подмешать ValueTable-данные из vt.<name>.<idx>.<field>.
	if vtRows := parseValueTableRows(r, form); vtRows != nil {
		if obj.TablePartRows == nil {
			obj.TablePartRows = map[string][]map[string]any{}
		}
		for k, v := range vtRows {
			obj.TablePartRows[k] = v
		}
	}
	// Подмешать ссылки → *runtime.Ref, как при сохранении (нужно для
	// Объект.Покупатель.Наименование и проч.).
	s.enrichHeaderRefs(r.Context(), entity, obj)
	for _, tp := range entity.TableParts {
		if rows, ok := obj.TablePartRows[tp.Name]; ok {
			s.enrichTPRowsWithRefs(r.Context(), tp, rows)
		}
	}

	// Сборка vars c builtin Сообщить, копящим сообщения. Дополнительно
	// прокидываем «Объект» и «ЭтотОбъект» как formObjectThis — обёртку,
	// которая возвращает *formTpProxy для табличных частей (чтобы
	// `Объект.Товары.Добавить()` реально модифицировал obj).
	mc := runtime.NewMovementsCollector(entity.Name, obj.ID)
	var msgs []string
	vars := s.buildDSLVarsWithMessages(r.Context(), mc, &msgs)
	thisObj := &formObjectThis{obj: obj, entity: entity, form: form}
	vars["Объект"] = thisObj
	vars["ЭтотОбъект"] = thisObj

	// Передаём все процедуры формы, чтобы обработчик мог вызывать
	// вспомогательные функции из того же .form.os (evalCall ищет
	// их по ключу __form_procs__).
	formProcs := make(map[string]*ast.ProcedureDecl, len(program.Procedures))
	for _, p := range program.Procedures {
		formProcs[strings.ToLower(p.Name.Literal)] = p
	}
	vars["__form_procs__"] = formProcs

	// Подбор (план 46). Фаза 1: билтин ПоказатьПодбор копит payload в sink —
	// после Run он уйдёт в ответ как pickerData, и клиент откроет диалог.
	var picker *pickerPayload
	pickerFn := newPickerBuiltin(&picker)
	vars["ПоказатьПодбор"] = pickerFn
	vars["ShowPicker"] = pickerFn

	// Динамический список значений (НачалоВыбора): билтин ДобавитьЗначениеСписка
	// копит пункты в sink; после Run они уходят в ответ как choiceList, и клиент
	// заполняет ими <select> элемента ПолеСписка.
	var choiceItems []choiceListItem
	choiceFn := newChoiceListBuiltin(&choiceItems)
	vars["ДобавитьЗначениеСписка"] = choiceFn
	vars["AddChoiceItem"] = choiceFn

	// Фаза 2: результат диалога приходит как _pick_result (JSON) → переменная
	// ПодборРезультат (Массив структур) для обработчика события Выбор.
	if pr := parsePickResult(r.FormValue("_pick_result")); pr != nil {
		vars["ПодборРезультат"] = pr
		vars["PickResult"] = pr
	}

	// Команды ТЧ: выделенные строки (_tp_selected = CSV индексов строк ТЧ из
	// _tp) → переменная ВыделенныеСтроки (Массив строк ТЧ) для обработчиков
	// команд вида «изменить выделенные».
	if sel := selectedTPRows(r, obj); sel != nil {
		vars["ВыделенныеСтроки"] = sel
		vars["SelectedRows"] = sel
	}
	addTPEventContextVars(r, obj, vars)

	// Выполнение процедуры. Ошибка DSL отдаётся в JSON, не как 500 —
	// клиент покажет красный баннер и не закроет форму.
	if runErr := s.interp.Run(decl, thisObj, vars); runErr != nil {
		values, tableParts, formTables, outMsgs := s.serializeManagedFormEventState(form, entity, obj, msgs)
		enc.Encode(formEventResponse{
			OK:         false,
			Values:     values,
			TableParts: tableParts,
			FormTables: formTables,
			Messages:   outMsgs,
			Error:      runErr.Error(),
			PickerData: picker,
		})
		return
	}

	values, tableParts, formTables, outMsgs := s.serializeManagedFormEventState(form, entity, obj, msgs)
	enc.Encode(formEventResponse{
		OK:         true,
		Values:     values,
		TableParts: tableParts,
		Messages:   outMsgs,
		FormTables: formTables,
		PickerData: picker,
		ChoiceList: choiceItems,
	})
}

func (s *Server) serializeManagedFormEventState(form *metadata.FormModule, entity *metadata.Entity, obj *runtime.Object, msgs []string) (map[string]any, map[string][]map[string]any, map[string][]map[string]any, []string) {
	if obj == nil {
		return nil, nil, nil, msgs
	}
	values := serializeFieldsForEntity(obj.Fields, entity)
	tableParts := serializeTablePartRowsForEntity(obj.TablePartRows, entity)
	if s.interp != nil {
		if warnings := applyManagedFormConditionalStyles(form, tableParts, values, newInterpEvaluator(s.interp)); len(warnings) > 0 {
			msgs = append(msgs, warnings...)
		}
	}
	return values, tableParts, formTablesFromRows(tableParts, form), msgs
}

// selectedTPRows читает _tp (имя ТЧ) и _tp_selected (CSV индексов отмеченных
// строк) из запроса и возвращает Массив соответствующих строк ТЧ (обёрнутых
// в MapThis) для DSL-обработчиков команд ТЧ. nil, если выделения нет.
//
// Индексы соответствуют отрисованным (непустым) строкам ТЧ — тем же, что
// собирает parseTablePartRows. Если пользователь оставил полностью пустую
// строку посередине, она отфильтровывается и при сдвиге индексов выделение
// может не совпасть; для команд «по выделенным» это приемлемое ограничение.
func selectedTPRows(r *http.Request, obj *runtime.Object) *interpreter.Array {
	tpName := strings.TrimSpace(r.FormValue("_tp"))
	selRaw := strings.TrimSpace(r.FormValue("_tp_selected"))
	if tpName == "" || selRaw == "" {
		return nil
	}
	rows := obj.TablePartRows[tpName]
	if len(rows) == 0 {
		return nil
	}
	var items []any
	for _, part := range strings.Split(selRaw, ",") {
		idx, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || idx < 0 || idx >= len(rows) {
			continue
		}
		items = append(items, &interpreter.MapThis{M: rows[idx]})
	}
	if len(items) == 0 {
		return nil
	}
	return interpreter.NewArray(items)
}

// addTPEventContextVars добавляет контекст события табличной части. Для
// ПриИзменении SlickGrid присылает строку/колонку изменённой ячейки; для
// команд ТЧ остаётся хотя бы имя табличной части. Имена без параметров
// сохраняют совместимость существующих обработчиков.
func addTPEventContextVars(r *http.Request, obj *runtime.Object, vars map[string]any) {
	tpName := strings.TrimSpace(r.FormValue("_tp"))
	if tpName == "" {
		return
	}
	vars["ИмяТабличнойЧасти"] = tpName
	vars["ТекущаяТабличнаяЧасть"] = tpName
	vars["TablePartName"] = tpName

	if colName := strings.TrimSpace(r.FormValue("_tp_col")); colName != "" {
		vars["ТекущаяКолонка"] = colName
		vars["ИмяКолонки"] = colName
		vars["CurrentColumn"] = colName
		vars["ColumnName"] = colName
	}
	if colIdx, ok := formEventInt(r, "_tp_col_index"); ok {
		vars["ИндексКолонки"] = float64(colIdx)
		vars["ColumnIndex"] = float64(colIdx)
	}

	rowIdx, rowOK := formEventInt(r, "_tp_row")
	rowNum, rowNumOK := formEventInt(r, "_tp_row_number")
	if !rowOK && rowNumOK {
		rowIdx = rowNum - 1
		rowOK = rowIdx >= 0
	}
	if !rowOK || rowIdx < 0 {
		return
	}
	if !rowNumOK {
		rowNum = rowIdx + 1
	}
	vars["ИндексСтроки"] = float64(rowIdx)
	vars["НомерСтроки"] = float64(rowNum)
	vars["RowIndex"] = float64(rowIdx)
	vars["RowNumber"] = float64(rowNum)

	if obj == nil || obj.TablePartRows == nil {
		return
	}
	rows := obj.TablePartRows[tpName]
	if rowIdx >= len(rows) {
		return
	}
	row := &interpreter.MapThis{M: rows[rowIdx]}
	vars["ТекущаяСтрока"] = row
	vars["CurrentRow"] = row
}

func formEventInt(r *http.Request, key string) (int, bool) {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// serializeFieldsForEntity нормализует имена полей к оригинальному регистру
// (Object.Set хранит lowercase) и сериализует значения. Без нормализации
// клиентский applyValues не найдёт input name="Дата" среди ключей "дата".
func serializeFieldsForEntity(in map[string]any, entity *metadata.Entity) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	handled := make(map[string]bool) // нижне-регистровые ключи, поглощённые полями сущности
	if entity != nil {
		for _, f := range entity.Fields {
			low := strings.ToLower(f.Name)
			// Мутация хука (Объект.Поле = …) пишется через Object.Set в нижнем
			// регистре, а исходное значение из формы — в оригинальном. При дубле
			// детерминированно побеждает мутация (раньше исход зависел от порядка
			// обхода карты и был флаки).
			v, ok := in[low]
			if !ok {
				v, ok = in[f.Name]
			}
			if !ok {
				continue
			}
			out[f.Name] = serializeValue(v)
			handled[low] = true
		}
	}
	// Остальные ключи (parent_id, is_folder, реквизиты формы вне сущности).
	for k, v := range in {
		if handled[strings.ToLower(k)] {
			continue
		}
		out[k] = serializeValue(v)
	}
	return out
}

// serializeTablePartRowsForEntity дополнительно нормализует имена полей
// в строках ТЧ. Внутри строки ключи тоже могут оказаться lowercase после
// MapThis.Set, поэтому ищем оригинальный регистр в entity.TableParts.Fields.
func serializeTablePartRowsForEntity(tps map[string][]map[string]any, entity *metadata.Entity) map[string][]map[string]any {
	if tps == nil {
		return nil
	}
	tpFields := make(map[string][]metadata.Field)
	if entity != nil {
		for _, tp := range entity.TableParts {
			tpFields[tp.Name] = tp.Fields
		}
	}
	out := make(map[string][]map[string]any, len(tps))
	for tpName, rows := range tps {
		fields := tpFields[tpName]
		outRows := make([]map[string]any, len(rows))
		for i, row := range rows {
			outRow := make(map[string]any, len(row))
			for fk, fv := range row {
				outKey := fk
				for _, f := range fields {
					if strings.EqualFold(f.Name, fk) {
						outKey = f.Name
						break
					}
				}
				outRow[outKey] = serializeValue(fv)
			}
			outRows[i] = outRow
		}
		out[tpName] = outRows
	}
	return out
}

// resolveHandlerProc возвращает имя процедуры-обработчика по уровню события.
// Если elementName пуст — ищет form.Handlers[event] (form-level).
// Иначе — element.Handlers[event] у указанного элемента дерева.
func resolveHandlerProc(form *metadata.FormModule, elementName, eventName string) string {
	evt := metadata.FormEventType(eventName)
	if elementName == "" {
		if form.Handlers != nil {
			if proc, ok := form.Handlers[evt]; ok {
				return proc
			}
		}
		return ""
	}
	el := form.GetElementByName(elementName)
	if el == nil || el.Handlers == nil {
		return ""
	}
	return el.Handlers[evt]
}

// buildObjectFromForm восстанавливает *runtime.Object из POST-формы.
// Использует те же helper'ы что и сохранение документа (formToFields,
// parseTablePartRows), чтобы поведение было идентично.
func buildObjectFromForm(r *http.Request, entity *metadata.Entity) *runtime.Object {
	fields := formToFields(r, entity)
	tpRows := parseTablePartRows(r, entity)
	idStr := strings.TrimSpace(r.FormValue("_id"))
	var id uuid.UUID
	if idStr != "" {
		if parsed, err := uuid.Parse(idStr); err == nil {
			id = parsed
		}
	}
	if id == uuid.Nil {
		id = uuid.New()
	}
	return &runtime.Object{
		Type:          entity.Name,
		Kind:          entity.Kind,
		ID:            id,
		Fields:        fields,
		TablePartRows: tpRows,
	}
}

// parseValueTableRows парсит vt.<name>.<idx>.<field> и tp_json.<name> из запроса
// для формовых атрибутов ValueTable. В отличие от parseTablePartRows, работает
// не с entity.TableParts, а с формовыми атрибутами типа ValueTable.
func parseValueTableRows(r *http.Request, form *metadata.FormModule) map[string][]map[string]any {
	if form == nil {
		return nil
	}
	result := make(map[string][]map[string]any)
	for _, attr := range form.Attributes {
		if !strings.EqualFold(attr.TypeRef, "ValueTable") || len(attr.Columns) == 0 {
			continue
		}
		name := attr.Name
		// SlickGrid-style JSON payload.
		if jsonBlob := r.FormValue("tp_json." + name); jsonBlob != "" {
			var rows []map[string]any
			if err := json.Unmarshal([]byte(jsonBlob), &rows); err == nil {
				cleaned := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					if len(row) == 0 {
						continue
					}
					converted := make(map[string]any, len(row))
					for _, col := range attr.Columns {
						raw := ""
						if v, ok := row[col.Name]; ok {
							raw = fmt.Sprintf("%v", v)
						} else {
							for k, v := range row {
								if strings.EqualFold(k, col.Name) {
									raw = fmt.Sprintf("%v", v)
									break
								}
							}
						}
						switch strings.ToLower(col.TypeRef) {
						case "number":
							if n, err := strconv.ParseFloat(raw, 64); err == nil {
								converted[col.Name] = n
							} else {
								converted[col.Name] = raw
							}
						case "bool":
							converted[col.Name] = raw == "true"
						default:
							converted[col.Name] = raw
						}
					}
					empty := true
					for _, col := range attr.Columns {
						if v, ok := converted[col.Name]; ok && fmt.Sprintf("%v", v) != "" {
							empty = false
							break
						}
					}
					if !empty {
						cleaned = append(cleaned, converted)
					}
				}
				result[name] = cleaned
				continue
			}
		}
		// Legacy vt.<name>.<idx>.<field> parsing.
		maxIdx := -1
		prefix := "vt." + name + "."
		for key := range r.Form {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) < 2 {
				continue
			}
			if idx, err := strconv.Atoi(parts[0]); err == nil && idx > maxIdx {
				maxIdx = idx
			}
		}
		if maxIdx < 0 {
			continue
		}
		rows := make([]map[string]any, maxIdx+1)
		for i := range rows {
			rows[i] = make(map[string]any)
		}
		for key, vals := range r.Form {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) < 2 {
				continue
			}
			idx, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			fieldName := parts[1]
			if len(vals) > 0 {
				rows[idx][fieldName] = vals[0]
			}
		}
		var cleaned []map[string]any
		for _, row := range rows {
			empty := true
			for _, col := range attr.Columns {
				if v, ok := row[col.Name]; ok && fmt.Sprintf("%v", v) != "" {
					empty = false
					break
				}
			}
			if empty {
				continue
			}
			converted := make(map[string]any, len(row))
			for _, col := range attr.Columns {
				// row[col.Name] может отсутствовать (колонка не пришла в форме) —
				// fmt.Sprintf("%v", nil) дал бы "<nil>". Берём "" при отсутствии.
				raw := ""
				if v, ok := row[col.Name]; ok {
					raw = fmt.Sprintf("%v", v)
				}
				switch strings.ToLower(col.TypeRef) {
				case "number":
					if n, err := strconv.ParseFloat(raw, 64); err == nil {
						converted[col.Name] = n
					} else {
						converted[col.Name] = raw
					}
				case "bool":
					converted[col.Name] = raw == "true"
				default:
					converted[col.Name] = raw
				}
			}
			cleaned = append(cleaned, converted)
		}
		result[name] = cleaned
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func serializeValue(v any) any {
	if v == nil {
		return ""
	}
	type refLike interface{ GetRefUUID() string }
	if r, ok := v.(refLike); ok {
		return r.GetRefUUID()
	}
	switch t := v.(type) {
	case uuid.UUID:
		return t.String()
	case time.Time:
		// input type=datetime-local ожидает ISO 8601 без timezone и без
		// секунд. Без явного формата time.Time.String() даёт
		// "2026-05-26 10:00:00 +0300 MSK" — браузер не распознаёт и
		// очищает значение поля.
		return t.Format("2006-01-02T15:04")
	case *time.Time:
		if t == nil {
			return ""
		}
		return t.Format("2006-01-02T15:04")
	case fmt.Stringer:
		return t.String()
	}
	return v
}

// handleProcessorFormEvent обрабатывает события managed-формы обработки.
// Аналог handleManagedFormEvent, но вместо Entity использует виртуальную entity
// из параметров обработки. Кнопка «Выполнить» запускает proc.os через interp.
func (s *Server) handleProcessorFormEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			enc.Encode(formEventResponse{Error: "bad form: " + err.Error()})
			return
		}
	}

	procName := chi.URLParam(r, "name")
	if procName == "" {
		enc.Encode(formEventResponse{Error: "processor name required"})
		return
	}
	proc := s.reg.GetProcessor(procName)
	if proc == nil {
		enc.Encode(formEventResponse{Error: "processor not found: " + procName})
		return
	}
	if !s.can(r, "processor", proc.Name, "run") {
		enc.Encode(formEventResponse{Error: "доступ запрещён"})
		return
	}
	// Тот же trust-гейт, что и у processorRun: form-event исполняет DSL
	// обработки (form-обработчики и кнопка «Выполнить»), поэтому недоверенную
	// внешнюю обработку здесь тоже может запускать только администратор —
	// иначе неадмин обходил бы проверку через /form-event.
	if !s.canRunExternalProc(r, proc) {
		enc.Encode(formEventResponse{Error: "доступ запрещён"})
		return
	}

	form := proc.ManagedForm()
	if form == nil {
		enc.Encode(formEventResponse{Error: "managed form not found for " + procName})
		return
	}

	elementName := strings.TrimSpace(r.FormValue("_element"))
	eventName := strings.TrimSpace(r.FormValue("_event"))
	if eventName == "" {
		enc.Encode(formEventResponse{Error: "_event required"})
		return
	}

	// Try form-level handler (from .form.os AST)
	progAny := form.ProgramAST
	var program *ast.Program
	if progAny != nil {
		if p, ok := progAny.(*ast.Program); ok && p != nil {
			program = p
		}
	}

	if program != nil {
		procName := resolveHandlerProc(form, elementName, eventName)
		if procName != "" {
			var decl *ast.ProcedureDecl
			for _, p := range program.Procedures {
				if strings.EqualFold(p.Name.Literal, procName) {
					decl = p
					break
				}
			}
			if decl != nil {
				virtEntity := processorVirtualEntity(proc)
				obj := buildObjectFromForm(r, virtEntity)
				mc := runtime.NewMovementsCollector("processor", uuid.Nil)
				var msgs []string
				vars := s.buildDSLVarsWithMessages(r.Context(), mc, &msgs)
				thisObj := &formObjectThis{obj: obj, entity: virtEntity}
				vars["Объект"] = thisObj
				vars["ЭтотОбъект"] = thisObj
				vars["Параметры"] = thisObj
				interpreter.InjectMaket(vars, proc.Layout)

				// Передаём все процедуры формы для вызовов из .form.os.
				formProcs := make(map[string]*ast.ProcedureDecl, len(program.Procedures))
				for _, p := range program.Procedures {
					formProcs[strings.ToLower(p.Name.Literal)] = p
				}
				vars["__form_procs__"] = formProcs

				// Подбор (план 46), как в handleManagedFormEvent: фаза 1 копит
				// payload через ПоказатьПодбор → pickerData в ответе; фаза 2
				// отдаёт _pick_result в ПодборРезультат для обработчика Выбор.
				var picker *pickerPayload
				pickerFn := newPickerBuiltin(&picker)
				vars["ПоказатьПодбор"] = pickerFn
				vars["ShowPicker"] = pickerFn
				if pr := parsePickResult(r.FormValue("_pick_result")); pr != nil {
					vars["ПодборРезультат"] = pr
					vars["PickResult"] = pr
				}

				if runErr := s.interp.Run(decl, thisObj, vars); runErr != nil {
					values, tableParts, formTables, outMsgs := s.serializeManagedFormEventState(form, virtEntity, obj, msgs)
					enc.Encode(formEventResponse{
						OK:         false,
						Values:     values,
						TableParts: tableParts,
						FormTables: formTables,
						Messages:   outMsgs,
						Error:      runErr.Error(),
						PickerData: picker,
					})
					return
				}

				values, tableParts, formTables, outMsgs := s.serializeManagedFormEventState(form, virtEntity, obj, msgs)
				enc.Encode(formEventResponse{
					OK:         true,
					Values:     values,
					TableParts: tableParts,
					FormTables: formTables,
					Messages:   outMsgs,
					PickerData: picker,
				})
				return
			}
		}
	}

	// No form handler — for "Нажатие" on execute button, run processor logic
	if eventName == string(metadata.FormEventOnClick) {
		if proc.External {
			// Исполнение внешней обработки (DSL) всегда логируем — как в processorRun.
			s.auditExtProcRun(r, proc.Name)
		}
		paramValues := map[string]any{}
		for _, p := range proc.Params {
			paramValues[p.Name] = parseParamValue(r.FormValue(p.Name), p.Type)
		}

		procDecl := s.reg.GetProcedure(proc.Name, "Выполнить")
		if procDecl == nil {
			enc.Encode(formEventResponse{OK: true})
			return
		}

		userKey := userKeyFromRequest(r)
		var msgs []string
		msgFunc := interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
			if len(args) > 0 {
				text := fmt.Sprintf("%v", args[0])
				msgs = append(msgs, text)
				s.messages.Push(userKey, text)
			}
			return nil, nil
		})

		paramsThis := &interpreter.MapThis{M: paramValues}
		mc := runtime.NewMovementsCollector("processor", uuid.Nil)
		dslVars := s.buildDSLVars(r.Context(), mc)
		dslVars["Параметры"] = paramsThis
		dslVars["Сообщить"] = msgFunc
		dslVars["Message"] = msgFunc
		interpreter.InjectMaket(dslVars, proc.Layout)

		err := s.interp.Run(procDecl, paramsThis, dslVars)
		if err != nil {
			enc.Encode(formEventResponse{
				OK:       false,
				Messages: msgs,
				Error:    err.Error(),
			})
			return
		}
		enc.Encode(formEventResponse{
			OK:       true,
			Messages: msgs,
		})
		return
	}

	enc.Encode(formEventResponse{OK: true})
}

func formTablesFromRows(rows map[string][]map[string]any, form *metadata.FormModule) map[string][]map[string]any {
	if rows == nil || form == nil {
		return nil
	}
	vtNames := map[string]bool{}
	for _, attr := range form.Attributes {
		if attr != nil && strings.EqualFold(attr.TypeRef, "ValueTable") {
			vtNames[attr.Name] = true
		}
	}
	if len(vtNames) == 0 {
		return nil
	}
	result := make(map[string][]map[string]any)
	for k, v := range rows {
		if vtNames[k] && len(v) > 0 {
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
