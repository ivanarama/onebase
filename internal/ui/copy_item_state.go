package ui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

const copySourceFormField = "_copy_source_id"

type copySourceSnapshot struct {
	row           map[string]any
	renderRow     map[string]any
	tablePartRows map[string][]map[string]any
}

type copySourceLoadError struct {
	status  int
	message string
}

func (e *copySourceLoadError) Error() string { return e.message }

// loadAuthorizedCopySource performs every read decision against the exact row
// that will be copied. The declarative RLS predicate and ПриЧтенииНаСервере
// both see this in-memory snapshot; neither is followed by another GetByID.
func (s *Server) loadAuthorizedCopySource(
	r *http.Request,
	entity *metadata.Entity,
	sourceID string,
) (*copySourceSnapshot, error) {
	id, err := uuid.Parse(strings.TrimSpace(sourceID))
	if err != nil {
		return nil, &copySourceLoadError{
			status:  http.StatusBadRequest,
			message: "Некорректный идентификатор копируемой записи: " + sourceID,
		}
	}
	if !s.can(r, string(entity.Kind), entity.Name, "read") {
		return nil, copySourceForbidden()
	}

	var row map[string]any
	tablePartRows := make(map[string][]map[string]any, len(entity.TableParts))
	var rowAllowed bool
	var hookObject *runtime.Object
	err = s.store.WithTx(r.Context(), func(txCtx context.Context) error {
		var loadErr error
		row, loadErr = s.store.GetByID(txCtx, entity.Name, id, entity)
		if loadErr != nil {
			return loadErr
		}
		// rowAllowsSelected evaluates the policy against this exact map.
		// Checking by ID would load a different row and reopen the TOCTOU.
		rowAllowed = s.rowAllowsSelected(txCtx, entity, row)
		if !rowAllowed || row == nil {
			return nil
		}
		for _, tablePart := range entity.TableParts {
			rows, partErr := s.store.GetTablePartRows(txCtx, entity.Name, tablePart.Name, id, tablePart)
			if partErr != nil {
				return partErr
			}
			tablePartRows[tablePart.Name] = rows
		}
		// References are enriched while the storage read scope is still active;
		// the hook later executes only against this detached in-memory object.
		hookObject = s.runtimeObjectFromSnapshot(txCtx, entity, id, row, tablePartRows)
		return nil
	})
	if err != nil {
		return nil, &copySourceLoadError{status: http.StatusInternalServerError, message: err.Error()}
	}
	if !rowAllowed {
		return nil, copySourceForbidden()
	}
	if row == nil {
		return nil, &copySourceLoadError{status: http.StatusNotFound, message: "Копируемая запись не найдена"}
	}

	renderRow := cloneRecord(row)
	// The canonical row remains untouched for server-side restoration. The
	// separately masked view is the only row allowed into browser values.
	s.maskRecord(r.Context(), entity, renderRow)
	snapshot := &copySourceSnapshot{row: row, renderRow: renderRow, tablePartRows: tablePartRows}
	if form := pickObjectFormWithReadHook(entity); form != nil {
		if hookErr := s.runFormReadHookOnObject(r.Context(), entity, form, hookObject); hookErr != nil {
			return nil, copySourceForbidden()
		}
	}
	return snapshot, nil
}

func cloneRecord(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for name, value := range source {
		result[name] = value
	}
	return result
}

func copySourceForbidden() error {
	return &copySourceLoadError{
		status:  http.StatusForbidden,
		message: "Нет прав на чтение копируемой записи",
	}
}

func writeCopySourceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if loadErr, ok := err.(*copySourceLoadError); ok {
		status = loadErr.status
	}
	http.Error(w, err.Error(), status)
}

func cloneTablePartRowMap(source map[string][]map[string]any) map[string][]map[string]any {
	result := make(map[string][]map[string]any, len(source))
	for name, rows := range source {
		result[name] = cloneTablePartRows(rows)
	}
	return result
}

func cloneTablePartRows(source []map[string]any) []map[string]any {
	if source == nil {
		return nil
	}
	result := make([]map[string]any, len(source))
	for index, row := range source {
		copyRow := make(map[string]any, len(row))
		for name, value := range row {
			copyRow[name] = value
		}
		result[index] = copyRow
	}
	return result
}

// managedFormEditableEntityFields derives browser authority from server-owned
// form metadata. A forged input cannot turn an unplaced or readonly field into
// an editable one; at least one actual writable control must exist.
func managedFormEditableEntityFields(form *metadata.FormModule, entity *metadata.Entity) map[string]bool {
	result := make(map[string]bool)
	if form == nil || entity == nil {
		return result
	}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		if visit.effectiveReadOnly || visit.parentTablePart != nil || !managedElementPostsScalar(visit.element) {
			return
		}
		path := strings.TrimSpace(visit.element.DataPath)
		if path == "" || strings.Count(path, ".") > 1 {
			return
		}
		if field, ok := entityFieldByName(entity, dpFieldName(path)); ok {
			result[strings.ToLower(field.Name)] = true
		}
	})
	return result
}

