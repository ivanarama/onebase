package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
)

type tpContextReader func(string) (string, bool)

func addProcessorTPEventContext(
	r *http.Request,
	proc *processorpkg.Processor,
	controls processorRequestControls,
	target browserFormEventTarget,
	obj *runtime.Object,
	vars map[string]any,
) error {
	read := func(key string) (string, bool) {
		return processorPostFormText(r, processorServiceFieldName(proc.Params, key))
	}
	return addValidatedTPEventContext(read, controls.tableParts, proc.TableParts, target, obj, vars)
}

func addEntityTPEventContext(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	target browserFormEventTarget,
	obj *runtime.Object,
	vars map[string]any,
) error {
	read := func(key string) (string, bool) { return formPostText(r, key) }
	return addValidatedTPEventContext(read, editableFormTableParts(form, entity.TableParts), entity.TableParts, target, obj, vars)
}

// formPostText deliberately reads PostForm rather than FormValue. Query-string
// values are not browser form state and must never create a table-part context.
func formPostText(r *http.Request, key string) (string, bool) {
	if r == nil || r.PostForm == nil {
		return "", false
	}
	values, ok := r.PostForm[key]
	if !ok || len(values) == 0 {
		return "", ok
	}
	return values[0], true
}

// editableFormTableParts maps the posted DataPath name to canonical metadata
// only for table parts which are actually placed on the form and editable.
// Readonly ancestors make the complete subtree readonly as well.
func editableFormTableParts(form *metadata.FormModule, declared []metadata.TablePart) map[string]string {
	allowed := make(map[string]string)
	if form == nil {
		return allowed
	}
	var walk func([]*metadata.FormElement, bool)
	walk = func(elements []*metadata.FormElement, parentReadOnly bool) {
		for _, el := range elements {
			if el == nil {
				continue
			}
			readOnly := parentReadOnly || el.ReadOnly
			if el.Kind == metadata.FormElementTablePart {
				if !readOnly && el.DataPath != "" && strings.Count(el.DataPath, ".") <= 1 {
					formName := dpFieldName(el.DataPath)
					if tp := tablePartByName(declared, formName); tp != nil {
						allowed[formName] = tp.Name
					}
				}
				continue
			}
			walk(el.Children, readOnly)
		}
	}
	walk(form.Elements, false)
	return allowed
}

func addValidatedTPEventContext(
	read tpContextReader,
	allowed map[string]string,
	declared []metadata.TablePart,
	target browserFormEventTarget,
	obj *runtime.Object,
	vars map[string]any,
) error {
	tpRaw, tpPresent := read("_tp")
	selRaw, selPresent := read("_tp_selected")
	rowRaw, rowPresent := read("_tp_row")
	rowNumRaw, rowNumPresent := read("_tp_row_number")
	colRaw, colPresent := read("_tp_col")
	colIdxRaw, colIdxPresent := read("_tp_col_index")

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

	postedFormName, canonicalTPName, ok := canonicalAllowedTablePart(allowed, tpRaw)
	if !ok {
		return fmt.Errorf("табличная часть %q не отрисована или доступна только для чтения", strings.TrimSpace(tpRaw))
	}
	expectedFormName := dpFieldName(expectedElement.DataPath)
	_, expectedCanonical, expectedOK := canonicalAllowedTablePart(allowed, expectedFormName)
	if !expectedOK || !strings.EqualFold(expectedCanonical, canonicalTPName) {
		return fmt.Errorf("контекст табличной части %q не соответствует элементу %q", postedFormName, expectedElement.Name)
	}

	tpMeta := tablePartByName(declared, canonicalTPName)
	if tpMeta == nil {
		return fmt.Errorf("табличная часть %q не объявлена", canonicalTPName)
	}
	var rows []map[string]any
	if obj != nil {
		rows = obj.TablePartRows[canonicalTPName]
	}

	var selected *interpreter.Array
	if strings.TrimSpace(selRaw) != "" {
		items := make([]any, 0)
		for _, part := range strings.Split(selRaw, ",") {
			idx, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || idx < 0 || idx >= len(rows) {
				return fmt.Errorf("некорректный индекс выделенной строки табличной части")
			}
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

	columnName, columnIndex, hasColumn, err := canonicalTPColumn(tpMeta, colRaw, colPresent, colIdxRaw, colIdxPresent)
	if err != nil {
		return err
	}
	if hasColumn && !hasRow {
		return fmt.Errorf("колонка табличной части указана без строки")
	}

	setTPNameVars(vars, canonicalTPName)
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

func canonicalAllowedTablePart(allowed map[string]string, raw string) (string, string, bool) {
	want := strings.TrimSpace(raw)
	for formName, canonical := range allowed {
		if strings.EqualFold(formName, want) {
			return formName, canonical, true
		}
	}
	return "", "", false
}

func tablePartByName(tableParts []metadata.TablePart, name string) *metadata.TablePart {
	for i := range tableParts {
		if strings.EqualFold(tableParts[i].Name, name) {
			return &tableParts[i]
		}
	}
	return nil
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

func canonicalTPColumn(tp *metadata.TablePart, nameRaw string, namePresent bool, indexRaw string, indexPresent bool) (string, int, bool, error) {
	name := strings.TrimSpace(nameRaw)
	idx, hasIndex, err := parseTPContextInt(indexRaw, indexPresent, "индекс колонки")
	if err != nil {
		return "", 0, false, err
	}
	hasName := namePresent && name != ""
	nameIndex := -1
	canonicalName := ""
	if hasName {
		for i := range tp.Fields {
			if strings.EqualFold(tp.Fields[i].Name, name) {
				nameIndex = i
				canonicalName = tp.Fields[i].Name
				break
			}
		}
		if nameIndex < 0 {
			return "", 0, false, fmt.Errorf("колонка %q не объявлена в табличной части", name)
		}
	}
	if hasIndex {
		if idx < 0 || idx >= len(tp.Fields) {
			return "", 0, false, fmt.Errorf("индекс колонки табличной части вне диапазона")
		}
		if hasName && idx != nameIndex {
			return "", 0, false, fmt.Errorf("имя колонки табличной части не соответствует индексу")
		}
		if !hasName {
			canonicalName = tp.Fields[idx].Name
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
