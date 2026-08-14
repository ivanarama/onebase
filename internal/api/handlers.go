package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/webhook"
)

const (
	restDefaultLimit = 100
	restMaxLimit     = 1000
	restMaxBodyBytes = 50 * 1024 * 1024
)

type handler struct {
	reg       *runtime.Registry
	store     *storage.DB
	interp    *interpreter.Interpreter
	entitySvc *entityservice.Service // разделяем с ui — см. ui.Server.EntitySvc()
	hooks     *webhook.Dispatcher    // исходящие веб-хуки (план 29); nil-safe
	// Настройки вложений (план 55) — те же, что у UI-пути (ui.Config): нужны для
	// REST v2 attachments (issue #315). maxFileSizeBytes == 0 → дефолт 50 МБ;
	// allowedAttachmentTypes пусто → без ограничений по расширению.
	maxFileSizeBytes       int64
	allowedAttachmentTypes []string
}

// dispatchHook шлёт исходящий веб-хук после успешной операции. REST-путь
// обязан оповещать интеграции так же, как UI (план 29): save/post идут через
// entityservice и событие шлют, а unpost/delete раньше уходили молча.
func (h *handler) dispatchHook(ctx context.Context, name, entity string, id uuid.UUID) {
	if h.hooks.Enabled() {
		h.hooks.Dispatch(webhook.Event{
			Name: name, Entity: entity, ID: id.String(),
			User: storage.AuditUserLogin(ctx),
		})
	}
}

// createUpdateBody — JSON-контракт для POST/PUT. Шапка — flat-поля (как раньше),
// плюс опциональные ТЧ через __tableparts. Action позволяет явно провести
// документ в одном вызове (раньше нужно было два запроса: PUT + кнопка UI).
//
// Пример:
//
//	{
//	  "Номер": "100", "Контрагент": "...",
//	  "__tableparts": {"Товары": [{"Номенклатура":"...","Количество":3}]},
//	  "__action": "post"
//	}
type createUpdateBody struct {
	Fields        map[string]any              `json:"-"`
	TablePartRows map[string][]map[string]any `json:"__tableparts,omitempty"`
	Action        string                      `json:"__action,omitempty"`
}

var errIfMatchRequired = errors.New("If-Match header is required")

// decodeBody парсит ровно один JSON-объект в createUpdateBody, отделяя
// служебные ключи (__tableparts, __action) от собственных полей сущности.
// Делаем это вручную через generic map, чтобы пользователю не нужно было
// оборачивать поля в "fields": {...}.
func decodeBody(r *http.Request) (createUpdateBody, error) {
	return decodeRESTBody(r)
}

func decodeRESTBody(r *http.Request) (createUpdateBody, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&raw); err != nil {
		return createUpdateBody{}, err
	}
	if raw == nil {
		return createUpdateBody{}, errors.New("request body must be a JSON object")
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return createUpdateBody{}, errors.New("request body must contain a single JSON object")
		}
		return createUpdateBody{}, fmt.Errorf("invalid trailing data: %w", err)
	}
	body := createUpdateBody{Fields: make(map[string]any, len(raw))}
	if v, ok := raw["__tableparts"]; ok {
		if err := json.Unmarshal(v, &body.TablePartRows); err != nil {
			return createUpdateBody{}, fmt.Errorf("__tableparts: %w", err)
		}
		delete(raw, "__tableparts")
	}
	if v, ok := raw["__action"]; ok {
		if err := json.Unmarshal(v, &body.Action); err != nil {
			return createUpdateBody{}, fmt.Errorf("__action: %w", err)
		}
		delete(raw, "__action")
	}
	for k, v := range raw {
		if strings.HasPrefix(k, "__") {
			return createUpdateBody{}, fmt.Errorf("unknown metadata field %q", k)
		}
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			return createUpdateBody{}, fmt.Errorf("%s: %w", k, err)
		}
		body.Fields[k] = val
	}
	return body, nil
}

func decodeBodyForEntity(r *http.Request, entity *metadata.Entity) (createUpdateBody, error) {
	body, err := decodeRESTBody(r)
	if err != nil {
		return createUpdateBody{}, err
	}
	if err := validateRESTBody(&body, entity); err != nil {
		return createUpdateBody{}, err
	}
	return body, nil
}

