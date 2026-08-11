package ui

import (
	"context"
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
	OK             bool                        `json:"ok"`
	Values         map[string]any              `json:"values,omitempty"`
	TableParts     map[string][]map[string]any `json:"tableparts,omitempty"`
	FormTables     map[string][]map[string]any `json:"formTables,omitempty"`
	ConditionalCSS string                      `json:"conditionalCss"`
	Messages       []string                    `json:"messages,omitempty"`
	Error          string                      `json:"error,omitempty"`
	// PickerData != nil — обработчик фазы 1 вызвал ПоказатьПодбор: клиент
	// открывает модальный диалог мультивыбора вместо применения ТЧ (план 46).
	PickerData *pickerPayload `json:"pickerData,omitempty"`
	// SavedID заполняется, когда обработчик записал ЕЩЁ НЕ СОХРАНЁННУЮ форму
	// через Объект.Записать(). Клиент подставляет его в _id следующих событий и
	// в адрес страницы — иначе второе действие подряд создало бы второй документ.
	SavedID string `json:"savedId,omitempty"`
	// Version — текущая версия записи после обработчика. Клиент кладёт её в
	// скрытое поле _version: обработчик, записавший объект, версию поднял, а
	// форма держала прочитанную при отрисовке — и следующая кнопка «Записать»
	// упиралась в «объект изменён другим пользователем».
	Version int64 `json:"version,omitempty"`
	// ChoiceList — динамический список значений для элемента ПолеСписка,
	// сформированный обработчиком НачалоВыбора (билтин ДобавитьЗначениеСписка).
	// Клиент заполняет им <select> того элемента, что инициировал событие.
	ChoiceList []choiceListItem `json:"choiceList,omitempty"`
	// RefOptions — <option> для ссылочных значений из Values: <select> рисуется
	// первой страницей справочника, и присвоенная обработчиком ссылка за её
	// пределами иначе молча обнуляла бы поле (#615, см. eventRefOptions).
	RefOptions map[string][]map[string]any `json:"refOptions,omitempty"`
}

