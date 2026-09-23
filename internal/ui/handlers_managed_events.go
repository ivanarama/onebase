package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
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
	// ElementStates — состояние элементов с условиями readonly_when/hidden_when,
	// пересчитанное по полям записи ПОСЛЕ обработчика. Без него запрет,
	// зависящий от состояния объекта, появлялся бы только после перезагрузки
	// страницы: команда «Принять» замораживает реквизиты сразу, а форма
	// продолжала показывать их редактируемыми.
	ElementStates *elementStates `json:"elementStates,omitempty"`
	Messages      []string       `json:"messages,omitempty"`
	Error         string         `json:"error,omitempty"`
	// PickerData != nil — обработчик фазы 1 вызвал ПоказатьПодбор: клиент
	// открывает модальный диалог мультивыбора вместо применения ТЧ (план 46).
	PickerData *pickerPayload `json:"pickerData,omitempty"`
	// SavedID заполняется, когда обработчик записал ЕЩЁ НЕ СОХРАНЁННУЮ форму
	// через Объект.Записать(). Клиент подставляет его в _id следующих событий и
	// в адрес страницы — иначе второе действие подряд создало бы второй документ.
	SavedID string `json:"savedId,omitempty"`
	// SavedLabel accompanies savedId for popup save-and-select. It is derived
	// from the persisted object, never trusted from the browser.
	SavedLabel string `json:"savedLabel,omitempty"`
	// Version — текущая версия записи после обработчика. Клиент кладёт её в
	// скрытое поле _version: обработчик, записавший объект, версию поднял, а
	// форма держала прочитанную при отрисовке — и следующая кнопка «Записать»
	// упиралась в «объект изменён другим пользователем».
	Version int64 `json:"version,omitempty"`
	// Dirty reports whether BeforeClose left object state that is not durable.
	// It is populated for save and discard: even a previously clean form can be
	// mutated by a denied/failed close handler and must then remain protected.
	Dirty *bool `json:"dirty,omitempty"`
	// ChoiceList — динамический список значений для элемента ПолеСписка,
	// сформированный обработчиком НачалоВыбора (билтин ДобавитьЗначениеСписка).
	// Клиент заполняет им <select> того элемента, что инициировал событие.
	ChoiceList []choiceListItem `json:"choiceList,omitempty"`
	// RefOptions — <option> для ссылочных значений из Values: <select> рисуется
	// первой страницей справочника, и присвоенная обработчиком ссылка за её
	// пределами иначе молча обнуляла бы поле (#615, см. eventRefOptions).
	RefOptions map[string][]map[string]any `json:"refOptions,omitempty"`
	// TPRefOptions — подписи ссылок из строк табличных частей. Значения строк
	// остаются UUID, а клиент обновляет этот словарь до перерисовки SlickGrid.
	TPRefOptions map[string]map[string][]map[string]any `json:"tpRefOptions,omitempty"`
	// Close присутствует только в ответе отдельного close-intent endpoint.
	// Обычный /form-event не выдаёт разрешение уничтожить форму.
	Close *formCloseDecision `json:"close,omitempty"`
}

type managedCloseSaveError struct {
	status   int
	kind     string
	message  string
	messages []string
	cause    error
}

func (e *managedCloseSaveError) Error() string { return e.message }
func (e *managedCloseSaveError) Unwrap() error { return e.cause }

const (
	managedSaveForbidden  = "forbidden"
	managedSaveConflict   = "conflict"
	managedSaveValidation = "validation"
	managedSaveHook       = "hook"
)

// saveManagedObject is the canonical result-returning save layer shared by
// HTML submit and close-intent. Callers parse/render their transport, while all
// permission, row-filter, required, optimistic-version, form-hook and
// entityservice semantics live here exactly once.
func (s *Server) saveManagedObject(r *http.Request, entity *metadata.Entity, form *metadata.FormModule, obj *runtime.Object, isNew bool, action string, requireReadableResult bool, onCommitted func(uuid.UUID, int64)) ([]string, int64, error) {
	if !s.can(r, string(entity.Kind), entity.Name, "write") {
		return nil, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён"}
	}
	posting := action == "post" || action == "post_and_close"
	if posting {
		if !entity.Posting {
			return nil, 0, &managedCloseSaveError{status: http.StatusBadRequest, kind: managedSaveValidation, message: "проведение недоступно для этой формы"}
		}
		if !s.can(r, string(entity.Kind), entity.Name, "post") {
			return nil, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён"}
		}
	}
	if !isNew {
		if _, err := s.protectMaskedFieldsOnWrite(r.Context(), entity, obj.ID, obj.Fields); err != nil {
			return nil, 0, err
		}
		dec, err := s.rowDecision(r.Context(), entity, "write")
		if err != nil || !dec.Allowed {
			return nil, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён"}
		}
		if !dec.Unrestricted {
			row, loadErr := s.store.GetByID(r.Context(), entity.Name, obj.ID, entity)
			if loadErr != nil || !s.matchRowPredicate(r.Context(), row, dec.Predicate) ||
				!s.matchRowPredicate(r.Context(), storage.MergeRowFields(row, obj.Fields), dec.Predicate) {
				return nil, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён"}
			}
		}
		if posting {
			postDec, err := s.rowDecision(r.Context(), entity, "post")
			if err != nil || !postDec.Allowed {
				return nil, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён"}
			}
			if !postDec.Unrestricted {
				row, loadErr := s.store.GetByID(r.Context(), entity.Name, obj.ID, entity)
				if loadErr != nil || !s.matchRowPredicate(r.Context(), row, postDec.Predicate) ||
					!s.matchRowPredicate(r.Context(), storage.MergeRowFields(row, obj.Fields), postDec.Predicate) {
					return nil, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён"}
				}
			}
		}
	}
	if err := s.validateManagedFormRequired(r, entity, form, obj.Fields); err != nil {
		return nil, 0, &managedCloseSaveError{status: http.StatusBadRequest, kind: managedSaveValidation, message: err.Error()}
	}

	var hookMessages []string
	var hookErr error
	var expectedVersion *int64
	if !isNew {
		if raw := r.FormValue("_version"); raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				expectedVersion = &parsed
			}
		}
		if hookErr = s.runPreSaveFormHooks(r.Context(), entity, obj, &hookMessages); hookErr != nil {
			return hookMessages, 0, &managedCloseSaveError{status: http.StatusBadRequest, kind: managedSaveHook, message: hookErr.Error(), messages: hookMessages}
		}
	}
	result, err := s.entitySvc.Save(r.Context(), entityservice.SaveRequest{
		Entity: entity, ID: obj.ID, IsNew: isNew, Fields: obj.Fields,
		TablePartRows: obj.TablePartRows, Action: action, ExpectedVersion: expectedVersion,
		Preflight: func(txCtx context.Context, saveObj *runtime.Object) error {
			if !isNew {
				return nil
			}
			if err := s.autoFillRowAccessFields(txCtx, entity, "write", saveObj.Fields); err != nil {
				return errSubmitRowAccessDenied
			}
			if posting {
				if err := s.autoFillRowAccessFields(txCtx, entity, "post", saveObj.Fields); err != nil {
					return errSubmitRowAccessDenied
				}
			}
			if !s.rowAllowedContext(txCtx, entity, "write", saveObj.Fields) ||
				(posting && !s.rowAllowedContext(txCtx, entity, "post", saveObj.Fields)) {
				return errSubmitRowAccessDenied
			}
			if hookErr = s.runPreSaveFormHooks(txCtx, entity, saveObj, &hookMessages); hookErr != nil {
				return errSubmitFormHook
			}
			return nil
		},
		FinalPreflight: func(txCtx context.Context, saveObj *runtime.Object) error {
			if !requireReadableResult {
				return nil
			}
			// Read the authoritative row after every persistence/service-field
			// mutation. Checking saveObj alone would miss RLS predicates on posted
			// and similar fields maintained by storage rather than by the form.
			persisted, loadErr := s.store.GetByID(txCtx, entity.Name, saveObj.ID, entity)
			if loadErr != nil {
				if storage.IsNotFound(loadErr) {
					return errCloseResultUnreadable
				}
				return loadErr
			}
			readable, accessErr := s.rowAllowedContextResult(txCtx, entity, "read", persisted)
			if accessErr != nil {
				return accessErr
			}
			if !s.canCtx(txCtx, string(entity.Kind), entity.Name, "read") || !readable {
				return errCloseResultUnreadable
			}
			return nil
		},
		OnCommitted: func(result entityservice.SaveResult) {
			obj.ID = result.ID
			if onCommitted != nil {
				onCommitted(result.ID, result.Version)
			}
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrVersionConflict):
			return hookMessages, 0, &managedCloseSaveError{status: http.StatusConflict, kind: managedSaveConflict, message: "объект был изменён другим пользователем; форма оставлена открытой", messages: hookMessages}
		case errors.Is(err, errCloseResultUnreadable):
			return hookMessages, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён", messages: hookMessages, cause: errCloseResultUnreadable}
		case errors.Is(err, errSubmitRowAccessDenied):
			return hookMessages, 0, &managedCloseSaveError{status: http.StatusForbidden, kind: managedSaveForbidden, message: "доступ запрещён", messages: hookMessages}
		case errors.Is(err, errSubmitFormHook):
			return hookMessages, 0, &managedCloseSaveError{status: http.StatusBadRequest, kind: managedSaveHook, message: hookErr.Error(), messages: hookMessages}
		default:
			return hookMessages, 0, err
		}
	}
	if result.DSLError != "" {
		return result.DSLMessages, 0, &managedCloseSaveError{status: http.StatusBadRequest, kind: managedSaveHook, message: result.DSLError, messages: result.DSLMessages}
	}
	obj.ID = result.ID
	s.runAfterWriteFormHook(r.Context(), entity, result.ID, &hookMessages)
	return hookMessages, result.Version, nil
}