// decodeOptionalBodyForEntity distinguishes an absent body from an empty JSON
// object. It deliberately attempts a decode when Content-Length is unknown
// (HTTP/1.1 chunked and HTTP/2), instead of treating -1 as an empty request.
func decodeOptionalBodyForEntity(r *http.Request, entity *metadata.Entity) (createUpdateBody, bool, error) {
	if r.Body == nil || r.ContentLength == 0 {
		return createUpdateBody{}, false, nil
	}
	body, err := decodeRESTBody(r)
	if errors.Is(err, io.EOF) {
		return createUpdateBody{}, false, nil
	}
	if err != nil {
		return createUpdateBody{}, false, err
	}
	if err := validateRESTBody(&body, entity); err != nil {
		return createUpdateBody{}, false, err
	}
	return body, true, nil
}

func validateRESTBody(body *createUpdateBody, entity *metadata.Entity) error {
	if body == nil || entity == nil {
		return errors.New("entity metadata is required")
	}

	fields := make(map[string]string, len(entity.Fields)+2)
	for _, field := range entity.Fields {
		fields[strings.ToLower(field.Name)] = field.Name
	}
	if entity.Hierarchical {
		fields["parent_id"] = "parent_id"
		fields["is_folder"] = "is_folder"
	}
	normalizedFields := make(map[string]any, len(body.Fields))
	for name, value := range body.Fields {
		canonical, ok := fields[strings.ToLower(name)]
		if !ok {
			return fmt.Errorf("unknown field %q", name)
		}
		if _, duplicate := normalizedFields[canonical]; duplicate {
			return fmt.Errorf("duplicate field %q", canonical)
		}
		normalizedFields[canonical] = value
	}
	body.Fields = normalizedFields

	tableParts := make(map[string]metadata.TablePart, len(entity.TableParts))
	for _, tp := range entity.TableParts {
		tableParts[strings.ToLower(tp.Name)] = tp
	}
	normalizedTPs := make(map[string][]map[string]any, len(body.TablePartRows))
	for name, rows := range body.TablePartRows {
		tp, ok := tableParts[strings.ToLower(name)]
		if !ok {
			return fmt.Errorf("unknown table part %q", name)
		}
		if _, duplicate := normalizedTPs[tp.Name]; duplicate {
			return fmt.Errorf("duplicate table part %q", tp.Name)
		}
		rowFields := make(map[string]string, len(tp.Fields))
		for _, field := range tp.Fields {
			rowFields[strings.ToLower(field.Name)] = field.Name
		}
		normalizedRows := make([]map[string]any, len(rows))
		for i, row := range rows {
			normalizedRow := make(map[string]any, len(row))
			for fieldName, value := range row {
				canonical, ok := rowFields[strings.ToLower(fieldName)]
				if !ok {
					return fmt.Errorf("unknown field %q in table part %q row %d", fieldName, tp.Name, i+1)
				}
				if _, duplicate := normalizedRow[canonical]; duplicate {
					return fmt.Errorf("duplicate field %q in table part %q row %d", canonical, tp.Name, i+1)
				}
				normalizedRow[canonical] = value
			}
			normalizedRows[i] = normalizedRow
		}
		normalizedTPs[tp.Name] = normalizedRows
	}
	body.TablePartRows = normalizedTPs

	action := strings.ToLower(strings.TrimSpace(body.Action))
	switch action {
	case "":
	case "post", "post_and_close":
		if entity.Kind != metadata.KindDocument || !entity.Posting {
			return fmt.Errorf("action %q is not supported for entity %q", action, entity.Name)
		}
	default:
		return fmt.Errorf("unknown action %q", body.Action)
	}
	body.Action = action
	return nil
}

