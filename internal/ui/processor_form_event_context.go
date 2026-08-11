package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
)

type tpContextReader func(string) ([]string, bool)

func addProcessorTPEventContext(
	r *http.Request,
	proc *processorpkg.Processor,
	controls processorRequestControls,
	target browserFormEventTarget,
	obj *runtime.Object,
	vars map[string]any,
) error {
	read := func(key string) ([]string, bool) {
		return processorPostFormValues(r, processorServiceFieldName(proc.Params, key))
	}
	return addValidatedTPEventContext(read, controls.formTables, target, obj, vars)
}

func addEntityTPEventContext(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	target browserFormEventTarget,
	obj *runtime.Object,
	vars map[string]any,
) error {
	read := func(key string) ([]string, bool) { return formPostValues(r, key) }
	allowed, err := editableFormTables(form, entity.TableParts)
	if err != nil {
		return err
	}
	return addValidatedTPEventContext(read, allowed, target, obj, vars)
}

// formPostText deliberately reads PostForm rather than FormValue. Query-string
// values are not browser form state and must never create a table-part context.
func formPostText(r *http.Request, key string) (string, bool) {
	values, ok := formPostValues(r, key)
	if !ok || len(values) == 0 {
		return "", ok
	}
	return values[0], true
}

func formPostValues(r *http.Request, key string) ([]string, bool) {
	if r == nil || r.PostForm == nil {
		return nil, false
	}
	values, ok := r.PostForm[key]
	return values, ok
}

func postFormOnlyRequest(r *http.Request) *http.Request {
	if r == nil {
		return nil
	}
	body := make(url.Values, len(r.PostForm))
	for key, values := range r.PostForm {
		body[key] = append([]string(nil), values...)
	}
	copy := new(http.Request)
	*copy = *r
	copy.Form = body
	copy.PostForm = body
	return copy
}

// editableFormTables maps a rendered DataPath to the shared canonical
// metadata for entity/processor table parts and form-local ValueTables.
// Readonly ancestors make the complete subtree readonly as well.
func editableFormTables(form *metadata.FormModule, declared []metadata.TablePart) (map[string]metadata.FormTableDefinition, error) {
	definitions, err := metadata.FormTableDefinitions(form, declared)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]metadata.FormTableDefinition)
	if form == nil {
		return allowed, nil
	}
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		el := visit.element
		if el.Kind != metadata.FormElementTablePart || visit.effectiveReadOnly || el.DataPath == "" || strings.Count(el.DataPath, ".") > 1 {
			return
		}
		formName := dpFieldName(el.DataPath)
		for _, definition := range definitions {
			if strings.EqualFold(definition.Name, formName) {
				allowed[formName] = definition
				break
			}
		}
	})
	return allowed, nil
}

