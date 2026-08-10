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

	postedFormName, canonicalTPName, ok := canonicalProcessorTablePart(controls, tpRaw)
	if !ok {
		return fmt.Errorf("табличная часть %q не отрисована или доступна только для чтения", strings.TrimSpace(tpRaw))
	}
	expectedFormName := dpFieldName(expectedElement.DataPath)
	_, expectedCanonical, expectedOK := canonicalProcessorTablePart(controls, expectedFormName)
	if !expectedOK || !strings.EqualFold(expectedCanonical, canonicalTPName) {
		return fmt.Errorf("контекст табличной части %q не соответствует элементу %q", postedFormName, expectedElement.Name)
	}

	tpMeta := processorTablePartByName(proc, canonicalTPName)
	if tpMeta == nil {
		return fmt.Errorf("табличная часть %q не объявлена", canonicalTPName)
	}
	rows := obj.TablePartRows[canonicalTPName]
	setTPNameVars(vars, canonicalTPName)

	if strings.TrimSpace(selRaw) != "" {
		items := make([]any, 0)
		for _, part := range strings.Split(selRaw, ",") {
			idx, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || idx < 0 || idx >= len(rows) {
				return fmt.Errorf("некорректный индекс выделенной строки табличной части")
			}
			items = append(items, &interpreter.MapThis{M: rows[idx]})
		}
		selected := interpreter.NewArray(items)
		vars["ВыделенныеСтроки"] = selected
		vars["SelectedRows"] = selected
	}

	rowIndex, hasRow, err := parseProcessorContextInt(rowRaw, rowPresent, "индекс строки")
	if err != nil {
		return err
	}
	rowNumber, hasRowNumber, err := parseProcessorContextInt(rowNumRaw, rowNumPresent, "номер строки")
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
		vars["ИндексСтроки"] = float64(rowIndex)
		vars["НомерСтроки"] = float64(rowNumber)
		vars["RowIndex"] = float64(rowIndex)
		vars["RowNumber"] = float64(rowNumber)
		row := &interpreter.MapThis{M: rows[rowIndex]}
		vars["ТекущаяСтрока"] = row
		vars["CurrentRow"] = row
	}

	columnName, columnIndex, hasColumn, err := canonicalProcessorColumn(tpMeta, colRaw, colPresent, colIdxRaw, colIdxPresent)
	if err != nil {
		return err
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

func canonicalProcessorTablePart(controls processorRequestControls, raw string) (string, string, bool) {
	want := strings.TrimSpace(raw)
	for formName, canonical := range controls.tableParts {
		if strings.EqualFold(formName, want) {
			return formName, canonical, true
		}
	}
	return "", "", false
}

func processorTablePartByName(proc *processorpkg.Processor, name string) *metadata.TablePart {
	for i := range proc.TableParts {
		if strings.EqualFold(proc.TableParts[i].Name, name) {
			return &proc.TableParts[i]
		}
	}
	return nil
}

func parseProcessorContextInt(raw string, present bool, label string) (int, bool, error) {
	if !present || strings.TrimSpace(raw) == "" {
		return 0, false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false, fmt.Errorf("некорректный %s", label)
	}
	return n, true, nil
}

func canonicalProcessorColumn(tp *metadata.TablePart, nameRaw string, namePresent bool, indexRaw string, indexPresent bool) (string, int, bool, error) {
	name := strings.TrimSpace(nameRaw)
	idx, hasIndex, err := parseProcessorContextInt(indexRaw, indexPresent, "индекс колонки")
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