func (h *handler) createObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromRoute(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "write") {
			return
		}
		limitRESTBody(w, r)
		body, err := decodeBodyForEntity(r, entity)
		if err != nil {
			writeDecodeError(w, err)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) && !requireRESTPerm(w, r, kind, entityName, "post") {
			return
		}

		// Автонумерация здесь больше не делается: её выполняет
		// entityservice.Save для ЛЮБОГО нового объекта (#869). Прежняя копия
		// искала поле по имени «Номер» и потому не нумеровала справочники
		// вовсе — элемент, созданный через REST, оставался без кода.
		if err := h.autoFillRowAccessFields(r.Context(), entity, "write", body.Fields); err != nil {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) {
			if err := h.autoFillRowAccessFields(r.Context(), entity, "post", body.Fields); err != nil {
				writeError(w, http.StatusForbidden, "forbidden", "", 0)
				return
			}
		}
		if !h.rowAllowed(r.Context(), entity, "write", body.Fields) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) && !h.rowAllowed(r.Context(), entity, "post", body.Fields) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}

		result, err := h.entitySvc.Save(r.Context(), entityservice.SaveRequest{
			Entity:        entity,
			ID:            uuid.New(),
			IsNew:         true,
			Fields:        body.Fields,
			TablePartRows: body.TablePartRows,
			Action:        body.Action,
		})
		if err != nil {
			writeSaveError(w, err)
			return
		}
		if result.DSLError != "" {
			writeError(w, http.StatusUnprocessableEntity, result.DSLError, "", 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       result.ID.String(),
			"messages": result.DSLMessages,
		})
	}
}

func (h *handler) getObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromRoute(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "read") {
			return
		}
		idStr := chi.URLParam(r, "id")
		id, err := uuid.Parse(idStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		result, err := h.store.GetByID(r.Context(), entityName, id, entity)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error(), "", 0)
			return
		}
		if !h.rowAllowed(r.Context(), entity, "read", result) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		h.maskRecord(r.Context(), entity, result)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

func (h *handler) listObjects(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromRoute(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "read") {
			return
		}
		params, err := parseRestListParams(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "", 0)
			return
		}
		params, err = h.applyRowFilter(r.Context(), entity, "read", params)
		if err != nil {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		rows, err := h.store.List(r.Context(), entityName, entity, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		total, err := h.store.CountList(r.Context(), entityName, entity, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		h.maskRecords(r.Context(), entity, rows)
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		w.Header().Set("X-Limit", strconv.Itoa(params.Limit))
		w.Header().Set("X-Offset", strconv.Itoa(params.Offset))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	}
}

func (h *handler) updateObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromRoute(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "write") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		limitRESTBody(w, r)
		body, err := decodeBodyForEntity(r, entity)
		if err != nil {
			writeDecodeError(w, err)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) && !requireRESTPerm(w, r, kind, entityName, "post") {
			return
		}
		if !h.rowAllowedUpdate(r.Context(), entity, "write", id, body.Fields) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		if kind == metadata.KindDocument && isPostAction(body.Action) && !h.rowAllowedUpdate(r.Context(), entity, "post", id, body.Fields) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}

		// If-Match — optimistic locking. Клиент шлёт версию которую он видел
		// при чтении; если в БД сейчас другая — 409 Conflict вместо тихого
		// перетирания чужих правок. Без заголовка проверка не делается
		// (обратная совместимость для клиентов которые её ещё не используют).
		expectedVersion, err := parseIfMatch(r, false)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "", 0)
			return
		}

		// План 88: масковый пользователь не перезаписывает реальное значение.
		if err := h.protectMaskedFieldsOnWrite(r.Context(), entity, id, body.Fields); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}

		result, err := h.entitySvc.Save(r.Context(), entityservice.SaveRequest{
			Entity:          entity,
			ID:              id,
			IsNew:           false,
			Fields:          body.Fields,
			TablePartRows:   body.TablePartRows,
			Action:          body.Action,
			ExpectedVersion: expectedVersion,
		})
		if err != nil {
			writeSaveError(w, err)
			return
		}
		if result.DSLError != "" {
			writeError(w, http.StatusUnprocessableEntity, result.DSLError, "", 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       id.String(),
			"messages": result.DSLMessages,
		})
	}
}

