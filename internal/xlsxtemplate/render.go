// Package xlsxtemplate заполняет теги {{...}} прямо в исходной книге Excel.
// В отличие от HTML/PDF-макета, этот путь не пересобирает лист и поэтому
// сохраняет стили, объединения, размеры, параметры печати и остальные листы.
package xlsxtemplate

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/xuri/excelize/v2"
)

var tagRE = regexp.MustCompile(`\{\{([^}]+)\}\}`)

type repeatRow struct {
	row       int
	tablePart string
	values    []string
}

// RenderBytes возвращает копию XLSX-шаблона с подставленными значениями.
// Строка, содержащая тег {{ТабличнаяЧасть.Поле}}, повторяется по числу строк
// табличной части. Ссылки Excel в формулах и диаграммах excelize корректирует
// лишь частично — это ограничение операции дублирования строк библиотеки.
func RenderBytes(template []byte, ctx *printform.RenderContext) ([]byte, error) {
	if len(template) == 0 {
		return nil, fmt.Errorf("xlsx template is empty")
	}
	f, err := excelize.OpenReader(bytes.NewReader(template))
	if err != nil {
		return nil, fmt.Errorf("open xlsx template: %w", err)
	}
	defer func() { _ = f.Close() }()

	for _, sheet := range f.GetSheetList() {
		if err := renderSheet(f, sheet, ctx); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if err := f.Write(&out); err != nil {
		return nil, fmt.Errorf("write xlsx template: %w", err)
	}
	return out.Bytes(), nil
}

func renderSheet(f *excelize.File, sheet string, ctx *printform.RenderContext) error {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	repeats, err := findRepeatRows(rows, ctx)
	if err != nil {
		return fmt.Errorf("sheet %q: %w", sheet, err)
	}
	sort.Slice(repeats, func(i, j int) bool { return repeats[i].row > repeats[j].row })
	for _, spec := range repeats {
		dataRows := tablePartRows(ctx, spec.tablePart)
		for i := 1; i < len(dataRows); i++ {
			if err := f.DuplicateRowTo(sheet, spec.row, spec.row+i); err != nil {
				return fmt.Errorf("repeat row %d: %w", spec.row, err)
			}
		}
		if len(dataRows) == 0 {
			dataRows = []map[string]any{nil}
		}
		for i, row := range dataRows {
			if err := renderValues(f, sheet, spec.row+i, spec.values, ctx, row, i+1, spec.tablePart); err != nil {
				return err
			}
		}
	}

	// После вставки строк координаты остальных ячеек могли сдвинуться, поэтому
	// перечитываем лист. Уже заполненные repeat-строки тегов больше не содержат.
	rows, err = f.GetRows(sheet)
	if err != nil {
		return fmt.Errorf("reread sheet %q: %w", sheet, err)
	}
	for i, values := range rows {
		if err := renderValues(f, sheet, i+1, values, ctx, nil, 0, ""); err != nil {
			return err
		}
	}
	return nil
}

func findRepeatRows(rows [][]string, ctx *printform.RenderContext) ([]repeatRow, error) {
	var result []repeatRow
	for i, values := range rows {
		found := ""
		for _, value := range values {
			for _, match := range tagRE.FindAllStringSubmatch(value, -1) {
				expr := strings.TrimSpace(strings.SplitN(match[1], "|", 2)[0])
				prefix, _, ok := strings.Cut(expr, ".")
				if !ok || tablePartRows(ctx, prefix) == nil {
					continue
				}
				if found != "" && !strings.EqualFold(found, prefix) {
					return nil, fmt.Errorf("row %d contains tags from several table parts", i+1)
				}
				found = prefix
			}
		}
		if found != "" {
			result = append(result, repeatRow{row: i + 1, tablePart: found, values: append([]string(nil), values...)})
		}
	}
	return result, nil
}

func renderValues(f *excelize.File, sheet string, rowNum int, values []string, ctx *printform.RenderContext, row map[string]any, itemNum int, tablePart string) error {
	for col, value := range values {
		if !tagRE.MatchString(value) {
			continue
		}
		axis, err := excelize.CoordinatesToCellName(col+1, rowNum)
		if err != nil {
			return err
		}
		matches := tagRE.FindAllStringSubmatchIndex(value, -1)
		if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(value) {
			inner := value[matches[0][2]:matches[0][3]]
			expr, format := splitFormat(inner)
			expr = stripTablePart(expr, tablePart)
			if format == "" {
				if err := f.SetCellValue(sheet, axis, printform.ResolveExpr(expr, ctx, row, itemNum)); err != nil {
					return err
				}
				continue
			}
		}
		rendered := tagRE.ReplaceAllStringFunc(value, func(tag string) string {
			inner := tag[2 : len(tag)-2]
			expr, format := splitFormat(inner)
			expr = stripTablePart(expr, tablePart)
			if format == "" {
				return printform.ResolveValue(expr, ctx, row, itemNum)
			}
			return printform.ResolveValue(expr+" | "+format, ctx, row, itemNum)
		})
		if err := f.SetCellStr(sheet, axis, rendered); err != nil {
			return err
		}
	}
	return nil
}

func splitFormat(value string) (expr, format string) {
	parts := strings.SplitN(value, "|", 2)
	expr = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		format = strings.TrimSpace(parts[1])
	}
	return expr, format
}

func stripTablePart(expr, tablePart string) string {
	if tablePart == "" || len(expr) <= len(tablePart) || expr[len(tablePart)] != '.' || !strings.EqualFold(expr[:len(tablePart)], tablePart) {
		return expr
	}
	return expr[len(tablePart)+1:]
}

// nil означает «табличной части с таким именем нет», пустой ненулевой slice —
// она есть, но строк в записи нет. Это различие важно при поиске repeat-тегов.
func tablePartRows(ctx *printform.RenderContext, name string) []map[string]any {
	if ctx == nil || ctx.TableParts == nil {
		return nil
	}
	if rows, ok := ctx.TableParts[name]; ok {
		if rows == nil {
			return []map[string]any{}
		}
		return rows
	}
	for key, rows := range ctx.TableParts {
		if strings.EqualFold(key, name) {
			if rows == nil {
				return []map[string]any{}
			}
			return rows
		}
	}
	return nil
}
