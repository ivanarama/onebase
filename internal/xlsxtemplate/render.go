// Package xlsxtemplate заполняет теги {{...}} прямо в исходной книге Excel.
// В отличие от HTML/PDF-макета, этот путь не пересобирает лист и поэтому
// сохраняет стили, объединения, размеры, параметры печати и остальные листы.
package xlsxtemplate

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

var tagRE = regexp.MustCompile(`\{\{([^}]+)\}\}`)

type repeatRow struct {
	row       int
	tablePart string
	values    []string
}

// unzipLimit — потолок распаковки для excelize (защита от zip-бомбы):
// шаблон печатной формы может прийти из недоверенного источника.
const unzipLimit = 64 << 20

// RenderBytes возвращает копию XLSX-шаблона с подставленными значениями.
// Строка, содержащая тег {{ТабличнаяЧасть.Поле}}, повторяется по числу строк
// табличной части. Ссылки Excel в формулах и диаграммах excelize корректирует
// лишь частично — это ограничение операции дублирования строк библиотеки.
func RenderBytes(template []byte, ctx *printform.RenderContext) (res []byte, err error) {
	if len(template) == 0 {
		return nil, fmt.Errorf("xlsx template is empty")
	}
	// excelize обычно возвращает ошибку на битом вводе, но паника у неё на
	// границе разбора (например, отрицательный индекс shared-строк) уронила бы
	// весь сервер — страховка на границе пакета обязательна.
	defer func() {
		if rec := recover(); rec != nil {
			res, err = nil, fmt.Errorf("xlsx template render panic: %v", rec)
		}
	}()
	f, oerr := excelize.OpenReader(bytes.NewReader(template), excelize.Options{
		UnzipSizeLimit:    unzipLimit,
		UnzipXMLSizeLimit: unzipLimit,
	})
	if oerr != nil {
		return nil, fmt.Errorf("open xlsx template: %w", oerr)
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
			value := printform.ResolveExpr(expr, ctx, row, itemNum)
			if written, err := setNumberCell(f, sheet, axis, value, format); err != nil {
				return err
			} else if written {
				continue
			}
			if format == "" {
				if err := f.SetCellValue(sheet, axis, value); err != nil {
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

// setNumberCell пишет числовое значение тега числом, а не текстом: иначе Excel
// помечает ячейку «число сохранено как текст» и не считает по ней формулы.
// Числовые поля приходят из хранилища как decimal.Decimal, который excelize не
// знает и записывает строкой, а форматы number:N и currency раньше всегда давали
// текст. Строковые значения не трогаем — ИНН или артикул с ведущими нулями
// должны остаться как есть. false означает «не число, пишите обычным путём».
func setNumberCell(f *excelize.File, sheet, axis string, value any, format string) (bool, error) {
	number, ok := numericValue(value)
	if !ok {
		return false, nil
	}
	decimals, ok := numberFormatDecimals(format)
	if !ok {
		return false, nil
	}
	if decimals < 0 {
		return true, f.SetCellFloat(sheet, axis, number, -1, 64)
	}
	// Как и DecimalPlaces в excelize, ограничиваем точность до 30 знаков.
	// Проверяем до форматирования и strings.Repeat: тег может прийти из чужого шаблона.
	if decimals > 30 {
		decimals = 30
	}
	// Используем то же округление, что и теги внутри текста. Иначе одна
	// сумма может отличаться на копейку в зависимости от окружения тега.
	rounded := printform.ApplyFormat(value, "number:"+strconv.Itoa(decimals))
	number, err := strconv.ParseFloat(rounded, 64)
	if err != nil {
		return true, fmt.Errorf("parse formatted number for %s: %w", axis, err)
	}
	if err := f.SetCellFloat(sheet, axis, number, -1, 64); err != nil {
		return true, err
	}
	return true, applyNumberFormat(f, sheet, axis, decimals)
}

// numberFormatDecimals: -1 — формат не задан, знаки не ограничиваем;
// ok=false — формат не числовой (date, upper, default:…), значение уходит текстом.
func numberFormatDecimals(format string) (int, bool) {
	name, arg, _ := strings.Cut(format, ":")
	switch strings.TrimSpace(name) {
	case "":
		return -1, true
	case "number":
		n, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil || n < 0 {
			n = 0
		}
		return n, true
	case "currency":
		return 2, true
	}
	return 0, false
}

func numericValue(value any) (float64, bool) {
	switch v := value.(type) {
	case decimal.Decimal:
		return v.InexactFloat64(), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	}
	return 0, false
}

// applyNumberFormat задаёт ячейке формат с нужным числом знаков, но только если
// автор бланка не задал свой: формат из шаблона (разделители, «руб.») главнее.
func applyNumberFormat(f *excelize.File, sheet, axis string, decimals int) error {
	styleID, err := f.GetCellStyle(sheet, axis)
	if err != nil {
		return err
	}
	style, err := f.GetStyle(styleID)
	if err != nil {
		return err
	}
	if style.NumFmt != 0 || (style.CustomNumFmt != nil && *style.CustomNumFmt != "") {
		return nil
	}
	code := "0"
	if decimals > 0 {
		code += "." + strings.Repeat("0", decimals)
	}
	style.CustomNumFmt = &code
	newID, err := f.NewStyle(style)
	if err != nil {
		return err
	}
	return f.SetCellStyle(sheet, axis, axis, newID)
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