func (h *handler) deleteObject(kind metadata.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromRoute(w, r, kind)
		if !ok {
			return
		}
		if !requireRESTPerm(w, r, kind, entityName, "delete") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		if !h.rowAllowedID(r.Context(), entity, "delete", id) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}
		// Через entityservice: хуки модуля объекта «ПередУдалением»/
		// «ПослеУдаления» обязаны срабатывать и по REST — иначе запрет,
		// написанный в конфигурации, обходится сменой способа удаления.
		res, err := h.entitySvc.Delete(r.Context(), entity, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
			return
		}
		if res.DSLError != "" {
			writeError(w, http.StatusConflict, res.DSLError, "", 0)
			return
		}
		h.dispatchHook(r.Context(), string(kind)+".delete", entityName, id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// postDocument — POST /documents/{entity}/{id}/post. Проводит существующий
// документ: запускает OnPost, пишет движения, ставит posted=true. Это
// функциональная дыра API: раньше провести документ через REST было
// невозможно (только через UI-кнопку).
//
// Тело может быть пустым (id берётся из URL) либо содержать обновлённые
// поля шапки/ТЧ (тогда сначала применятся изменения, потом проведение —
// аналогично UI «Записать и провести»).
func (h *handler) postDocument() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entity, entityName, ok := h.entityFromRoute(w, r, metadata.KindDocument)
		if !ok {
			return
		}
		if !entity.Posting {
			writeError(w, http.StatusBadRequest, "entity is not postable: "+entityName, "", 0)
			return
		}
		if !requireRESTPerm(w, r, metadata.KindDocument, entityName, "post") {
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid id", "", 0)
			return
		}
		if !h.rowAllowedID(r.Context(), entity, "post", id) {
			writeError(w, http.StatusForbidden, "forbidden", "", 0)
			return
		}

		// Тело опционально. Если есть — используем как обновление перед проведением;
		// если пусто — читаем текущее состояние из БД, чтобы OnPost увидел актуальные
		// данные документа.
		limitRESTBody(w, r)
		body, hasBody, decErr := decodeOptionalBodyForEntity(r, entity)
		if decErr != nil {
			writeDecodeError(w, decErr)
			return
		}
		var fields map[string]any
		var tpRows map[string][]map[string]any
		if hasBody {
			if !requireRESTPerm(w, r, metadata.KindDocument, entityName, "write") {
				return
			}
			if !h.rowAllowedUpdate(r.Context(), entity, "write", id, body.Fields) ||
				!h.rowAllowedUpdate(r.Context(), entity, "post", id, body.Fields) {
				writeError(w, http.StatusForbidden, "forbidden", "", 0)
				return
			}
			fields = body.Fields
			tpRows = body.TablePartRows
		} else {
			existing, gerr := h.store.GetByID(r.Context(), entityName, id, entity)
			if gerr != nil {
				writeError(w, http.StatusNotFound, gerr.Error(), "", 0)
				return
			}
			fields = existing
			// Догружаем табличные части из БД. Иначе OnPost увидел бы пустую
			// this.Товары и посчитал движения по пустой ТЧ, а сам документ
			// потерял бы строки. Передаём их в Save явно (ключ присутствует ⇒
			// строки сохраняются как есть).
			tpRows = make(map[string][]map[string]any, len(entity.TableParts))
			for _, tp := range entity.TableParts {
				rows, terr := h.store.GetTablePartRows(r.Context(), entityName, tp.Name, id, tp)
				if terr != nil {
					writeError(w, http.StatusInternalServerError, terr.Error(), "", 0)
					return
				}
				tpRows[tp.Name] = rows
			}
		}

		result, err := h.entitySvc.Save(r.Context(), entityservice.SaveRequest{
			Entity:        entity,
			ID:            id,
			IsNew:         false,
			Fields:        fields,
			TablePartRows: tpRows,
			Action:        "post",
		})
		if err != nil {
			writeSaveError(w, err)
			return
		}
		if result.DSLError != "" {
			writeError(w, http.StatusUnprocessableEntity, result.DSLError, "", 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":       id.String(),
			"posted":   true,
			"messages": result.DSLMessages,
		})
	}
}

func (h *handler) entityFromRoute(w http.ResponseWriter, r *http.Request, kind metadata.Kind) (*metadata.Entity, string, bool) {
	entityName := capitalize(chi.URLParam(r, "entity"))
	entity := h.reg.GetEntity(entityName)
	if entity == nil || entity.Kind != kind {
		writeError(w, http.StatusNotFound, "unknown entity: "+entityName, "", 0)
		return nil, entityName, false
	}
	return entity, entityName, true
}