func addValidatedTPEventContext(
	read tpContextReader,
	allowed map[string]metadata.FormTableDefinition,
	target browserFormEventTarget,
	obj *runtime.Object,
	vars map[string]any,
) error {
	tpRaw, tpPresent, err := readSingleTPContext(read, "_tp")
	if err != nil {
		return err
	}
	selRaw, selPresent, err := readSingleTPContext(read, "_tp_selected")
	if err != nil {
		return err
	}
	rowRaw, rowPresent, err := readSingleTPContext(read, "_tp_row")
	if err != nil {
		return err
	}
	rowNumRaw, rowNumPresent, err := readSingleTPContext(read, "_tp_row_number")
	if err != nil {
		return err
	}
	colRaw, colPresent, err := readSingleTPContext(read, "_tp_col")
	if err != nil {
		return err
	}
	colIdxRaw, colIdxPresent, err := readSingleTPContext(read, "_tp_col_index")
	if err != nil {
		return err
	}

	expectedElement := target.parentTablePart
	if expectedElement == nil && target.element != nil && target.element.Kind == metadata.FormElementTablePart {
		expectedElement = target.element
	}
	hasContext := tpPresent || selPresent || rowPresent || rowNumPresent || colPresent || colIdxPresent
	if expectedElement == nil {
		if hasContext {
			return fmt.Errorf("контекст табличной части недопустим для этого элемента")
		}
		return nil
	}
	if !tpPresent || strings.TrimSpace(tpRaw) == "" {
		return fmt.Errorf("не указан контекст табличной части")
	}

	postedFormName, tableDefinition, ok := canonicalAllowedFormTable(allowed, tpRaw)
	if !ok {
		return fmt.Errorf("табличная часть %q не отрисована или доступна только для чтения", strings.TrimSpace(tpRaw))
	}
	expectedFormName := dpFieldName(expectedElement.DataPath)
	_, expectedDefinition, expectedOK := canonicalAllowedFormTable(allowed, expectedFormName)
	if !expectedOK || !strings.EqualFold(expectedDefinition.Name, tableDefinition.Name) {
		return fmt.Errorf("контекст табличной части %q не соответствует элементу %q", postedFormName, expectedElement.Name)
	}
	var rows []map[string]any
	if obj != nil {
		rows = obj.TablePartRows[tableDefinition.Name]
	}

	var selected *interpreter.Array
	if strings.TrimSpace(selRaw) != "" {
		items := make([]any, 0)
		seen := make(map[int]struct{})
		for _, part := range strings.Split(selRaw, ",") {
			idx, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || idx < 0 || idx >= len(rows) {
				return fmt.Errorf("некорректный индекс выделенной строки табличной части")
			}
			if _, duplicate := seen[idx]; duplicate {
				return fmt.Errorf("индекс выделенной строки табличной части %d указан повторно", idx)
			}
			seen[idx] = struct{}{}
			items = append(items, &interpreter.MapThis{M: rows[idx]})
		}
		selected = interpreter.NewArray(items)
	}

	rowIndex, hasRow, err := parseTPContextInt(rowRaw, rowPresent, "индекс строки")
	if err != nil {
		return err
	}
	rowNumber, hasRowNumber, err := parseTPContextInt(rowNumRaw, rowNumPresent, "номер строки")
	if err != nil {
		return err
	}
	if !hasRow && hasRowNumber {
		rowIndex, hasRow = rowNumber-1, true
	}
	if hasRow {
		if rowIndex < 0 || rowIndex >= len(rows) {
			return fmt.Errorf("индекс строки табличной части вне диапазона")
		}
		if hasRowNumber && rowNumber != rowIndex+1 {
			return fmt.Errorf("номер строки табличной части не соответствует индексу")
		}
		rowNumber = rowIndex + 1
	}

	columnName, columnIndex, hasColumn, err := canonicalTPColumn(tableDefinition.Columns, colRaw, colPresent, colIdxRaw, colIdxPresent)
	if err != nil {
		return err
	}
	if hasColumn && !hasRow {
		return fmt.Errorf("колонка табличной части указана без строки")
	}

	setTPNameVars(vars, tableDefinition.Name)
	if selected != nil {
		vars["ВыделенныеСтроки"] = selected
		vars["SelectedRows"] = selected
	}
	if hasRow {
		vars["ИндексСтроки"] = float64(rowIndex)
		vars["НомерСтроки"] = float64(rowNumber)
		vars["RowIndex"] = float64(rowIndex)
		vars["RowNumber"] = float64(rowNumber)
		row := &interpreter.MapThis{M: rows[rowIndex]}
		vars["ТекущаяСтрока"] = row
		vars["CurrentRow"] = row
	}
	if hasColumn {
		vars["ТекущаяКолонка"] = columnName
		vars["ИмяКолонки"] = columnName
		vars["CurrentColumn"] = columnName
		vars["ColumnName"] = columnName
		vars["ИндексКолонки"] = float64(columnIndex)
		vars["ColumnIndex"] = float64(columnIndex)
	}
	return nil
}

func readSingleTPContext(read tpContextReader, key string) (string, bool, error) {
	values, present := read(key)
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", true, fmt.Errorf("служебное поле контекста %s указано неоднозначно", key)
	}
	return values[0], true, nil
}

func canonicalAllowedFormTable(allowed map[string]metadata.FormTableDefinition, raw string) (string, metadata.FormTableDefinition, bool) {
	want := strings.TrimSpace(raw)
	for formName, definition := range allowed {
		if strings.EqualFold(formName, want) {
			return formName, definition, true
		}
	}
	return "", metadata.FormTableDefinition{}, false
}

func parseTPContextInt(raw string, present bool, label string) (int, bool, error) {
	if !present || strings.TrimSpace(raw) == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false, fmt.Errorf("некорректный %s", label)
	}
	return n, true, nil
}

func canonicalTPColumn(columns []string, nameRaw string, namePresent bool, indexRaw string, indexPresent bool) (string, int, bool, error) {
	name := strings.TrimSpace(nameRaw)
	idx, hasIndex, err := parseTPContextInt(indexRaw, indexPresent, "индекс колонки")
	if err != nil {
		return "", 0, false, err
	}
	hasName := namePresent && name != ""
	nameIndex := -1
	canonicalName := ""
	if hasName {
		for i := range columns {
			if strings.EqualFold(columns[i], name) {
				nameIndex = i
				canonicalName = columns[i]
				break
			}
		}
		if nameIndex < 0 {
			return "", 0, false, fmt.Errorf("колонка %q не объявлена в табличной части", name)
		}
	}
	if hasIndex {
		if idx < 0 || idx >= len(columns) {
			return "", 0, false, fmt.Errorf("индекс колонки табличной части вне диапазона")
		}
		if hasName && idx != nameIndex {
			return "", 0, false, fmt.Errorf("имя колонки табличной части не соответствует индексу")
		}
		if !hasName {
			canonicalName = columns[idx]
		}
		return canonicalName, idx, true, nil
	}
	if hasName {
		return canonicalName, nameIndex, true, nil
	}
	return "", 0, false, nil
}

func setTPNameVars(vars map[string]any, name string) {
	vars["ИмяТабличнойЧасти"] = name
	vars["ТекущаяТабличнаяЧасть"] = name
	vars["TablePartName"] = name
}