func (s *Server) saveManagedCloseObject(r *http.Request, entity *metadata.Entity, form *metadata.FormModule, obj *runtime.Object, isNew bool, mode string, inv *formCloseInvocation) ([]string, int64, error) {
	action := ""
	if mode == "post" {
		action = "post"
	}
	if mode == "save_and_select" && r.FormValue("_popup") != "1" {
		return nil, 0, &managedCloseSaveError{status: http.StatusBadRequest, kind: managedSaveValidation, message: "save_and_select разрешён только для popup-формы"}
	}
	return s.saveManagedObject(r, entity, form, obj, isNew, action, true, func(id uuid.UUID, version int64) {
		if inv == nil {
			return
		}
		inv.saved = true
		inv.savedID = id.String()
		inv.version = version
		inv.formURL = "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + id.String()
	})
}

// handleManagedFormEvent — единая точка обработки событий managed-форм.
func (s *Server) handleManagedFormEvent(w http.ResponseWriter, r *http.Request) {
	s.handleManagedFormEventMode(w, r, nil)
}

func (s *Server) installFormCloseAccessRecheck(
	ctx context.Context,
	inv *formCloseInvocation,
	entity *metadata.Entity,
	id func() uuid.UUID,
	shouldCheck func() bool,
	canRead bool,
) {
	if inv == nil || entity == nil || id == nil || shouldCheck == nil {
		return
	}
	checkBase := context.WithoutCancel(ctx)
	inv.recheckAccess = func() {
		checkCtx, cancel := context.WithTimeout(checkBase, 2*time.Second)
		defer cancel()
		if !shouldCheck() {
			return
		}
		rowID := id()
		if rowID == uuid.Nil {
			return
		}
		persisted, err := s.store.GetByID(checkCtx, entity.Name, rowID, entity)
		switch {
		case storage.IsNotFound(err):
			inv.suppressState = true
			inv.terminal = true
		case err != nil:
			inv.suppressState = true
			inv.accessCheckFailed = true
		default:
			readable, accessErr := s.rowAllowedContextResult(checkCtx, entity, "read", persisted)
			if accessErr != nil {
				inv.suppressState = true
				inv.accessCheckFailed = true
			} else if !canRead || !readable {
				inv.suppressState = true
				inv.terminal = true
			}
		}
	}
}