// handleManagedFormEvent — единая точка обработки событий managed-форм.
func (s *Server) handleManagedFormEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)

	// Сущность резолвим ДО разбора тела: от неё зависит предел (#629). Пределы не
	// композируются — прежняя пара «1 МиБ, затем limitMultipartRequest на 52 МиБ»
	// связывала всегда по внутреннему мегабайту, из-за чего кнопка на форме с
	// большим richtext ломалась ещё до записи, а внешний предел был мёртв.
	entityName := chi.URLParam(r, "entity")
	if entityName == "" {
		respondJSON(enc, formEventResponse{Error: "entity required"})
		return
	}
	entity := s.reg.GetEntity(entityName)
	if entity == nil {
		respondJSON(enc, formEventResponse{Error: "entity not found: " + entityName})
		return
	}
	entityKind := string(entity.Kind)
	canRead := s.can(r, entityKind, entity.Name, "read")
	canWrite := s.can(r, entityKind, entity.Name, "write")
	if !canRead && !canWrite {
		w.WriteHeader(http.StatusForbidden)
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.entityFormBodyLimit(r, entity))
	if err := parseBoundedForm(r, 32<<20); err != nil {
		w.WriteHeader(uploadErrorStatus(err))
		respondJSON(enc, formEventResponse{Error: s.errText(r, formBodyError(err, entity))})
		return
	}
	// Every value describing browser form state comes from the POST body.
	// Request.Form and FormValue normally merge the query string; using one
	// normalized request prevents query-only ids, targets, fields and table rows
	// from reaching any of the existing form parsers below.
	r = postFormOnlyRequest(r)
	rawID := strings.TrimSpace(r.FormValue("_id"))
	formKind := strings.ToLower(strings.TrimSpace(r.FormValue("_kind")))
	if formKind == "" {
		formKind = "object"
	}
	form := pickManagedForm(entity, formKind)
	if form == nil {
		respondJSON(enc, formEventResponse{Error: "managed form not found for " + entityName})
		return
	}
	tableAuthorities, err := managedFormTableAuthorities(form, entity.TableParts, canWrite)
	if err != nil {
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}
	isNewObject := rawID == "" && (strings.EqualFold(form.Kind, "object") || form.Kind == "" && formKind == "object")
	if isNewObject {
		if !canWrite {
			w.WriteHeader(http.StatusForbidden)
			respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
			return
		}
	} else if !canRead {
		w.WriteHeader(http.StatusForbidden)
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	} else if rawID != "" {
		id, err := uuid.Parse(rawID)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			respondJSON(enc, formEventResponse{Error: "некорректный идентификатор записи"})
			return
		}
		_, exists, err := s.store.EntityVersionExists(r.Context(), entity.Name, id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			respondJSON(enc, formEventResponse{Error: s.errText(r, err)})
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			respondJSON(enc, formEventResponse{Error: "запись не найдена"})
			return
		}
		if !s.rowAllowsID(r.Context(), entity, "read", id) {
			w.WriteHeader(http.StatusForbidden)
			respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
			return
		}
	}
	elementName := strings.TrimSpace(r.FormValue("_element"))
	eventName := strings.TrimSpace(r.FormValue("_event"))
	progAny := form.ProgramAST
	if progAny == nil {
		// Preserve the historical no-op for forms without a loaded .form.os, but
		// still enforce table authority: a missing AST must not turn a forged
		// TP/ValueTable target into an accepted event for a read-only user.
		if eventName != "" {
			_, eventTarget, _, eligibilityErr := resolveBrowserFormEvent(form, elementName, eventName, false)
			isTableTarget := eventTarget.parentTablePart != nil ||
				eventTarget.element != nil && eventTarget.element.Kind == metadata.FormElementTablePart
			if isTableTarget {
				if err := validateManagedFormTableEventTarget(tableAuthorities, eventTarget); err != nil {
					respondJSON(enc, formEventResponse{Error: err.Error()})
					return
				}
			}
			if !isTableTarget && strings.TrimSpace(r.FormValue("_tp")) != "" && eligibilityErr != nil {
				respondJSON(enc, formEventResponse{Error: eligibilityErr.Error()})
				return
			}
		}
		respondJSON(enc, formEventResponse{OK: true})
		return
	}
	if eventName == "" {
		respondJSON(enc, formEventResponse{Error: "_event required"})
		return
	}

	// Найти имя процедуры, которая привязана к событию.
	procName, eventTarget, _, eligibilityErr := resolveBrowserFormEvent(form, elementName, eventName, false)
	if eligibilityErr != nil {
		respondJSON(enc, formEventResponse{Error: eligibilityErr.Error()})
		return
	}
	if err := validateManagedFormTableEventTarget(tableAuthorities, eventTarget); err != nil {
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}
	program, ok := progAny.(*ast.Program)
	if !ok || program == nil {
		respondJSON(enc, formEventResponse{Error: "form AST type mismatch"})
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
		respondJSON(enc, formEventResponse{OK: true, Messages: []string{
			"⚠ Процедура «" + procName + "» не найдена в .form.os",
		}})
		return
	}

	// Лимит richtext проверяем по СЫРОМУ значению формы до санитайза, как в
	// parseSubmitForm/handlers_entity — иначе мега-blob обходит лимит через
	// событийный путь managed-формы (DoS/раздувание БД), XSS при этом нет.
	if err := checkRichTextLimits(r, entity); err != nil {
		respondJSON(enc, formEventResponse{Error: s.errText(r, err)})
		return
	}

	// Построить объект из текущих form-values.
	obj, err := buildObjectFromForm(r, entity, form, canWrite)
	if err != nil {
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}

	// Дочитать поля, которых нет на форме (или которые пришли disabled), из БД —
	// тем же правилом, что и при сохранении. Без этого обработчик видит nil у
	// неразмещённого реквизита, а applyValues на клиенте очищает такие поля прямо
	// в DOM ещё до записи. Для новой записи восстанавливать нечего; ошибку чтения
	// глотаем — событие не должно падать из-за удалённой записи.
	// Гейт по сырому _id: buildObjectFromForm для новой записи генерирует
	// случайный uuid, поэтому проверка obj.ID != uuid.Nil была бы всегда истинной
	// и гоняла бы лишний запрос в БД на каждое событие.
	existingFormID := strings.TrimSpace(r.FormValue("_id"))
	if existingFormID != "" {
		_ = s.restoreUnsubmittedFields(r.Context(), r, entity, form, obj.ID, obj.Fields)
	}
	persistedID := uuid.Nil
	if existingFormID != "" {
		persistedID = obj.ID
	}
	if err := s.restoreUneditableTableParts(r.Context(), entity, form, persistedID, obj.TablePartRows, canWrite); err != nil {
		respondJSON(enc, formEventResponse{Error: s.errText(r, err)})
		return
	}

	// Псевдо-реквизит «Ссылка» самой записи — как в entityservice.Save. Без него
	// Объект.Ссылка в обработчике формы было Неопределено, и типовой вызов
	// Модуль.Действие(Объект.Ссылка) падал на «ПолучитьОбъект вызван у
	// Неопределено». Ставится ДО newFormObjectThis: attachObject предзагружает
	// реквизиты именно по этой ссылке.
	setFormSelfRef(r, entity, obj)

	// Реквизиты формы (attributes с save:false) — formToFields их не разбирает,
	// поэтому без этого шага Объект.<Реквизит> в обработчике всегда nil.
	s.mergeFormAttrValues(r.Context(), r, form, entity, obj)

	// Подмешать ValueTable-данные из vt.<name>.<idx>.<field>.
	vtRows, err := parseValueTableRowsForManagedForm(r, form, entity, canWrite)
	if err != nil {
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}
	if vtRows != nil {
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
	// txState — «живой» контекст: обработчик может позвать модуль, который
	// откроет транзакцию, и ссылки объекта обязаны выполнять ПолучитьОбъект()
	// внутри неё, а не ждать второго соединения (пул SQLite — одно).
	vars, txState := s.buildDSLVarsWithMessagesTx(r.Context(), mc, &msgs)
	defer rollbackDSLExecution(txState)
	thisObj := s.newFormObjectThisLive(r.Context(), txState, obj, entity, form, strings.TrimSpace(r.FormValue("_id")) == "")
	vars["Объект"] = thisObj
	vars["ЭтотОбъект"] = thisObj

	// Реквизиты формы видны и голым именем — как в модуле управляемой формы 1С,
	// где под Объект лежит только основной реквизит. Читаются через thisObj,
	// поэтому ссылочные приходят как *Ref, а ValueTable — как прокси таблицы.
	addFormAttrVars(form, entity, thisObj, vars)

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

	condRuntime := newFormConditionalRuntime(form)
	for k, v := range condRuntime.builtins() {
		vars[k] = v
	}

	// Фаза 2: результат диалога приходит как _pick_result (JSON) → переменная
	// ПодборРезультат (Массив структур) для обработчика события Выбор.
	if pr := parsePickResult(r.FormValue("_pick_result")); pr != nil {
		vars["ПодборРезультат"] = pr
		vars["PickResult"] = pr
	}

	if err := addEntityTPEventContext(r, entity, form, tableAuthorities, eventTarget, obj, vars); err != nil {
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}

	// Снимок полей до обработчика — по нему после Run отличаем «поле изменил сам
	// обработчик» от «поле осталось прежним», см. refreshFieldsWrittenByHandler.
	fieldsBefore := snapshotFieldValues(obj.Fields)

	// Снимок строк ТЧ (POST) и их состояние в базе ДО обработчика — по ним после
	// Run отличаем «модуль переписал ТЧ в базе» от «пользователь правил грид»
	// (issue #579, см. refreshTablePartsWrittenByHandler). Только для существующей
	// записи с табличными частями — иначе лишний запрос в БД на каждое событие.
	existingRecord := strings.TrimSpace(r.FormValue("_id")) != ""
	var tpBefore, tpDBBefore map[string][]map[string]any
	if existingRecord && len(entity.TableParts) > 0 && obj.ID != uuid.Nil {
		tpBefore = tablePartRowsSnapshot(obj.TablePartRows)
		tpDBBefore = s.tablePartRowsFromDB(r.Context(), entity, obj.ID)
	}

	// Выполнение процедуры. Ошибка DSL отдаётся в JSON, не как 500 —
	// клиент покажет красный баннер и не закроет форму.
	runErr := s.interp.Run(decl, thisObj, vars)
	// Незавершённая DSL-транзакция отменяется ДО перечитывания БД и сериализации:
	// иначе pgx удерживает соединение после запроса, а SQLite ждёт занятое
	// единственное соединение. Успешный выход с открытой транзакцией считается
	// ошибкой процедуры, чтобы конфигурационная ошибка не оставалась незаметной.
	runErr = finishDSLExecution(txState, runErr)
	liveCtx := txState.Ctx()
	// Перечитывать из базы имеет смысл только для записи, которая там есть:
	// либо форма открыта по _id, либо обработчик записал новую (тогда нужен и он —
	// номер от нумератора обязан приехать на экран «Создать» сразу). Гейт по
	// сырому _id, а не по obj.ID: buildObjectFromForm для новой записи генерирует
	// случайный uuid, поэтому obj.ID != uuid.Nil истинно ВСЕГДА и каждое событие
	// на «Создать» уходило бы в базу за несуществующей строкой. Ровно эта ловушка
	// описана выше у restoreUnsubmittedFields.
	if strings.TrimSpace(r.FormValue("_id")) != "" || savedFormID(thisObj) != "" {
		s.refreshFieldsWrittenByHandler(liveCtx, r, entity, form, obj, fieldsBefore)
	}
	if existingRecord && tpDBBefore != nil {
		s.refreshTablePartsWrittenByHandler(liveCtx, entity, obj, tpBefore, tpDBBefore)
	}
	if runErr != nil {
		resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, condRuntime.rules, msgs).response(false)
		resp.Error = interpreter.FormatUserError(runErr)
		resp.PickerData = picker
		// Обработчик мог записать форму и упасть уже после этого: id всё равно
		// нужен клиенту, иначе повтор действия создаст второй документ.
		resp.SavedID = savedFormID(thisObj)
		resp.Version = s.versionWrittenByHandler(liveCtx, entity, obj, thisObj)
		respondJSON(enc, resp)
		return
	}

	resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, condRuntime.rules, msgs).response(true)
	resp.PickerData = picker
	resp.ChoiceList = choiceItems
	resp.SavedID = savedFormID(thisObj)
	resp.Version = s.versionWrittenByHandler(liveCtx, entity, obj, thisObj)
	respondJSON(enc, resp)
}