func requireRESTPerm(w http.ResponseWriter, r *http.Request, kind metadata.Kind, entity, op string) bool {
	if canREST(r.Context(), string(kind), entity, op) {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", "", 0)
	return false
}

func canREST(ctx context.Context, kind, entity, op string) bool {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return auth.OpenAccessFromContext(ctx)
	}
	return u.Has(kind, entity, op)
}

func isPostAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "post", "post_and_close":
		return true
	default:
		return false
	}
}

func parseIfMatch(r *http.Request, required bool) (*int64, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		if required {
			return nil, errIfMatchRequired
		}
		return nil, nil
	}
	if strings.HasPrefix(raw, "W/") || strings.Contains(raw, ",") {
		return nil, errors.New("invalid If-Match header")
	}
	if strings.HasPrefix(raw, `"`) || strings.HasSuffix(raw, `"`) {
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			return nil, errors.New("invalid If-Match header")
		}
		raw = raw[1 : len(raw)-1]
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 0 {
		return nil, errors.New("invalid If-Match header")
	}
	return &version, nil
}

func limitRESTBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, restMaxBodyBytes)
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large", "", 0)
		return
	}
	writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "", 0)
}

func parseRestFilters(r *http.Request) map[string]storage.FilterValue {
	filters := make(map[string]storage.FilterValue)
	for k, vals := range r.URL.Query() {
		if strings.HasPrefix(k, "f.") && len(vals) > 0 {
			filters[strings.TrimPrefix(k, "f.")] = storage.FilterValue{Value: vals[0]}
		}
	}
	return filters
}

func parseRestListParams(r *http.Request) (storage.ListParams, error) {
	q := r.URL.Query()
	limit, err := parsePositiveInt(q.Get("limit"), restDefaultLimit)
	if err != nil {
		return storage.ListParams{}, errors.New("invalid limit")
	}
	if limit > restMaxLimit {
		limit = restMaxLimit
	}
	offset, err := parseNonNegativeInt(q.Get("offset"), 0)
	if err != nil {
		return storage.ListParams{}, errors.New("invalid offset")
	}
	params := storage.ListParams{
		Filters: parseRestFilters(r),
		Search:  q.Get("q"),
		Limit:   limit,
		Offset:  offset,
	}
	if s := q.Get("sort"); s != "" {
		params.Sort = s
	}
	if d := q.Get("dir"); d != "" {
		params.Dir = d
	}
	return params, nil
}

func parsePositiveInt(raw string, def int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, errors.New("not positive")
	}
	return n, nil
}

func parseNonNegativeInt(raw string, def int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("negative")
	}
	return n, nil
}

type errorResponse struct {
	Error string `json:"error"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
}

func writeError(w http.ResponseWriter, code int, msg, file string, line int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg, File: file, Line: line})
}

// writeSaveError отдаёт структурированную HTTP-ошибку для неуспешного
// entitySvc.Save, маппя известные ошибки storage в точные коды статуса:
// конфликт версий → 409, нарушение внешнего ключа (ссылка на несуществующий
// объект) → 422. Прочее — 500 с текстом ошибки, как прежде. Общий для v1 и v2
// REST, чтобы сырой текст драйвера БД не утекал в ответ по create/update/post.
func writeSaveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrVersionConflict):
		writeError(w, http.StatusConflict, "version conflict: object was modified by another client", "", 0)
	case errors.Is(err, storage.ErrForeignKeyViolation):
		writeError(w, http.StatusUnprocessableEntity, "ссылка на несуществующий объект", "", 0)
	case errors.Is(err, storage.ErrCodeDuplicate):
		// Занятый код — ошибка данных клиента, а не сбой сервера. Текст здесь
		// уже человеческий (план 117E) и называет поле и значение, поэтому
		// отдаём его как есть, а не подменяем общей формулировкой.
		writeError(w, http.StatusUnprocessableEntity, err.Error(), "", 0)
	default:
		writeError(w, http.StatusInternalServerError, err.Error(), "", 0)
	}
}

func capitalize(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		s = dec
	}
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}