func managedElementPostsScalar(element *metadata.FormElement) bool {
	if element == nil {
		return false
	}
	switch element.Kind {
	case metadata.FormElementField,
		metadata.FormElementCodeField,
		metadata.FormElementCheckbox,
		metadata.FormElementInputList,
		metadata.FormElementDatePicker,
		metadata.FormElementSwitch:
		return true
	default:
		return false
	}
}

// restoreManagedCopyState merges the canonical source snapshot into fields
// which the managed form cannot edit. Writable controls and writable table
// parts retain the validated browser payload; readonly/unplaced state ignores
// any forged payload and is reloaded from the authorized source.
func (s *Server) restoreManagedCopyState(
	w http.ResponseWriter,
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	fields map[string]any,
	objectFields map[string]any,
	tablePartRows map[string][]map[string]any,
) bool {
	sourceID := strings.TrimSpace(r.FormValue(copySourceFormField))
	if sourceID == "" {
		return false
	}
	snapshot, err := s.loadAuthorizedCopySource(r, entity, sourceID)
	if err != nil {
		writeCopySourceError(w, err)
		return true
	}

	editable := managedFormEditableEntityFields(form, entity)
	submitted := submittedFormKeys(r)
	checkboxes := checkboxOmittedFields(form, entity)
	decisions := s.fieldDecisions(r.Context(), entity)
	for _, field := range entity.Fields {
		key := strings.ToLower(field.Name)
		clientAuthoritative := editable[key] &&
			(formKeySubmitted(submitted, field.Name) || checkboxes[key])
		if clientAuthoritative {
			continue
		}

		if skipFieldOnCopy(entity, field) {
			// A readonly/unplaced system date still needs the new document's
			// current timestamp; it must not become nil merely because the
			// managed form does not submit it. Number is generated separately.
			if isSystemDocumentDate(entity, field) {
				now := time.Now()
				setCanonicalCopyField(r, fields, objectFields, field, now)
			} else {
				clearCanonicalCopyField(r, fields, objectFields, field)
			}
			continue
		}
		if decision, ok := fieldDecisionByName(decisions, field.Name); ok && decision.Masked() {
			clearCanonicalCopyField(r, fields, objectFields, field)
			continue
		}
		value, ok := maskCIKeyValue(snapshot.row, field.Name)
		if !ok {
			clearCanonicalCopyField(r, fields, objectFields, field)
			continue
		}
		setCanonicalCopyField(r, fields, objectFields, field, normalizeRestoredValue(field, value))
	}

	authorities, err := managedFormTableAuthorities(form, entity.TableParts, true)
	if err != nil {
		writeCopySourceError(w, &copySourceLoadError{
			status: http.StatusBadRequest, message: err.Error(),
		})
		return true
	}
	for _, tablePart := range entity.TableParts {
		if authorities[tablePart.Name].source != 0 {
			continue
		}
		for postedName := range tablePartRows {
			if strings.EqualFold(postedName, tablePart.Name) {
				delete(tablePartRows, postedName)
			}
		}
		tablePartRows[tablePart.Name] = cloneTablePartRows(snapshot.tablePartRows[tablePart.Name])
	}
	return false
}

func clearCanonicalCopyField(
	r *http.Request,
	fields map[string]any,
	objectFields map[string]any,
	field metadata.Field,
) {
	setCanonicalCopyField(r, fields, objectFields, field, nil)
}

func setCanonicalCopyField(
	r *http.Request,
	fields map[string]any,
	objectFields map[string]any,
	field metadata.Field,
	value any,
) {
	if key, ok := maskCIKey(fields, field.Name); ok {
		fields[key] = value
	} else {
		fields[field.Name] = value
	}
	if key, ok := maskCIKey(objectFields, field.Name); ok {
		objectFields[key] = value
	} else {
		objectFields[strings.ToLower(field.Name)] = value
	}
	// Error re-rendering reads r.Form. These values are written by the server
	// after authorization, so readonly controls show the canonical state while
	// unplaced fields remain absent from HTML.
	if r != nil {
		text := formatFieldValueForInput(field, value)
		r.Form.Set(field.Name, text)
		r.PostForm.Set(field.Name, text)
	}
}

func copySourceIDForRender(r *http.Request) string {
	if r == nil {
		return ""
	}
	if value := strings.TrimSpace(r.FormValue(copySourceFormField)); value != "" {
		return value
	}
	return strings.TrimSpace(r.URL.Query().Get("copy"))
}