// savedFormID возвращает id записи, если обработчик сохранил ЕЩЁ НЕ записанную
// форму через Объект.Записать(). Для уже существующей записи пусто — клиенту
// нечего менять.
func savedFormID(this *formObjectThis) string {
	if this == nil || !this.isNew || !this.saved || this.obj == nil {
		return ""
	}
	return this.obj.ID.String()
}

// formEventState — сериализованное состояние формы для ответа события.
//
// Возвращается одной структурой, а не набором значений, ради RefOptions: их
// собирает сама сериализация, и ни один из четырёх ответов не может их забыть.
// Прежняя россыпь возвратов и была бы ровно тем механизмом отказа, который
// разбирает #615, — инвариант, применённый в N−1 месте из N.
type formEventState struct {
	Values         map[string]any
	TableParts     map[string][]map[string]any
	FormTables     map[string][]map[string]any
	RefOptions     map[string][]map[string]any
	ConditionalCSS string
	Messages       []string
}

// response — единственное место, где состояние переносится в ответ.
func (st formEventState) response(ok bool) formEventResponse {
	return formEventResponse{
		OK:             ok,
		Values:         st.Values,
		TableParts:     st.TableParts,
		FormTables:     st.FormTables,
		RefOptions:     st.RefOptions,
		ConditionalCSS: st.ConditionalCSS,
		Messages:       st.Messages,
	}
}