func (s *Server) handleManagedFormEventMode(w http.ResponseWriter, r *http.Request, closeInv *formCloseInvocation) {
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
		if closeInv != nil {
			s.handleMissingEntityFormClose(w, r, closeInv, entityName)
			return
		}
		respondJSON(enc, formEventResponse{Error: "entity not found: " + entityName})
		return
	}
	entityKind := string(entity.Kind)
	canRead := s.can(r, entityKind, entity.Name, "read")
	canWrite := s.can(r, entityKind, entity.Name, "write")
	if closeInv == nil && !canRead && !canWrite {
		w.WriteHeader(http.StatusForbidden)
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	}

	if closeInv != nil {
		var cancel context.CancelFunc
		r, cancel = s.withFormCloseOperationDeadline(r, opFormEvent)
		defer cancel()
		value := func(name string) string { return missingFormCloseValue(r, name) }
		envelopeFormKind := strings.ToLower(value("_kind"))
		if envelopeFormKind == "" {
			envelopeFormKind = "object"
		}
		envelopeRawID := value("_id")
		routeKind := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
		routeKey := "entity|" + routeKind + "|" + strings.ToLower(entity.Name) + "|" + envelopeFormKind + "|" + envelopeRawID
		currentForm := pickManagedForm(entity, envelopeFormKind)
		renderedSchema := value("_close_schema")
		currentSchema := entityFormCloseSchema(entity, currentForm)
		if renderedSchema == "" || renderedSchema != currentSchema {
			recovered, recoverErr := s.recoverExistingFormCloseInvocation(r, closeInv, routeKey, value)
			if recoverErr != nil {
				status := http.StatusBadRequest
				var closeErr *formCloseHTTPError
				if errors.As(recoverErr, &closeErr) {
					status = closeErr.status
				}
				w.WriteHeader(status)
				respondJSON(enc, formEventResponse{
					Error: recoverErr.Error(), Dirty: boolPtr(true),
					Close: &formCloseDecision{IntentID: closeInv.intentID, Reconcile: errors.Is(recoverErr, errFormCloseReplayExpired)},
				})
				return
			}
			if recovered {
				if parsedID, parseErr := uuid.Parse(envelopeRawID); parseErr == nil {
					s.installFormCloseAccessRecheck(r.Context(), closeInv, entity,
						func() uuid.UUID { return parsedID }, func() bool { return true }, canRead)
				} else if closeInv.replay != nil {
					var replayResponse formEventResponse
					if json.Unmarshal(closeInv.replay.body, &replayResponse) == nil {
						if savedID, parseErr := uuid.Parse(replayResponse.SavedID); parseErr == nil {
							s.installFormCloseAccessRecheck(r.Context(), closeInv, entity,
								func() uuid.UUID { return savedID }, func() bool { return true }, canRead)
						}
					}
				}
				return
			}
			if err := s.prepareMissingFormCloseInvocation(r, closeInv, routeKey, value); err != nil {
				status := http.StatusBadRequest
				var closeErr *formCloseHTTPError
				if errors.As(err, &closeErr) {
					status = closeErr.status
				}
				w.WriteHeader(status)
				respondJSON(enc, formEventResponse{
					Error: err.Error(), Dirty: boolPtr(true),
					Close: &formCloseDecision{IntentID: closeInv.intentID, Reconcile: errors.Is(err, errFormCloseReplayExpired)},
				})
				return
			}
			if closeInv.mode == "discard" {
				closeInv.suppressState = true
				closeInv.terminal = true
				respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
			} else {
				closeInv.reconcile = true
				w.WriteHeader(http.StatusConflict)
				respondJSON(enc, formEventResponse{
					Error: "форма была изменена на сервере; проверьте данные и перезагрузите форму перед сохранением",
					Dirty: boolPtr(true),
				})
			}
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.entityFormBodyLimit(r, entity))
	var rawID, formKind string
	var form *metadata.FormModule
	parseFormState := func() error {
		if err := parseBoundedForm(r, 32<<20); err != nil {
			return err
		}
		// Every value describing browser form state comes from the POST body.
		// Request.Form and FormValue normally merge the query string; using one
		// normalized request prevents query-only ids, targets, fields and table rows
		// from reaching any of the existing form parsers below.
		r = postFormOnlyRequest(r)
		rawID = strings.TrimSpace(r.FormValue("_id"))
		formKind = strings.ToLower(strings.TrimSpace(r.FormValue("_kind")))
		if formKind == "" {
			formKind = "object"
		}
		form = pickManagedForm(entity, formKind)
		return nil
	}
	if closeInv != nil {
		// Replay/single-flight lookup intentionally precedes the shared processor
		// semaphore. Otherwise an exact duplicate gets 429 while its original owns
		// the sole slot instead of waiting for and replaying that result.
		if err := parseFormState(); err != nil {
			w.WriteHeader(uploadErrorStatus(err))
			respondJSON(enc, formEventResponse{Error: s.errText(r, formBodyError(err, entity))})
			return
		}
		envelopeFormKind := strings.ToLower(missingFormCloseValue(r, "_kind"))
		if envelopeFormKind == "" {
			envelopeFormKind = "object"
		}
		envelopeRawID := missingFormCloseValue(r, "_id")
		if formKind != envelopeFormKind || rawID != envelopeRawID {
			w.WriteHeader(http.StatusBadRequest)
			respondJSON(enc, formEventResponse{
				Error: "состояние формы не совпало со служебным конвертом закрытия", Dirty: boolPtr(true),
				Close: &formCloseDecision{IntentID: closeInv.intentID},
			})
			return
		}
		routeKind := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
		routeKey := "entity|" + routeKind + "|" + strings.ToLower(entity.Name) + "|" + envelopeFormKind + "|" + envelopeRawID
		if err := s.prepareFormCloseInvocation(r, closeInv, routeKey,
			func(name string) string { return missingFormCloseValue(r, name) }); err != nil {
			var closeErr *formCloseHTTPError
			reconcile := false
			if errors.As(err, &closeErr) {
				w.WriteHeader(closeErr.status)
			}
			reconcile = errors.Is(err, errFormCloseReplayExpired)
			respondJSON(enc, formEventResponse{
				Error: err.Error(), Dirty: boolPtr(true),
				Close: &formCloseDecision{IntentID: closeInv.intentID, Reconcile: reconcile},
			})
			return
		}
		if form == nil {
			// The already-open form disappeared during a hot reload. There is no
			// trusted lifecycle code or durable schema left to execute, but the
			// shell still needs a correlated decision to destroy the stale UI.
			closeInv.suppressState = true
			closeInv.terminal = true
			if closeInv.replay == nil {
				respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
			}
			return
		}
		if parsedID, parseErr := uuid.Parse(rawID); parseErr == nil {
			s.installFormCloseAccessRecheck(r.Context(), closeInv, entity,
				func() uuid.UUID { return parsedID }, func() bool { return true }, canRead)
		}
		if closeInv.replay != nil && rawID == "" {
			var replayResponse formEventResponse
			hasSavedIdentity := false
			if json.Unmarshal(closeInv.replay.body, &replayResponse) == nil {
				if savedID, parseErr := uuid.Parse(replayResponse.SavedID); parseErr == nil {
					hasSavedIdentity = true
					s.installFormCloseAccessRecheck(r.Context(), closeInv, entity,
						func() uuid.UUID { return savedID }, func() bool { return true }, canRead)
				}
			}
			if !hasSavedIdentity && !canWrite {
				// The cached result belongs to a still-unsaved form. If write access
				// disappeared, replay must have the same terminal semantics as a fresh
				// discard and must not disclose old handler deltas/messages.
				closeInv.suppressState = true
				closeInv.terminal = true
			}
		}
		if closeInv.replay != nil {
			return
		}
	}

	// Предел конкурентности и дедлайн — как у обработок (#735). Для close-intent
	// он расположен после replay lookup; обычные события по-прежнему проверяют
	// слот до разбора тела.
	opCtx, finish, ok := s.beginOperation(r, opFormEvent, entity.Name)
	if !ok {
		w.WriteHeader(http.StatusTooManyRequests)
		respondJSON(enc, formEventResponse{Error: "слишком много одновременно выполняемых обработчиков формы, повторите позже"})
		return
	}
	// Fail closed for metrics: every return after beginOperation is an error
	// unless the branch explicitly produced a successful form-event response.
	// This keeps validation/configuration failures from being reported as ok.
	opStatus := "error"
	defer func() {
		if opStatus == "error" && opCtx.Err() != nil {
			opStatus = operationStatus(opCtx, opCtx.Err())
		}
		finish(opStatus, 0, false)
	}()
	dslCtx, cancelDSL := context.WithCancel(opCtx)
	defer cancelDSL()
	finishTerminalClose := func() {
		if closeInv == nil {
			return
		}
		closeInv.suppressState = true
		closeInv.terminal = true
		opStatus = "ok"
		respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
	}

	if closeInv == nil {
		if err := parseFormState(); err != nil {
			w.WriteHeader(uploadErrorStatus(err))
			respondJSON(enc, formEventResponse{Error: s.errText(r, formBodyError(err, entity))})
			return
		}
		if form == nil {
			respondJSON(enc, formEventResponse{Error: "managed form not found for " + entityName})
			return
		}
	}
	if closeInv != nil && closeInv.mode != "discard" &&
		!strings.EqualFold(form.Kind, "object") && (form.Kind != "" || formKind != "object") {
		w.WriteHeader(http.StatusBadRequest)
		respondJSON(enc, formEventResponse{Error: "сохранение при закрытии разрешено только для формы объекта"})
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
			if closeInv != nil && closeInv.mode == "discard" {
				// There is no durable identity to protect and no authorized write to
				// perform. Let the owner destroy its unsaved client-only form without
				// invoking trusted lifecycle code under revoked permissions.
				finishTerminalClose()
				return
			}
			w.WriteHeader(http.StatusForbidden)
			respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
			return
		}
	} else if !canRead {
		if closeInv != nil && rawID != "" {
			// The browser already owns a form for this id, but no longer has even
			// coarse read permission. Close it without revealing whether the row
			// still exists or returning an identity usable by another request.
			finishTerminalClose()
			return
		}
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
		_, exists, err := s.store.EntityVersionExists(dslCtx, entity.Name, id)
		if err != nil {
			opStatus = operationStatus(opCtx, err)
			w.WriteHeader(http.StatusInternalServerError)
			respondJSON(enc, formEventResponse{Error: s.errText(r, err)})
			return
		}
		if !exists {
			if closeInv != nil {
				finishTerminalClose()
				return
			}
			w.WriteHeader(http.StatusNotFound)
			respondJSON(enc, formEventResponse{Error: "запись не найдена"})
			return
		}
		rowReadable, rowReadErr := s.rowAllowsIDResult(dslCtx, entity, "read", id)
		if rowReadErr != nil {
			opStatus = operationStatus(opCtx, rowReadErr)
			w.WriteHeader(http.StatusInternalServerError)
			respondJSON(enc, formEventResponse{Error: "не удалось проверить доступ к записи"})
			return
		}
		if !rowReadable {
			if err := dslCtx.Err(); err != nil {
				opStatus = operationStatus(opCtx, err)
			}
			if closeInv != nil && dslCtx.Err() == nil {
				finishTerminalClose()
				return
			}
			w.WriteHeader(http.StatusForbidden)
			respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
			return
		}
	}
	elementName := strings.TrimSpace(r.FormValue("_element"))
	eventName := strings.TrimSpace(r.FormValue("_event"))
	if closeInv != nil {
		elementName = ""
		eventName = string(metadata.FormEventBeforeClose)
	}
	progAny := form.ProgramAST
	if progAny == nil {
		if closeInv != nil {
			if procName := resolveFormCloseHandler(form); procName != "" {
				respondJSON(enc, formEventResponse{Error: "процедура «" + procName + "» не найдена в .form.os"})
				return
			}
			// A save-mode close still has work even without ПередЗакрытием.
			// Continue through snapshot parsing and the canonical save pipeline.
			if closeInv.mode == "discard" {
				opStatus = "ok"
				respondJSON(enc, formEventResponse{OK: true})
				return
			}
		} else {
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
			opStatus = "ok"
			respondJSON(enc, formEventResponse{OK: true})
			return
		}
	}
	if eventName == "" {
		respondJSON(enc, formEventResponse{Error: "_event required"})
		return
	}

	// Найти имя процедуры, которая привязана к событию. Lifecycle-hook
	// ПередЗакрытием разрешён только отдельному close-intent endpoint и никогда
	// не проходит через browser event resolver.
	var procName string
	var eventTarget browserFormEventTarget
	if closeInv != nil {
		procName = resolveFormCloseHandler(form)
		if procName == "" && closeInv.mode == "discard" {
			opStatus = "ok"
			respondJSON(enc, formEventResponse{OK: true})
			return
		}
	} else {
		var eligibilityErr error
		procName, eventTarget, _, eligibilityErr = resolveBrowserFormEvent(form, elementName, eventName, false)
		if eligibilityErr != nil {
			respondJSON(enc, formEventResponse{Error: eligibilityErr.Error()})
			return
		}
	}
	if err := validateManagedFormTableEventTarget(tableAuthorities, eventTarget); err != nil {
		respondJSON(enc, formEventResponse{Error: err.Error()})
		return
	}
	var program *ast.Program
	if progAny != nil {
		var ok bool
		program, ok = progAny.(*ast.Program)
		if !ok || program == nil {
			respondJSON(enc, formEventResponse{Error: "form AST type mismatch"})
			return
		}
	}

	// Найти AST процедуры.
	var decl *ast.ProcedureDecl
	if procName != "" {
		for _, p := range program.Procedures {
			if strings.EqualFold(p.Name.Literal, procName) {
				decl = p
				break
			}
		}
	}
	if procName != "" && decl == nil {
		if closeInv != nil {
			respondJSON(enc, formEventResponse{Error: "процедура «" + procName + "» не найдена в .form.os"})
			return
		}
		opStatus = "ok"
		respondJSON(enc, formEventResponse{OK: true, Messages: []string{
			"⚠ Процедура «" + procName + "» не найдена в .form.os",
		}})
		return
	}
	var closeArgs []any
	if closeInv != nil && decl != nil {
		closeArgs, err = validateFormCloseProcedure(decl)
		if err != nil {
			respondJSON(enc, formEventResponse{Error: err.Error()})
			return
		}
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
	var submittedVersion *int64
	if rawVersion := strings.TrimSpace(r.FormValue("_version")); rawVersion != "" {
		if parsedVersion, parseErr := strconv.ParseInt(rawVersion, 10, 64); parseErr == nil {
			submittedVersion = &parsedVersion
		}
	}
	if closeInv != nil {
		// Observe the entire close lifecycle, not only BeforeClose. In
		// particular ПослеЗаписи may obtain another wrapper through a common
		// module and perform another durable write before the canonical reload.
		observeSave := func(savedEntity *metadata.Entity, result entityservice.SaveResult) {
			if savedEntity == nil || !strings.EqualFold(savedEntity.Name, entity.Name) || result.ID != obj.ID {
				return
			}
			closeInv.saved = true
			closeInv.savedID = result.ID.String()
			closeInv.version = result.Version
			closeInv.formURL = "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + result.ID.String()
		}
		r = r.WithContext(entityservice.ContextWithSaveObserver(r.Context(), observeSave))
		dslCtx = entityservice.ContextWithSaveObserver(dslCtx, observeSave)
		s.installFormCloseAccessRecheck(r.Context(), closeInv, entity,
			func() uuid.UUID { return obj.ID },
			func() bool { return existingFormID != "" || closeInv.saved }, canRead)
	}
	copyStateRestored := false
	if existingFormID != "" {
		if restoreErr := s.restoreUnsubmittedFields(dslCtx, r, entity, form, obj.ID, obj.Fields); restoreErr != nil && closeInv != nil {
			// A close lifecycle must never continue with absent readonly/unplaced
			// fields: a save could persist nulls and discard could authorize close
			// or run side effects against an incomplete object.
			w.WriteHeader(http.StatusInternalServerError)
			respondJSON(enc, formEventResponse{Error: s.errText(r, restoreErr)})
			return
		}
		if closeInv != nil {
			persisted, loadErr := s.store.GetByID(dslCtx, entity.Name, obj.ID, entity)
			if loadErr != nil {
				if storage.IsNotFound(loadErr) {
					finishTerminalClose()
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
				respondJSON(enc, formEventResponse{Error: s.errText(r, loadErr)})
				return
			}
			rowReadable, rowReadErr := s.rowAllowedContextResult(dslCtx, entity, "read", persisted)
			if rowReadErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				respondJSON(enc, formEventResponse{Error: "не удалось проверить доступ к записи"})
				return
			}
			if !rowReadable {
				finishTerminalClose()
				return
			}
			// Service fields are not regular form fields and therefore are not
			// restored by restoreUnsubmittedFields. BeforeClose must nevertheless
			// observe the canonical posting/deletion/hierarchy state on discard.
			mergePersistedEntityServiceFieldsForClose(entity, persisted, obj.Fields, closeInv.mode != "discard")
			// The object exposed to a discard lifecycle represents the browser
			// snapshot, including its optimistic version. Replacing this with the
			// latest DB version would let Object.Write persist stale editable fields
			// over a concurrent update.
			if submittedVersion != nil {
				obj.Fields["_version"] = *submittedVersion
			}
			// Discard still runs trusted BeforeClose code, which may call
			// Object.Write. Restore fields hidden/masked from this user before the
			// handler so forged browser values cannot ride that authorized write.
			if _, protectErr := s.protectMaskedFieldsOnWrite(dslCtx, entity, obj.ID, obj.Fields); protectErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				respondJSON(enc, formEventResponse{Error: s.errText(r, protectErr)})
				return
			}
		}
	} else if strings.TrimSpace(r.FormValue(copySourceFormField)) != "" {
		if err := s.restoreManagedCopyStateResult(r, entity, form, obj.Fields, obj.Fields, obj.TablePartRows); err != nil {
			status := http.StatusInternalServerError
			if loadErr, ok := err.(*copySourceLoadError); ok {
				status = loadErr.status
			}
			w.WriteHeader(status)
			respondJSON(enc, formEventResponse{Error: err.Error()})
			return
		}
		copyStateRestored = true
	}
	persistedID := uuid.Nil
	if existingFormID != "" {
		persistedID = obj.ID
	}
	// A copy source already supplied the authorized canonical snapshot for
	// readonly/unplaced table parts. Running the ordinary new-record restore
	// with persistedID=nil would clear those copied rows. This mirrors the
	// mutually exclusive copy/non-copy preparation in HTML submit.
	if !copyStateRestored {
		if err := s.restoreUneditableTableParts(dslCtx, r, entity, form, persistedID, obj.TablePartRows, canWrite); err != nil {
			opStatus = operationStatus(opCtx, err)
			respondJSON(enc, formEventResponse{Error: s.errText(r, err)})
			return
		}
	}

	// Псевдо-реквизит «Ссылка» самой записи — как в entityservice.Save. Без него
	// Объект.Ссылка в обработчике формы было Неопределено, и типовой вызов
	// Модуль.Действие(Объект.Ссылка) падал на «ПолучитьОбъект вызван у
	// Неопределено». Ставится ДО newFormObjectThis: attachObject предзагружает
	// реквизиты именно по этой ссылке.
	setFormSelfRef(r, entity, obj)

	// Реквизиты формы (attributes с save:false) — formToFields их не разбирает,
	// поэтому без этого шага Объект.<Реквизит> в обработчике всегда nil.
	s.mergeFormAttrValues(dslCtx, r, form, entity, obj)

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
	if closeInv != nil {
		baseline := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, nil).response(false)
		closeInv.baselineValues = baseline.Values
		closeInv.baselineTableParts = baseline.TableParts
		closeInv.baselineFormTables = baseline.FormTables
	}
	var msgs []string
	if closeInv != nil && closeInv.mode != "discard" {
		saveMessages, savedVersion, saveErr := s.saveManagedCloseObject(r, entity, form, obj, existingFormID == "", closeInv.mode, closeInv)
		msgs = append(msgs, saveMessages...)
		if saveErr != nil {
			status := http.StatusInternalServerError
			message := s.errText(r, saveErr)
			var closeSaveErr *managedCloseSaveError
			if errors.As(saveErr, &closeSaveErr) {
				status = closeSaveErr.status
				message = closeSaveErr.message
			}
			w.WriteHeader(status)
			resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(false)
			resp.Error = message
			// Hooks mutate the live object map even when the save transaction rolls
			// back. If that final candidate is unreadable, neither its fields nor its
			// messages may be reflected to a write-only/RLS-restricted caller.
			closeInv.suppressState = errors.Is(saveErr, errCloseResultUnreadable) || !canRead ||
				!s.rowAllowedContext(r.Context(), entity, "read", obj.Fields)
			// A conflict can be caused by a concurrent change that also removed
			// this caller's read access (or deleted the row). Without a durable
			// recheck the open form would be trapped: every retry is rejected by
			// the initial read gate. Return an identity-free terminal close instead.
			if existingFormID != "" && obj.ID != uuid.Nil {
				persisted, durableErr := s.store.GetByID(r.Context(), entity.Name, obj.ID, entity)
				switch {
				case storage.IsNotFound(durableErr):
					closeInv.suppressState = true
					closeInv.terminal = true
				case durableErr != nil:
					// Unknown durable state is not proof that closing is safe, but it
					// also is not authority to serialize a hook-mutated candidate.
					closeInv.suppressState = true
				default:
					readable, accessErr := s.rowAllowedContextResult(r.Context(), entity, "read", persisted)
					if accessErr != nil {
						closeInv.suppressState = true
					} else if !canRead || !readable {
						closeInv.suppressState = true
						closeInv.terminal = true
					}
				}
			}
			// The failed save can leave caller-visible state changed by
			// BeforeWrite/validation hooks. Keep the still-open form fail-safe:
			// a later Close must offer Save/Discard instead of dropping it.
			resp.Dirty = boolPtr(true)
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
		// The write is already committed. Record the durable identity before the
		// canonical reload so a rare reload failure cannot misreport it as an
		// unsaved attempt (the form still remains open fail-closed).
		closeInv.saved = true
		closeInv.savedID = obj.ID.String()
		closeInv.formURL = "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + obj.ID.String()
		// Capture the committed optimistic version before any fallible reload.
		// Even a fail-closed response must let the still-open form update this
		// exact record instead of retrying an unversioned write.
		if closeInv.version < savedVersion {
			closeInv.version = savedVersion
		}
		canonicalVersion := closeInv.version
		// ПередЗакрытием must observe the canonical persisted object, including
		// assigned number/id/version. Preserve form-only attributes and ValueTable
		// rows while replacing persisted entity state from the database.
		persisted, loadErr := s.store.GetByID(r.Context(), entity.Name, obj.ID, entity)
		if loadErr != nil {
			if storage.IsNotFound(loadErr) {
				closeInv.suppressState = true
				closeInv.terminal = true
			}
			w.WriteHeader(http.StatusInternalServerError)
			resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(false)
			resp.Error = s.errText(r, loadErr)
			resp.Dirty = boolPtr(false)
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
		rowReadable, rowReadErr := s.rowAllowedContextResult(r.Context(), entity, "read", persisted)
		if rowReadErr != nil {
			closeInv.suppressState = true
			w.WriteHeader(http.StatusInternalServerError)
			resp := formEventResponse{Error: "не удалось проверить доступ к записанной форме", Dirty: boolPtr(false)}
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
		if !canRead || !rowReadable {
			closeInv.suppressState = true
			closeInv.terminal = true
		}
		if persistedEntityVersion(persisted) != canonicalVersion {
			w.WriteHeader(http.StatusConflict)
			resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(false)
			resp.Error = "объект изменён другим пользователем после записи"
			resp.Dirty = boolPtr(false)
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
		canonicalFields := cloneRecord(obj.Fields)
		canonicalTableParts := cloneTablePartRowMap(obj.TablePartRows)
		for _, field := range entity.Fields {
			for key := range canonicalFields {
				if strings.EqualFold(key, field.Name) {
					delete(canonicalFields, key)
				}
			}
			if value, ok := maskCIKeyValue(persisted, field.Name); ok {
				canonicalFields[field.Name] = value
			}
		}
		mergePersistedEntityServiceFields(entity, persisted, canonicalFields)
		if version := persistedEntityVersion(persisted); version > 0 {
			canonicalFields["_version"] = version
		}
		for _, tp := range entity.TableParts {
			rows, rowsErr := s.store.GetTablePartRows(r.Context(), entity.Name, tp.Name, obj.ID, tp)
			if rowsErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(false)
				resp.Error = s.errText(r, rowsErr)
				resp.Dirty = boolPtr(false)
				compactFormCloseDelta(&resp, closeInv)
				respondJSON(enc, resp)
				return
			}
			canonicalTableParts[tp.Name] = rows
		}
		verifiedVersion, exists, versionErr := s.store.EntityVersionExists(r.Context(), entity.Name, obj.ID)
		if versionErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(false)
			resp.Error = s.errText(r, versionErr)
			resp.Dirty = boolPtr(false)
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
		if !exists || verifiedVersion != canonicalVersion {
			w.WriteHeader(http.StatusConflict)
			resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(false)
			resp.Error = "объект изменён другим пользователем после записи"
			resp.Dirty = boolPtr(false)
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
		obj.Fields = canonicalFields
		obj.TablePartRows = canonicalTableParts
		// A newly saved form now has a real reference. BeforeClose must be
		// able to pass it to a common module just like an originally existing
		// form; the pseudo-field is filtered from the browser response.
		setPersistedFormSelfRef(entity, obj)
		closeInv.savedLabel = s.maskedRecordLabel(r.Context(), entity, persisted)
		if decl == nil {
			opStatus = "ok"
			resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, nil, msgs).response(true)
			resp.Dirty = boolPtr(false)
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}
	}
	// Подмешать ссылки → *runtime.Ref, как при сохранении (нужно для
	// Объект.Покупатель.Наименование и проч.).
	s.enrichHeaderRefs(dslCtx, entity, obj)
	for _, tp := range entity.TableParts {
		if rows, ok := obj.TablePartRows[tp.Name]; ok {
			s.enrichTPRowsWithRefs(dslCtx, tp, rows)
		}
	}

	// Сборка vars c builtin Сообщить, копящим сообщения. Дополнительно
	// прокидываем «Объект» и «ЭтотОбъект» как formObjectThis — обёртку,
	// которая возвращает *formTpProxy для табличных частей (чтобы
	// `Объект.Товары.Добавить()` реально модифицировал obj).
	mc := runtime.NewMovementsCollector(entity.Name, obj.ID)
	// txState — «живой» контекст: обработчик может позвать модуль, который
	// откроет транзакцию, и ссылки объекта обязаны выполнять ПолучитьОбъект()
	// внутри неё, а не ждать второго соединения (пул SQLite — одно).
	vars, txState := s.buildDSLVarsWithMessagesTx(dslCtx, mc, &msgs)
	defer rollbackDSLExecution(txState)
	isNewForHandler := strings.TrimSpace(r.FormValue("_id")) == "" && (closeInv == nil || !closeInv.saved)
	thisObj := s.newFormObjectThisLive(dslCtx, txState, obj, entity, form, isNewForHandler)
	if closeInv != nil {
		thisObj.finalPreflight = func(txCtx context.Context, saveObj *runtime.Object) error {
			persisted, loadErr := s.store.GetByID(txCtx, entity.Name, saveObj.ID, entity)
			if loadErr != nil {
				if storage.IsNotFound(loadErr) {
					closeInv.suppressState = true
					return errCloseResultUnreadable
				}
				return loadErr
			}
			readable, accessErr := s.rowAllowedContextResult(txCtx, entity, "read", persisted)
			if accessErr != nil {
				return accessErr
			}
			if !s.canCtx(txCtx, string(entity.Kind), entity.Name, "read") || !readable {
				closeInv.suppressState = true
				return errCloseResultUnreadable
			}
			return nil
		}
		thisObj.onCommitted = func(result entityservice.SaveResult) {
			closeInv.saved = true
			closeInv.savedID = result.ID.String()
			if result.Version > closeInv.version {
				closeInv.version = result.Version
			}
			closeInv.formURL = "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + result.ID.String()
		}
	}
	if closeInv != nil && closeInv.saved && closeInv.version > 0 {
		version := closeInv.version
		thisObj.expectedVersion = &version
	} else if submittedVersion != nil {
		version := *submittedVersion
		thisObj.expectedVersion = &version
	}
	vars["Объект"] = thisObj
	vars["ЭтотОбъект"] = thisObj

	// Реквизиты формы видны и голым именем — как в модуле управляемой формы 1С,
	// где под Объект лежит только основной реквизит. Читаются через thisObj,
	// поэтому ссылочные приходят как *Ref, а ValueTable — как прокси таблицы.
	addFormAttrVars(form, entity, thisObj, vars)

	// Передаём все процедуры формы, чтобы обработчик мог вызывать
	// вспомогательные функции из того же .form.os (evalCall ищет
	// их по ключу __form_procs__).
	formProcs := make(map[string]*ast.ProcedureDecl)
	if program != nil {
		for _, p := range program.Procedures {
			formProcs[strings.ToLower(p.Name.Literal)] = p
		}
	}
	vars["__form_procs__"] = formProcs
	if closeInv != nil {
		vars["ПричинаЗакрытия"] = closeInv.reason
		vars["CloseReason"] = closeInv.reason
	}

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
	existingRecord := strings.TrimSpace(r.FormValue("_id")) != "" || (closeInv != nil && closeInv.saved)
	tpBefore := tablePartRowsSnapshot(obj.TablePartRows)
	var tpDBBefore map[string][]map[string]any
	if existingRecord && len(entity.TableParts) > 0 && obj.ID != uuid.Nil {
		tpDBBefore = s.tablePartRowsFromDB(dslCtx, entity, obj.ID)
	}

	// Выполнение процедуры. Ошибка DSL отдаётся в JSON, не как 500 —
	// клиент покажет красный баннер и не закроет форму.
	var runErr error
	timeout := interpreter.ClampWallClock(opCtx, s.operationTimeout(opFormEvent))
	if closeInv != nil {
		var entryResult interpreter.EntryCallResult
		entryResult, runErr = s.interp.CallEntrySandboxedWithBindings(decl, thisObj, closeArgs,
			interpreter.SandboxProfile{Context: dslCtx, MaxWallClock: timeout}, vars)
		if runErr == nil {
			closeInv.cancelled = formCloseBindingCancelled(decl, entryResult.Bindings)
		}
	} else if timeout > 0 {
		runErr = s.interp.RunSandboxed(decl, thisObj,
			interpreter.SandboxProfile{Context: dslCtx, MaxWallClock: timeout}, nil, vars)
	} else {
		runErr = s.interp.Run(decl, thisObj, vars)
	}
	// Незавершённая DSL-транзакция отменяется ДО перечитывания БД и сериализации:
	// иначе pgx удерживает соединение после запроса, а SQLite ждёт занятое
	// единственное соединение. Успешный выход с открытой транзакцией считается
	// ошибкой процедуры, чтобы конфигурационная ошибка не оставалась незаметной.
	runErr = finishDSLExecution(txState, runErr)
	liveCtx := txState.Ctx()
	if closeInv != nil && thisObj.saved {
		// BeforeClose itself may save in discard mode. Promote that durable
		// outcome for both new and existing forms so the client adopts the
		// final version and never retries an existing row as a new object.
		closeInv.saved = true
		closeInv.savedID = obj.ID.String()
		closeInv.formURL = "/ui/" + strings.ToLower(string(entity.Kind)) + "/" + entity.Name + "/" + obj.ID.String()
		if thisObj.expectedVersion != nil && *thisObj.expectedVersion > closeInv.version {
			closeInv.version = *thisObj.expectedVersion
		}
	}
	// Перечитывать из базы имеет смысл только для записи, которая там есть:
	// либо форма открыта по _id, либо обработчик записал новую (тогда нужен и он —
	// номер от нумератора обязан приехать на экран «Создать» сразу). Гейт по
	// сырому _id, а не по obj.ID: buildObjectFromForm для новой записи генерирует
	// случайный uuid, поэтому obj.ID != uuid.Nil истинно ВСЕГДА и каждое событие
	// на «Создать» уходило бы в базу за несуществующей строкой. Ровно эта ловушка
	// описана выше у restoreUnsubmittedFields.
	if (existingRecord || savedFormID(thisObj) != "") && (closeInv == nil || !closeInv.saved) {
		s.refreshFieldsWrittenByHandler(liveCtx, r, entity, form, obj, fieldsBefore)
	}
	if existingRecord && tpDBBefore != nil && !thisObj.saved {
		s.refreshTablePartsWrittenByHandler(liveCtx, entity, obj, tpBefore, tpDBBefore)
	}
	var closePersisted map[string]any
	var closePersistedErr error
	if closeInv != nil && obj.ID != uuid.Nil && (existingFormID != "" || closeInv.saved) {
		closePersisted, closePersistedErr = s.store.GetByID(liveCtx, entity.Name, obj.ID, entity)
		switch {
		case storage.IsNotFound(closePersistedErr):
			// A trusted handler may delete or replace the row through another
			// wrapper. The old form has no valid next request and must terminate.
			closeInv.suppressState = true
			closeInv.terminal = true
		case closePersistedErr != nil:
			// Unknown durable state is not authority to close or serialize a
			// potentially stale row. Preserve the browser DOM, redact the response,
			// and let a later request retry the read.
			closeInv.suppressState = true
			if runErr == nil {
				runErr = fmt.Errorf("не удалось проверить итоговое состояние формы: %w", closePersistedErr)
			}
		default:
			readable, accessErr := s.rowAllowedContextResult(liveCtx, entity, "read", closePersisted)
			if accessErr != nil {
				closeInv.suppressState = true
				if runErr == nil {
					runErr = fmt.Errorf("не удалось проверить доступ к итоговому состоянию формы: %w", accessErr)
				}
			} else if !canRead || !readable {
				closeInv.suppressState = true
				closeInv.terminal = true
			}
		}
	}
	if closeInv != nil && closeInv.saved {
		// BeforeClose may persist a new display value after the initial save.
		// Popup save-and-select must publish the final canonical, field-masked
		// label—not the snapshot captured before the handler ran.
		if finalPersisted, finalErr := closePersisted, closePersistedErr; finalErr == nil {
			// Only state carrying the exact token written by this lifecycle may be
			// adopted. A newer row belongs to a concurrent writer; publishing its
			// token with our stale/unsaved form state would launder the conflict.
			if persistedEntityVersion(finalPersisted) == closeInv.version {
				previousFields := cloneRecord(obj.Fields)
				previousTableParts := cloneTablePartRowMap(obj.TablePartRows)
				obj.Fields["_version"] = closeInv.version
				mergeBaselineFields := fieldsBefore
				if thisObj.saved && thisObj.lastSavedFields != nil {
					mergeBaselineFields = thisObj.lastSavedFields
				}
				mergePersistedFieldsUnchanged(entity, finalPersisted, obj.Fields, mergeBaselineFields)
				mergeBaselineTableParts := tpBefore
				if thisObj.saved && thisObj.lastSavedTableParts != nil {
					mergeBaselineTableParts = thisObj.lastSavedTableParts
				}
				s.mergePersistedTablePartsUnchanged(liveCtx, entity, obj, mergeBaselineTableParts)
				stableVersion, stableExists, stableErr := s.store.EntityVersionExists(liveCtx, entity.Name, obj.ID)
				if stableErr == nil && stableExists && stableVersion == closeInv.version {
					closeInv.savedLabel = s.maskedRecordLabel(liveCtx, entity, cloneRecord(finalPersisted))
				} else {
					obj.Fields = previousFields
					obj.TablePartRows = previousTableParts
				}
			}
		} else if runErr == nil {
			runErr = fmt.Errorf("не удалось прочитать итоговое состояние записанной формы: %w", finalErr)
		}
	}
	// Publish authoritative dirty state for ordinary commands as well as close
	// lifecycle calls. Programmatic values/TableParts/FormTables do not emit DOM
	// input events; without this signal an unsaved command mutation on a clean
	// form could be silently discarded by the next Close.
	dirty := transientManagedStateDirty(obj, fieldsBefore, tpBefore)
	if existingRecord || thisObj.saved || (closeInv != nil && closeInv.saved) {
		dirty = s.managedCloseStateDirty(liveCtx, entity, form, obj, fieldsBefore, tpBefore)
	}
	eventDirty := boolPtr(dirty)
	if runErr != nil {
		opStatus = operationStatus(opCtx, runErr)
		resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, condRuntime.rules, msgs).response(false)
		resp.Error = interpreter.FormatUserError(runErr)
		resp.PickerData = picker
		// Обработчик мог записать форму и упасть уже после этого: id всё равно
		// нужен клиенту, иначе повтор действия создаст второй документ.
		resp.SavedID = savedFormID(thisObj)
		resp.Version = versionWrittenByHandler(thisObj)
		resp.Dirty = eventDirty
		compactFormCloseDelta(&resp, closeInv)
		respondJSON(enc, resp)
		return
	}

	opStatus = "ok"
	resp := s.serializeManagedFormEventState(r.Context(), form, entity, obj, condRuntime.rules, msgs).response(true)
	resp.PickerData = picker
	resp.ChoiceList = choiceItems
	resp.SavedID = savedFormID(thisObj)
	resp.Version = versionWrittenByHandler(thisObj)
	resp.Dirty = eventDirty
	compactFormCloseDelta(&resp, closeInv)
	respondJSON(enc, resp)
}

func (s *Server) handleMissingEntityFormClose(w http.ResponseWriter, r *http.Request, inv *formCloseInvocation, entityName string) {
	var cancel context.CancelFunc
	r, cancel = s.withFormCloseOperationDeadline(r, opFormEvent)
	defer cancel()
	enc := json.NewEncoder(w)
	// Metadata may disappear after the browser rendered a form. In that case we
	// cannot reconstruct the old metadata-derived body limit: a formerly valid
	// request may contain any number of rich-text fields. The fixed lifecycle
	// headers are deliberately independent of the body, so reserve/correlate the
	// intent without reading a potentially large stale form at all.
	value := func(name string) string { return missingFormCloseValue(r, name) }
	formKind := strings.ToLower(value("_kind"))
	if formKind == "" {
		formKind = "object"
	}
	rawID := value("_id")
	kind := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
	routeKey := "entity|" + kind + "|" + strings.ToLower(entityName) + "|" + formKind + "|" + rawID
	if err := s.prepareMissingFormCloseInvocation(r, inv, routeKey, value); err != nil {
		status := http.StatusBadRequest
		var closeErr *formCloseHTTPError
		if errors.As(err, &closeErr) {
			status = closeErr.status
		}
		w.WriteHeader(status)
		respondJSON(enc, formEventResponse{
			Error: err.Error(), Dirty: boolPtr(true),
			Close: &formCloseDecision{IntentID: inv.intentID, Reconcile: errors.Is(err, errFormCloseReplayExpired)},
		})
		return
	}
	// With the entity definition gone there is no current field policy/RLS
	// evaluator to authorize a recovered saved id. An existing replay must stay
	// open for identity-free reconciliation: terminal success would conceal an
	// uncertain durable result while SavedID/version cannot be disclosed safely.
	inv.suppressState = true
	if inv.replay != nil {
		var replayResponse formEventResponse
		if json.Unmarshal(inv.replay.body, &replayResponse) == nil && replayResponse.Close != nil && replayResponse.Close.Terminal {
			// A fresh discard performed after metadata removal already proved that
			// no write was attempted. Preserve that retained terminal outcome for a
			// lost-response retry; only saved/nonterminal cached results are
			// uncertain without current metadata and require reconciliation.
			inv.terminal = true
			return
		}
		inv.reconcile = true
		return
	}
	// A fresh discard performs no write and may close terminally. Fresh write
	// modes cannot be executed without metadata (body limits, field policy and
	// persistence schema are all unknown), so keep the form open and dirty.
	if inv.mode == "discard" {
		inv.terminal = true
		respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
		return
	}
	inv.reconcile = true
	w.WriteHeader(http.StatusConflict)
	respondJSON(enc, formEventResponse{
		Error: "описание объекта удалено с сервера; обновите форму перед повтором сохранения",
		Dirty: boolPtr(true),
	})
}

// mergePersistedTablePartsUnchanged mirrors mergePersistedFieldsUnchanged for
// a handler that called Object.Write. Rows still equal to the exact successful
// write snapshot may absorb canonical/external DB changes; edits performed by
// BeforeClose after that write remain unsaved form state and must win.
func (s *Server) mergePersistedTablePartsUnchanged(
	ctx context.Context,
	entity *metadata.Entity,
	obj *runtime.Object,
	lastSaved map[string][]map[string]any,
) {
	if s == nil || s.store == nil || entity == nil || obj == nil || obj.ID == uuid.Nil {
		return
	}
	if obj.TablePartRows == nil {
		obj.TablePartRows = map[string][]map[string]any{}
	}
	for _, tp := range entity.TableParts {
		if !tpRowsEqual(obj.TablePartRows[tp.Name], lastSaved[tp.Name], tp) {
			continue
		}
		fresh, err := s.store.GetTablePartRows(ctx, entity.Name, tp.Name, obj.ID, tp)
		if err != nil {
			continue
		}
		s.enrichTPRowsWithRefs(ctx, tp, fresh)
		obj.TablePartRows[tp.Name] = fresh
	}
}

func boolPtr(value bool) *bool { return &value }

func persistedEntityVersion(row map[string]any) int64 {
	value, ok := maskCIKeyValue(row, "_version")
	if !ok {
		return 0
	}
	switch version := value.(type) {
	case int64:
		return version
	case int:
		return int64(version)
	default:
		parsed, _ := strconv.ParseInt(fmt.Sprint(version), 10, 64)
		return parsed
	}
}

func mergePersistedEntityServiceFields(entity *metadata.Entity, persisted, fields map[string]any) {
	if entity == nil || persisted == nil || fields == nil {
		return
	}
	keys := []string{"deletion_mark"}
	if entity.Posting {
		keys = append(keys, "posted")
	}
	if entity.Hierarchical {
		keys = append(keys, "parent_id", "is_folder")
	}
	for _, name := range keys {
		for key := range fields {
			if strings.EqualFold(key, name) {
				delete(fields, key)
			}
		}
		if value, ok := maskCIKeyValue(persisted, name); ok {
			fields[name] = value
		}
	}
}

// mergePersistedEntityServiceFieldsForClose restores server-owned service
// state before BeforeClose. Save-like intents keep hierarchy values submitted
// by the form (the user may be moving/turning a group); missing hierarchy keys,
// discard, posting and deletion state remain canonical from storage.
func mergePersistedEntityServiceFieldsForClose(entity *metadata.Entity, persisted, fields map[string]any, preserveSubmittedHierarchy bool) {
	type presentValue struct {
		value any
		ok    bool
	}
	preserved := map[string]presentValue{}
	if preserveSubmittedHierarchy && entity != nil && entity.Hierarchical {
		for _, name := range []string{"parent_id", "is_folder"} {
			value, ok := maskCIKeyValue(fields, name)
			preserved[name] = presentValue{value: value, ok: ok}
		}
	}
	mergePersistedEntityServiceFields(entity, persisted, fields)
	for name, item := range preserved {
		if !item.ok {
			continue
		}
		for key := range fields {
			if strings.EqualFold(key, name) {
				delete(fields, key)
			}
		}
		fields[name] = item.value
	}
}

// mergePersistedFieldsUnchanged absorbs writes made through another object or
// a common module during BeforeClose, including submitted editable fields.
// Direct in-memory mutations made by BeforeClose remain unsaved form state and
// must win over the reload so the client can keep them dirty.
func mergePersistedFieldsUnchanged(entity *metadata.Entity, persisted, fields map[string]any, before map[string]string) {
	if entity == nil || persisted == nil || fields == nil {
		return
	}
	names := make([]string, 0, len(entity.Fields)+4)
	for _, field := range entity.Fields {
		names = append(names, field.Name)
	}
	names = append(names, "deletion_mark")
	if entity.Posting {
		names = append(names, "posted")
	}
	if entity.Hierarchical {
		names = append(names, "parent_id", "is_folder")
	}
	for _, name := range names {
		was, wasOK := snapshotValueCI(before, name)
		current, currentOK := maskCIKeyValue(fields, name)
		if wasOK != currentOK || wasOK && was != snapshotComparableValue(current) {
			continue
		}
		for key := range fields {
			if strings.EqualFold(key, name) {
				delete(fields, key)
			}
		}
		if value, ok := maskCIKeyValue(persisted, name); ok {
			fields[name] = value
		}
	}
}

// managedCloseStateDirty compares the final state returned to the browser
// with the durable record after BeforeClose. Entity fields/table parts are
// compared with storage; form-only attributes and ValueTables are compared
// with their pre-handler snapshot because they have no durable counterpart.
// Read failures are fail-safe: the form stays dirty instead of risking loss.
func (s *Server) managedCloseStateDirty(
	ctx context.Context,
	entity *metadata.Entity,
	form *metadata.FormModule,
	obj *runtime.Object,
	fieldsBefore map[string]string,
	tpBefore map[string][]map[string]any,
) bool {
	if s == nil || s.store == nil || entity == nil || obj == nil || obj.ID == uuid.Nil {
		return true
	}
	persisted, err := s.store.GetByID(ctx, entity.Name, obj.ID, entity)
	if err != nil || persisted == nil {
		return true
	}
	for _, field := range entity.Fields {
		stored, _ := maskCIKeyValue(persisted, field.Name)
		if tpCellNorm(field, obj.Get(field.Name)) != tpCellNorm(field, stored) {
			return true
		}
	}
	serviceKeys := []string{"deletion_mark"}
	if entity.Posting {
		serviceKeys = append(serviceKeys, "posted")
	}
	if entity.Hierarchical {
		serviceKeys = append(serviceKeys, "parent_id", "is_folder")
	}
	for _, name := range serviceKeys {
		live, _ := maskCIKeyValue(obj.Fields, name)
		stored, _ := maskCIKeyValue(persisted, name)
		if name == "parent_id" {
			if refValueString(live) != refValueString(stored) {
				return true
			}
		} else if boolCanon(asBool(live)) != boolCanon(asBool(stored)) {
			return true
		}
	}
	for _, tablePart := range entity.TableParts {
		stored, rowsErr := s.store.GetTablePartRows(ctx, entity.Name, tablePart.Name, obj.ID, tablePart)
		if rowsErr != nil || !tpRowsEqual(obj.TablePartRows[tablePart.Name], stored, tablePart) {
			return true
		}
	}
	for _, attr := range form.Attributes {
		if attr == nil || attr.MainAttribute || entityField(entity, attr.Name) != nil || entityServiceFieldName(attr.Name) {
			continue
		}
		if strings.EqualFold(attr.TypeRef, "ValueTable") {
			tp := formAttributeTablePart(attr)
			if tp == nil || !tpRowsEqual(obj.TablePartRows[attr.Name], tpBefore[attr.Name], *tp) {
				return true
			}
			continue
		}
		if !formAttrIsScalar(attr) {
			continue
		}
		before, beforeOK := snapshotValueCI(fieldsBefore, attr.Name)
		after, afterOK := maskCIKeyValue(obj.Fields, attr.Name)
		if beforeOK != afterOK || beforeOK && before != snapshotComparableValue(after) {
			return true
		}
	}
	return false
}

func snapshotValueCI(snapshot map[string]string, name string) (string, bool) {
	for key, value := range snapshot {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

// transientManagedStateDirty is used by processor forms, which have no
// durable record to compare with. It answers only whether BeforeClose changed
// the request snapshot, so a no-op denied close stays clean while an unsaved
// mutation is protected by the next close attempt.
func transientManagedStateDirty(obj *runtime.Object, fieldsBefore map[string]string, tablesBefore map[string][]map[string]any) bool {
	if obj == nil {
		return true
	}
	fieldsAfter := snapshotFieldValues(obj.Fields)
	if !snapshotStringMapsEqual(fieldsBefore, fieldsAfter) {
		return true
	}
	if len(tablesBefore) != len(obj.TablePartRows) {
		return true
	}
	for name, rowsBefore := range tablesBefore {
		rowsAfter, ok := tableRowsCI(obj.TablePartRows, name)
		if !ok || len(rowsBefore) != len(rowsAfter) {
			return true
		}
		for i := range rowsBefore {
			if !snapshotStringMapsEqual(snapshotFieldValues(rowsBefore[i]), snapshotFieldValues(rowsAfter[i])) {
				return true
			}
		}
	}
	return false
}

func snapshotStringMapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		other, ok := snapshotValueCI(right, name)
		if !ok || other != value {
			return false
		}
	}
	return true
}

func tableRowsCI(tables map[string][]map[string]any, name string) ([]map[string]any, bool) {
	for key, rows := range tables {
		if strings.EqualFold(key, name) {
			return rows, true
		}
	}
	return nil, false
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
	TPRefOptions   map[string]map[string][]map[string]any
	ConditionalCSS string
	Messages       []string
	ElementStates  *elementStates
}

// response — единственное место, где состояние переносится в ответ.
func (st formEventState) response(ok bool) formEventResponse {
	return formEventResponse{
		OK:             ok,
		Values:         st.Values,
		TableParts:     st.TableParts,
		FormTables:     st.FormTables,
		RefOptions:     st.RefOptions,
		TPRefOptions:   st.TPRefOptions,
		ConditionalCSS: st.ConditionalCSS,
		Messages:       st.Messages,
		ElementStates:  st.ElementStates,
	}
}

// elementStates — состояние элементов формы, зависящее от полей записи.
// В картах присутствует каждый элемент, у которого условие ОБЪЯВЛЕНО, — со
// значением true или false: клиент должен уметь и снять запрет, а не только
// поставить.
//
// Снять получается не всё: элемент, скрытый на момент отрисовки, в разметку не
// попал, и показать его обратно клиенту нечем — hidden=false для него ничего не
// изменит до перезагрузки страницы. Для readonly обе стороны рабочие.
type elementStates struct {
	ReadOnly map[string]bool `json:"readonly,omitempty"`
	Hidden   map[string]bool `json:"hidden,omitempty"`
}

// formElementStates пересчитывает readonly_when/hidden_when по значениям формы
// ПОСЛЕ обработчика: команда меняет состояние объекта, и доступность полей
// должна измениться сразу, а не после перезагрузки страницы. nil, если условий
// в форме нет — клиенту нечего применять.
func (s *Server) formElementStates(form *metadata.FormModule, entity *metadata.Entity, values map[string]any) *elementStates {
	if form == nil || s.interp == nil {
		return nil
	}
	ro, hidden, _ := managedFormElementStates(form, managedFormHeaderValues(entity, values), newInterpEvaluator(s.interp))
	if len(ro) == 0 && len(hidden) == 0 {
		return nil
	}
	return &elementStates{ReadOnly: ro, Hidden: hidden}
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
	for _, k := range []string{"ссылка", "reference", "_version"} {
		if _, isEntityField := entityFieldByName(entity, k); !isEntityField {
			delete(values, k)
		}
	}
	tableParts := serializeTablePartRowsForEntity(obj.TablePartRows, entity, form)
	// Виртуальные колонки пересчитываются и в ответе события (#845). Клиент
	// применяет tableparts целиком, а колонки в payload нет — без пересчёта
	// первое же серверное событие гасило бы её значения. Побочно это и есть
	// «пересчёт на лету»: выбрал в строке другую ссылку, обработчик строки
	// отработал — колонка приехала обновлённой.
	s.applyVirtualTPColumns(ctx, entity, form, tableParts)
	tpRefOptions, _ := s.loadInitialTPRefOptions(ctx, entity, tableParts)
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
		TPRefOptions:   tpRefOptions,
		ConditionalCSS: conditionalCSS,
		Messages:       msgs,
		// Условия readonly_when/hidden_when считаются здесь же, где известны
		// значения ПОСЛЕ обработчика: команда, изменившая состояние объекта,
		// сразу меняет доступность полей.
		ElementStates: s.formElementStates(form, entity, values),
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
	fields, err := formToFields(r, entity)
	if err != nil {
		return nil, err
	}
	mergeSubmittedEntityServiceFields(r, entity, fields)
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
		//
		// In(time.Local) обязателен: формат без зоны печатает стенные часы, а
		// драйверы отдают момент в РАЗНЫХ зонах (SQLite всегда UTC, pgx — в зоне
		// процесса). Без приведения одна и та же дата давала разные стенные часы
		// на разных СУБД, а на хосте со смещением от UTC у SQLite съезжал
		// календарный день (#1077).
		return t.In(time.Local).Format("2006-01-02T15:04")
	case *time.Time:
		if t == nil {
			return ""
		}
		return t.In(time.Local).Format("2006-01-02T15:04")
	case fmt.Stringer:
		return t.String()
	}
	return v
}