func (s *Server) serializeManagedFormEventState(ctx context.Context, form *metadata.FormModule, entity *metadata.Entity, obj *runtime.Object, rules []metadata.FormCondRule, msgs []string) formEventState {
	conditionalCSS := formConditionalRulesCSS(rules)
	if obj == nil {
		return formEventState{ConditionalCSS: conditionalCSS, Messages: msgs}
	}
	fields := serializeFieldsForEntity(obj.Fields, entity)
	// Маска накладывается ЗДЕСЬ, на пути к клиенту, а не при чтении из БД
	// (issue #609). Разделение принципиальное: те же значения нужны настоящими
	// для записи и для DSL-обработчика — restoreUnsubmittedFields и
	// refreshFieldsWrittenByHandler дочитывают неприсланные реквизиты именно
	// затем, чтобы запись их не затёрла. Замаскировать при чтении значило бы
	// записать строку-маску в базу поверх реального значения, а это хуже
	// утечки: утечка обратима, испорченные данные — нет.
	//
	// serializeFieldsForEntity строит НОВУЮ карту, поэтому obj.Fields остаётся
	// нетронутым и обработчик продолжает видеть настоящие значения.
	s.maskRecord(ctx, entity, fields)
	values := normalizeFormAttrKeys(fields, form, entity)
	// Псевдо-реквизит «Ссылка» — контекст обработчика, а не значение формы:
	// в ответ он не едет, чтобы applyValues не искал под него элемент.
	for _, k := range []string{"ссылка", "reference"} {
		if _, isEntityField := entityFieldByName(entity, k); !isEntityField {
			delete(values, k)
		}
	}
	tableParts := serializeTablePartRowsForEntity(obj.TablePartRows, entity, form)
	if s.interp != nil {
		if warnings := applyManagedFormConditionalRules(form, tableParts, values, rules, newInterpEvaluator(s.interp)); len(warnings) > 0 {
			msgs = append(msgs, warnings...)
		}
	}
	return formEventState{
		Values:         values,
		TableParts:     tableParts,
		FormTables:     formTablesFromRows(tableParts, form),
		RefOptions:     s.eventRefOptions(ctx, form, entity, values),
		ConditionalCSS: conditionalCSS,
		Messages:       msgs,
	}
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
func serializeTablePartRowsForEntity(tps map[string][]map[string]any, entity *metadata.Entity, forms ...*metadata.FormModule) map[string][]map[string]any {
	if tps == nil {
		return nil
	}
	var form *metadata.FormModule
	if len(forms) > 0 {
		form = forms[0]
	}
	var declared []metadata.TablePart
	if entity != nil {
		declared = entity.TableParts
	}
	definitions, _ := metadata.FormTableDefinitions(form, declared)
	out := make(map[string][]map[string]any, len(tps))
	for tpName, rows := range tps {
		canonicalName := tpName
		var columns []string
		for _, definition := range definitions {
			if strings.EqualFold(definition.Name, tpName) {
				canonicalName = definition.Name
				columns = definition.Columns
				break
			}
		}
		outRows := make([]map[string]any, len(rows))
		for i, row := range rows {
			outRow := make(map[string]any, len(row))
			for _, column := range columns {
				// Старый MapThis мог оставить рядом исходный canonical key и новую
				// lowercase-мутацию. Мутация должна побеждать детерминированно.
				v, ok := row[strings.ToLower(column)]
				if !ok {
					v, ok = row[column]
				}
				if !ok {
					for key, value := range row {
						if strings.EqualFold(key, column) {
							v, ok = value, true
							break
						}
					}
				}
				if ok {
					outRow[column] = serializeValue(v)
				}
			}
			for fk, fv := range row {
				handled := false
				for _, column := range columns {
					if strings.EqualFold(column, fk) {
						handled = true
						break
					}
				}
				if !handled {
					outRow[fk] = serializeValue(fv)
				}
			}
			outRows[i] = outRow
		}
		out[canonicalName] = outRows
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
	// Обработчик самого элемента имеет приоритет — но именно по нужному событию.
	// Ветвление по «есть ли у элемента вообще Handlers» глушило фолбэк: элемент с
	// непустой картой без ключа этого события возвращал пустую строку, и кнопка
	// автопанели с тем же именем оказывалась мёртвой без всякой диагностики.
	if el := form.GetElementByName(elementName); el != nil && el.Handlers != nil {
		if proc := el.Handlers[evt]; proc != "" {
			return proc
		}
	}
	// Команда, размещённая автоматической командной панелью (не вручную элементом
	// kind: Кнопка), не имеет элемента в дереве — резолвим по имени команды на её
	// процедуру-Action. Фолбэк ограничен событиями, которые автопанель реально
	// шлёт: без этого команда выполнялась бы на любом событии, включая ПриИзменении
	// и мусорные имена.
	if evt != metadata.FormEventOnClick && evt != metadata.FormEventOnChoice {
		return ""
	}
	for _, c := range form.Commands {
		if c != nil && c.Action != "" && strings.EqualFold(c.Name, elementName) {
			return c.Action
		}
	}
	return ""
}

// buildObjectFromForm восстанавливает *runtime.Object из POST-формы.
// Использует те же helper'ы что и сохранение документа (formToFields,
// parseTablePartRows), чтобы поведение было идентично.
func buildObjectFromForm(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	canWrite bool,
) (*runtime.Object, error) {
	fields := formToFields(r, entity)
	tpRows, err := parseTablePartRowsForManagedForm(r, entity, form, canWrite)
	if err != nil {
		return nil, err
	}
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
	}, nil
}

// parseValueTableRowsForManagedForm parses only the representation rendered by
// a writable placement. ValueTable currently always uses DOM vt.* controls;
// tp_json.* is therefore never trusted for it merely because a client sent it.
func parseValueTableRowsForManagedForm(
	r *http.Request,
	form *metadata.FormModule,
	entity *metadata.Entity,
	canWrite bool,
) (map[string][]map[string]any, error) {
	if form == nil {
		return nil, nil
	}
	declared := []metadata.TablePart(nil)
	if entity != nil {
		declared = entity.TableParts
	}
	sources, err := managedFormTablePayloadSources(form, declared, canWrite)
	if err != nil {
		return nil, err
	}
	var bodyValues map[string][]string
	if r != nil {
		bodyValues = r.PostForm
	}
	result := make(map[string][]map[string]any)
	for _, attr := range form.Attributes {
		if attr == nil || !strings.EqualFold(attr.TypeRef, "ValueTable") || len(attr.Columns) == 0 {
			continue
		}
		allowedSources := sources[attr.Name]
		if allowedSources == 0 {
			continue
		}
		name := attr.Name
		columns := make([]string, 0, len(attr.Columns))
		for _, column := range attr.Columns {
			columns = append(columns, column.Name)
		}
		var rawRows []map[string]any
		switch allowedSources {
		case managedFormTableJSONPayload:
			blob, present, valueErr := managedFormSinglePayloadValue(bodyValues, "tp_json."+name)
			if valueErr != nil {
				return nil, valueErr
			}
			if !present {
				continue
			}
			decoded, decodeErr := decodeManagedFormJSONRows(blob, columns)
			if decodeErr != nil {
				return nil, fmt.Errorf("некорректный JSON payload таблицы %q: %w", name, decodeErr)
			}
			rawRows = decoded
		case managedFormTableNamedPayload:
			namedRows, present, namedErr := managedFormNamedTableRows(bodyValues, "vt", name, columns)
			if namedErr != nil {
				return nil, namedErr
			}
			if !present {
				continue
			}
			rawRows = make([]map[string]any, 0, len(namedRows))
			for _, namedRow := range namedRows {
				rawRow := make(map[string]any, len(namedRow))
				for column, value := range namedRow {
					rawRow[column] = value
				}
				rawRows = append(rawRows, rawRow)
			}
		default:
			continue
		}
		result[name] = convertManagedValueTableRows(rawRows, attr)
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func convertManagedValueTableRows(rows []map[string]any, attr *metadata.FormAttribute) []map[string]any {
	cleaned := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		empty := true
		for _, column := range attr.Columns {
			if value, present := row[column.Name]; present && fmt.Sprintf("%v", value) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		converted := make(map[string]any, len(attr.Columns))
		for _, column := range attr.Columns {
			raw := ""
			if value, present := row[column.Name]; present {
				raw = fmt.Sprintf("%v", value)
			}
			switch strings.ToLower(column.TypeRef) {
			case "number":
				if number, err := strconv.ParseFloat(raw, 64); err == nil {
					converted[column.Name] = number
				} else {
					converted[column.Name] = raw
				}
			case "bool":
				converted[column.Name] = raw == "true"
			default:
				converted[column.Name] = raw
			}
		}
		cleaned = append(cleaned, converted)
	}
	return cleaned
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

	procName := chi.URLParam(r, "name")
	if procName == "" {
		respondJSON(enc, formEventResponse{Error: "processor name required"})
		return
	}
	proc := s.reg.GetProcessor(procName)
	if proc == nil {
		respondJSON(enc, formEventResponse{Error: "processor not found: " + procName})
		return
	}
	if !s.can(r, "processor", proc.Name, "run") {
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	}
	// Тот же trust-гейт, что и у processorRun: form-event исполняет DSL
	// обработки (form-обработчики и кнопка «Выполнить»), поэтому недоверенную
	// внешнюю обработку здесь тоже может запускать только администратор —
	// иначе неадмин обходил бы проверку через /form-event.
	if !s.canRunExternalProc(r, proc) {
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	}

	form := proc.ManagedForm()
	if form == nil {
		respondJSON(enc, formEventResponse{Error: "managed form not found for " + procName})
		return
	}

	// Один предел, а не два вложенных: пределы не композируются, и прежний
	// MaxBytesReader на defaultFormMemoryBytes связывал раньше, обрезая
	// файл-параметр обработки мегабайтом (issue #674). Авторизация и trust-гейт
	// выше выполняются до разбора потенциально большого multipart-тела.
	maxSize := s.effectiveUploadLimit()
	requestControls := processorRequestControlsForForm(proc, form)
	if requestControls.formTablesErr != nil {
		respondJSON(enc, formEventResponse{Error: requestControls.formTablesErr.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, processorFormBodyLimit(r, maxSize, requestControls))
	opCtx, finish, ok := s.beginOperation(r, opProcessorRun, proc.Name)
	if !ok {
		w.WriteHeader(http.StatusTooManyRequests)
		respondJSON(enc, formEventResponse{Error: "слишком много одновременно выполняемых обработок, повторите позже"})
		return
	}
	opStatus := "ok"
	defer func() { finish(opStatus, 0, false) }()

	if err := parseBoundedForm(r, 32<<20); err != nil {
		opStatus = "error"
		w.WriteHeader(uploadErrorStatus(err))
		respondJSON(enc, formEventResponse{Error: s.errText(r, formBodyError(err, nil))})
		return
	}
	elementValue, _ := processorPostFormText(r, processorServiceFieldName(proc.Params, "_element"))
	eventValue, _ := processorPostFormText(r, processorServiceFieldName(proc.Params, "_event"))
	elementName := strings.TrimSpace(elementValue)
	eventName := strings.TrimSpace(eventValue)
	if eventName == "" {
		opStatus = "error"
		respondJSON(enc, formEventResponse{Error: "_event required"})
		return
	}

	// Явно привязанный обработчик имеет безусловный приоритет. Если его нет в
	// .form.os, это ошибка конфигурации, а не разрешение незаметно выполнить
	// глобальную процедуру Выполнить.
	boundProcName, eventTarget, executeFallback, eligibilityErr := resolveBrowserFormEvent(form, elementName, eventName, true)
	if eligibilityErr != nil {
		opStatus = "error"
		respondJSON(enc, formEventResponse{Error: eligibilityErr.Error()})
		return
	}
	if err := validateManagedFormTableEventTarget(requestControls.tableAuthorities, eventTarget); err != nil {
		opStatus = "error"
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}
	paramValues, err := processorParamValuesFromRequest(
		r,
		proc.Params,
		maxSize,
		requestControls,
	)
	if err != nil {
		opStatus = "error"
		w.WriteHeader(uploadErrorStatus(err))
		respondJSON(enc, formEventResponse{Error: s.errText(r, err)})
		return
	}
	progAny := form.ProgramAST
	var program *ast.Program
	if p, ok := progAny.(*ast.Program); ok && p != nil {
		program = p
	}
	if boundProcName != "" {
		var decl *ast.ProcedureDecl
		if program != nil {
			for _, p := range program.Procedures {
				if strings.EqualFold(p.Name.Literal, boundProcName) {
					decl = p
					break
				}
			}
		}
		if decl == nil {
			opStatus = "error"
			respondJSON(enc, formEventResponse{Error: "процедура «" + boundProcName + "» не найдена в .form.os"})
			return
		}
		if proc.External {
			s.auditExtProcRun(r, proc.Name)
		}

		virtEntity := processorVirtualEntity(proc)
		obj, err := processorFormObjectFromRequest(r, virtEntity, form, paramValues, requestControls)
		if err != nil {
			opStatus = "error"
			respondJSON(enc, formEventResponse{Error: err.Error()})
			return
		}
		mc := runtime.NewMovementsCollector("processor", uuid.Nil)
		var msgs []string
		// An unclosed explicit DSL transaction must be rolled back when the
		// request ends even when operation timeouts are disabled.
		dslCtx, cancelDSL := context.WithCancel(opCtx)
		defer cancelDSL()
		vars, txState := s.buildDSLVarsWithMessagesTx(dslCtx, mc, &msgs)
		defer rollbackDSLExecution(txState)
		thisObj := s.newFormObjectThisLive(dslCtx, txState, obj, virtEntity, form, false)
		vars["Объект"] = thisObj
		vars["ЭтотОбъект"] = thisObj
		vars["Параметры"] = thisObj
		addFormAttrVars(form, virtEntity, thisObj, vars)
		interpreter.InjectMaket(vars, proc.Layout)

		formProcs := make(map[string]*ast.ProcedureDecl, len(program.Procedures))
		for _, p := range program.Procedures {
			formProcs[strings.ToLower(p.Name.Literal)] = p
		}
		vars["__form_procs__"] = formProcs

		var picker *pickerPayload
		pickerFn := newPickerBuiltin(&picker)
		vars["ПоказатьПодбор"] = pickerFn
		vars["ShowPicker"] = pickerFn
		condRuntime := newFormConditionalRuntime(form)
		for k, v := range condRuntime.builtins() {
			vars[k] = v
		}
		pickResult, _ := processorPostFormText(r, processorServiceFieldName(proc.Params, "_pick_result"))
		if pr := parsePickResult(pickResult); pr != nil {
			vars["ПодборРезультат"] = pr
			vars["PickResult"] = pr
		}
		if err := addProcessorTPEventContext(r, proc, requestControls, eventTarget, obj, vars); err != nil {
			opStatus = "error"
			respondJSON(enc, formEventResponse{Error: err.Error()})
			return
		}

		var runErr error
		if timeout := processorSandboxTimeout(opCtx, s.operationTimeout(opProcessorRun)); timeout > 0 {
			runErr = s.interp.RunSandboxed(decl, thisObj,
				interpreter.SandboxProfile{MaxWallClock: timeout}, nil, vars)
		} else {
			runErr = s.interp.Run(decl, thisObj, vars)
		}
		runErr = finishDSLExecution(txState, runErr)
		if runErr != nil {
			opStatus = operationStatus(opCtx, runErr)
			resp := s.serializeManagedFormEventState(r.Context(), form, virtEntity, obj, condRuntime.rules, msgs).response(false)
			resp.Error = interpreter.FormatUserError(runErr)
			resp.PickerData = picker
			respondJSON(enc, resp)
			return
		}

		resp := s.serializeManagedFormEventState(r.Context(), form, virtEntity, obj, condRuntime.rules, msgs).response(true)
		resp.PickerData = picker
		respondJSON(enc, resp)
		return
	}

	// Без привязанного обработчика общий Выполнить разрешён только настоящей
	// кнопке из метаданных формы. Одного присланного клиентом _event=Нажатие
	// недостаточно.
	if executeFallback {
		procDecl := s.reg.GetProcedure(proc.Name, "Выполнить")
		if procDecl == nil {
			opStatus = "error"
			respondJSON(enc, formEventResponse{Error: "процедура Выполнить() не найдена в обработке «" + proc.Name + "»"})
			return
		}
		if proc.External {
			s.auditExtProcRun(r, proc.Name)
		}

		var msgs []string

		paramsThis := &interpreter.MapThis{M: paramValues}
		mc := runtime.NewMovementsCollector("processor", uuid.Nil)
		dslCtx, cancelDSL := context.WithCancel(opCtx)
		defer cancelDSL()
		dslVars, txState := s.buildDSLVarsWithMessagesTx(dslCtx, mc, &msgs)
		defer rollbackDSLExecution(txState)
		dslVars["Параметры"] = paramsThis
		interpreter.InjectMaket(dslVars, proc.Layout)

		// Кнопка managed-формы должна передавать параметры в объявленные
		// аргументы Выполнить так же, как обычный POST запуска обработки.
		procArgs := interpreter.BindNamedArgs(procDecl, paramValues)
		var err error
		if timeout := processorSandboxTimeout(opCtx, s.operationTimeout(opProcessorRun)); timeout > 0 {
			_, err = s.interp.CallSandboxed(procDecl, paramsThis, procArgs,
				interpreter.SandboxProfile{MaxWallClock: timeout}, dslVars)
		} else {
			_, err = s.interp.Call(procDecl, paramsThis, procArgs, dslVars)
		}
		err = finishDSLExecution(txState, err)
		if err != nil {
			opStatus = operationStatus(opCtx, err)
			respondJSON(enc, formEventResponse{
				OK:       false,
				Messages: msgs,
				Error:    interpreter.FormatUserError(err),
			})
			return
		}
		respondJSON(enc, formEventResponse{
			OK:       true,
			Messages: msgs,
		})
		return
	}

	respondJSON(enc, formEventResponse{Error: "недоступное событие формы"})
}

func formTablesFromRows(rows map[string][]map[string]any, form *metadata.FormModule) map[string][]map[string]any {
	if rows == nil || form == nil {
		return nil
	}
	vtNames := make([]string, 0)
	for _, attr := range form.Attributes {
		if attr != nil && strings.EqualFold(attr.TypeRef, "ValueTable") {
			vtNames = append(vtNames, attr.Name)
		}
	}
	if len(vtNames) == 0 {
		return nil
	}
	result := make(map[string][]map[string]any)
	for _, name := range vtNames {
		for rowName, value := range rows {
			if strings.EqualFold(rowName, name) {
				if value == nil {
					value = []map[string]any{}
				}
				// Наличие ключа важно даже для пустого slice: Очистить() должно
				// приказать клиенту удалить старые строки, а не оставить их.
				result[name] = value
				break
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