// handleProcessorFormEvent обрабатывает события managed-формы обработки.
// Аналог handleManagedFormEvent, но вместо Entity использует виртуальную entity
// из параметров обработки. Кнопка «Выполнить» запускает proc.os через interp.
func (s *Server) handleProcessorFormEvent(w http.ResponseWriter, r *http.Request) {
	s.handleProcessorFormEventMode(w, r, nil)
}

func (s *Server) handleProcessorFormEventMode(w http.ResponseWriter, r *http.Request, closeInv *formCloseInvocation) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)

	procName := chi.URLParam(r, "name")
	if procName == "" {
		respondJSON(enc, formEventResponse{Error: "processor name required"})
		return
	}
	proc := s.reg.GetProcessor(procName)
	if proc == nil {
		if closeInv != nil {
			s.handleMissingProcessorFormClose(w, r, closeInv, procName)
			return
		}
		respondJSON(enc, formEventResponse{Error: "processor not found: " + procName})
		return
	}
	canRun := s.can(r, "processor", proc.Name, "run")
	canRunExternal := s.canRunExternalProc(r, proc)
	if closeInv == nil && !canRun {
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	}
	// Тот же trust-гейт, что и у processorRun: form-event исполняет DSL
	// обработки (form-обработчики и кнопка «Выполнить»), поэтому недоверенную
	// внешнюю обработку здесь тоже может запускать только администратор —
	// иначе неадмин обходил бы проверку через /form-event.
	if closeInv == nil && !canRunExternal {
		respondJSON(enc, formEventResponse{Error: "доступ запрещён"})
		return
	}

	form := proc.ManagedForm()
	if form == nil && closeInv == nil {
		respondJSON(enc, formEventResponse{Error: "managed form not found for " + procName})
		return
	}
	maxSize := s.effectiveUploadLimit()
	requestControls := processorRequestControlsForForm(proc, form)
	currentBodyLimit := processorFormBodyLimit(r, maxSize, requestControls)
	if closeInv != nil {
		var cancel context.CancelFunc
		r, cancel = s.withFormCloseOperationDeadline(r, opProcessorRun)
		defer cancel()
		value := func(name string) string { return missingFormCloseValue(r, name) }
		routeKey := "processor|" + strings.ToLower(proc.Name)
		renderedSchema := value("_close_schema")
		currentSchema := processorFormCloseSchema(proc, form)
		if renderedSchema == "" || renderedSchema != currentSchema {
			recovered, recoverErr := s.recoverExistingFormCloseInvocation(r, closeInv, routeKey, value)
			if recoverErr != nil {
				status := http.StatusBadRequest
				var closeErr *formCloseHTTPError
				if errors.As(recoverErr, &closeErr) {
					status = closeErr.status
				}
				w.WriteHeader(status)
				respondJSON(enc, formEventResponse{
					Error: recoverErr.Error(), Dirty: boolPtr(true),
					Close: &formCloseDecision{IntentID: closeInv.intentID, Reconcile: errors.Is(recoverErr, errFormCloseReplayExpired)},
				})
				return
			}
			if recovered {
				if form == nil || !canRun || !canRunExternal {
					closeInv.suppressState = true
					closeInv.terminal = true
				}
				return
			}
		}
		if renderedSchema == "" || renderedSchema != currentSchema {
			if err := s.prepareMissingFormCloseInvocation(r, closeInv, routeKey, value); err != nil {
				status := http.StatusBadRequest
				var closeErr *formCloseHTTPError
				if errors.As(err, &closeErr) {
					status = closeErr.status
				}
				w.WriteHeader(status)
				respondJSON(enc, formEventResponse{
					Error: err.Error(), Dirty: boolPtr(true),
					Close: &formCloseDecision{IntentID: closeInv.intentID, Reconcile: errors.Is(err, errFormCloseReplayExpired)},
				})
				return
			}
			closeInv.suppressState = true
			closeInv.terminal = true
			if closeInv.replay == nil {
				respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
			}
			return
		}
	}

	// Один предел, а не два вложенных: пределы не композируются, и прежний
	// MaxBytesReader на defaultFormMemoryBytes связывал раньше, обрезая
	// файл-параметр обработки мегабайтом (issue #674). Авторизация и trust-гейт
	// выше выполняются до разбора потенциально большого multipart-тела.
	if requestControls.formTablesErr != nil {
		respondJSON(enc, formEventResponse{Error: requestControls.formTablesErr.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, currentBodyLimit)
	if closeInv != nil {
		// Parse and reserve before the processor semaphore so an exact duplicate
		// joins the in-flight close instead of being rejected by its occupied slot.
		if err := parseBoundedForm(r, 32<<20); err != nil {
			w.WriteHeader(uploadErrorStatus(err))
			respondJSON(enc, formEventResponse{Error: s.errText(r, formBodyError(err, nil))})
			return
		}
		value := func(name string) string { return missingFormCloseValue(r, name) }
		routeKey := "processor|" + strings.ToLower(proc.Name)
		if err := s.prepareFormCloseInvocation(r, closeInv, routeKey, value); err != nil {
			var closeErr *formCloseHTTPError
			reconcile := false
			if errors.As(err, &closeErr) {
				w.WriteHeader(closeErr.status)
			}
			reconcile = errors.Is(err, errFormCloseReplayExpired)
			respondJSON(enc, formEventResponse{
				Error: err.Error(), Dirty: boolPtr(true),
				Close: &formCloseDecision{IntentID: closeInv.intentID, Reconcile: reconcile},
			})
			return
		}
		if form == nil {
			closeInv.suppressState = true
			closeInv.terminal = true
			if closeInv.replay == nil {
				respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
			}
			return
		}
		if !canRun || !canRunExternal {
			// An already-open processor form must remain closable after run/trust
			// permission is revoked. Reserve/recover the intent first so the answer
			// stays correlated, then terminate without executing trusted form code.
			closeInv.suppressState = true
			closeInv.terminal = true
			if closeInv.replay != nil {
				return
			}
			respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
			return
		}
		if closeInv.replay != nil {
			return
		}
	}
	opCtx, finish, ok := s.beginOperation(r, opProcessorRun, proc.Name)
	if !ok {
		w.WriteHeader(http.StatusTooManyRequests)
		respondJSON(enc, formEventResponse{Error: "слишком много одновременно выполняемых обработок, повторите позже"})
		return
	}
	opStatus := "ok"
	defer func() { finish(opStatus, 0, false) }()

	if closeInv == nil {
		if err := parseBoundedForm(r, 32<<20); err != nil {
			opStatus = "error"
			w.WriteHeader(uploadErrorStatus(err))
			respondJSON(enc, formEventResponse{Error: s.errText(r, formBodyError(err, nil))})
			return
		}
	}
	elementValue, _ := processorPostFormText(r, processorServiceFieldName(proc.Params, "_element"))
	eventValue, _ := processorPostFormText(r, processorServiceFieldName(proc.Params, "_event"))
	elementName := strings.TrimSpace(elementValue)
	eventName := strings.TrimSpace(eventValue)
	if closeInv != nil {
		elementName = ""
		eventName = string(metadata.FormEventBeforeClose)
	}
	if eventName == "" {
		opStatus = "error"
		respondJSON(enc, formEventResponse{Error: "_event required"})
		return
	}

	// Явно привязанный обработчик имеет безусловный приоритет. Если его нет в
	// .form.os, это ошибка конфигурации, а не разрешение незаметно выполнить
	// глобальную процедуру Выполнить.
	var boundProcName string
	var eventTarget browserFormEventTarget
	var executeFallback bool
	if closeInv != nil {
		boundProcName = resolveFormCloseHandler(form)
		if boundProcName == "" {
			respondJSON(enc, formEventResponse{OK: true})
			return
		}
	} else {
		var eligibilityErr error
		boundProcName, eventTarget, executeFallback, eligibilityErr = resolveBrowserFormEvent(form, elementName, eventName, true)
		if eligibilityErr != nil {
			opStatus = "error"
			respondJSON(enc, formEventResponse{Error: eligibilityErr.Error()})
			return
		}
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
		var closeArgs []any
		if closeInv != nil {
			closeArgs, err = validateFormCloseProcedure(decl)
			if err != nil {
				opStatus = "error"
				respondJSON(enc, formEventResponse{Error: err.Error()})
				return
			}
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
		if closeInv != nil {
			baseline := s.serializeManagedFormEventState(r.Context(), form, virtEntity, obj, nil, nil).response(false)
			closeInv.baselineValues = baseline.Values
			closeInv.baselineTableParts = baseline.TableParts
			closeInv.baselineFormTables = baseline.FormTables
		}
		fieldsBefore := snapshotFieldValues(obj.Fields)
		tablesBefore := tablePartRowsSnapshot(obj.TablePartRows)
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
		if closeInv != nil {
			vars["ПричинаЗакрытия"] = closeInv.reason
			vars["CloseReason"] = closeInv.reason
		}

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
		timeout := processorSandboxTimeout(opCtx, s.operationTimeout(opProcessorRun))
		if closeInv != nil {
			var entryResult interpreter.EntryCallResult
			entryResult, runErr = s.interp.CallEntrySandboxedWithBindings(decl, thisObj, closeArgs,
				interpreter.SandboxProfile{Context: dslCtx, MaxWallClock: timeout}, vars)
			if runErr == nil {
				closeInv.cancelled = formCloseBindingCancelled(decl, entryResult.Bindings)
			}
		} else if timeout > 0 {
			runErr = s.interp.RunSandboxed(decl, thisObj,
				interpreter.SandboxProfile{Context: dslCtx, MaxWallClock: timeout}, nil, vars)
		} else {
			runErr = s.interp.Run(decl, thisObj, vars)
		}
		runErr = finishDSLExecution(txState, runErr)
		if runErr != nil {
			opStatus = operationStatus(opCtx, runErr)
			resp := s.serializeManagedFormEventState(r.Context(), form, virtEntity, obj, condRuntime.rules, msgs).response(false)
			resp.Error = interpreter.FormatUserError(runErr)
			resp.PickerData = picker
			resp.Dirty = boolPtr(transientManagedStateDirty(obj, fieldsBefore, tablesBefore))
			compactFormCloseDelta(&resp, closeInv)
			respondJSON(enc, resp)
			return
		}

		resp := s.serializeManagedFormEventState(r.Context(), form, virtEntity, obj, condRuntime.rules, msgs).response(true)
		resp.PickerData = picker
		resp.Dirty = boolPtr(transientManagedStateDirty(obj, fieldsBefore, tablesBefore))
		compactFormCloseDelta(&resp, closeInv)
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
				interpreter.SandboxProfile{Context: dslCtx, MaxWallClock: timeout}, dslVars)
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

func (s *Server) handleMissingProcessorFormClose(w http.ResponseWriter, r *http.Request, inv *formCloseInvocation, procName string) {
	var cancel context.CancelFunc
	r, cancel = s.withFormCloseOperationDeadline(r, opProcessorRun)
	defer cancel()
	enc := json.NewEncoder(w)
	// See handleMissingEntityFormClose: after hot removal the old request body
	// can be larger than any limit derivable from the remaining registry.
	value := func(name string) string { return missingFormCloseValue(r, name) }
	if err := s.prepareMissingFormCloseInvocation(r, inv, "processor|"+strings.ToLower(procName), value); err != nil {
		status := http.StatusBadRequest
		var closeErr *formCloseHTTPError
		if errors.As(err, &closeErr) {
			status = closeErr.status
		}
		w.WriteHeader(status)
		respondJSON(enc, formEventResponse{
			Error: err.Error(), Dirty: boolPtr(true),
			Close: &formCloseDecision{IntentID: inv.intentID, Reconcile: errors.Is(err, errFormCloseReplayExpired)},
		})
		return
	}
	inv.suppressState = true
	inv.terminal = true
	if inv.replay == nil {
		respondJSON(enc, formEventResponse{OK: true, Dirty: boolPtr(false)})
	}
}

func missingFormCloseValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	headers := map[string]string{
		"_close_intent_id": "X-OneBase-Close-Intent",
		"_close_epoch":     "X-OneBase-Close-Epoch",
		"_close_issued_at": "X-OneBase-Close-Issued-At",
		"_close_reason":    "X-OneBase-Close-Reason",
		"_close_mode":      "X-OneBase-Close-Mode",
		"_close_client":    "X-OneBase-Close-Client",
		"_close_schema":    "X-OneBase-Close-Schema",
		"_kind":            "X-OneBase-Form-Kind",
		"_id":              "X-OneBase-Record-ID",
	}
	return strings.TrimSpace(r.Header.Get(headers[name]))
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
