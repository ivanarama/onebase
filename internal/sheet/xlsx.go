package sheet

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"
)

// XLSX выгружает документ в книгу Excel (один лист) с оформлением макета:
// значения (числа и даты — значениями, не текстом), шрифт (имя, размер,
// жирный, курсив, цвет), заливка, выравнивание и перенос по словам, рамки
// (по сторонам и пресет), объединения, ширины колонок и высоты строк.
// Нужна вложениям писем «как в 1С по макету» (ТабДок.Записать(…, XLS97)).
//
// Единицы: ширины/высоты документа — CSS-пиксели (96 dpi), как у HTML/PDF;
// в Excel ширина колонки — в символах шрифта по умолчанию (≈7 px), высота
// строки — в пунктах (px × 0.75).
func (d *Document) XLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	const sheetName = "Лист1"
	if err := f.SetSheetName("Sheet1", sheetName); err != nil {
		return nil, err
	}
	maxRow, maxCol := d.ContentBounds()
	if len(d.Cells) == 0 {
		maxRow, maxCol = -1, -1
	}
	collapsed, outlineLevel, err := d.xlsxView(f, sheetName, maxRow)
	if err != nil {
		return nil, err
	}

	for col, px := range d.ColWidths {
		if px <= 0 || col < 1 {
			continue
		}
		name, err := excelize.ColumnNumberToName(col)
		if err != nil {
			continue
		}
		if err := f.SetColWidth(sheetName, name, name, pxToExcelWidth(px)); err != nil {
			return nil, err
		}
	}
	for row, px := range d.RowHeights {
		if px <= 0 || row < 1 {
			continue
		}
		if err := f.SetRowHeight(sheetName, row, px*0.75); err != nil {
			return nil, err
		}
	}

	styles := map[string]int{}
	styleFor := func(st *excelize.Style) (int, error) {
		custom := ""
		if st.CustomNumFmt != nil {
			custom = *st.CustomNumFmt
		}
		key := fmt.Sprintf("%+v|%+v|%+v|%+v|%v|%s", st.Font, st.Fill, st.Alignment, st.Border, st.NumFmt, custom)
		if id, ok := styles[key]; ok {
			return id, nil
		}
		id, err := f.NewStyle(st)
		if err != nil {
			return 0, err
		}
		styles[key] = id
		return id, nil
	}

	for row := 0; row <= maxRow; row++ {
		for col := 0; col <= maxCol; col++ {
			cell := d.GetCell(row, col)
			if cell == nil {
				continue
			}
			ref, err := excelize.CoordinatesToCellName(col+1, row+1)
			if err != nil {
				return nil, err
			}
			st, isDate := xlsxCellStyle(cell)
			if err := setXLSXValue(f, sheetName, ref, cell, isDate); err != nil {
				return nil, err
			}
			id, err := styleFor(st)
			if err != nil {
				return nil, err
			}
			// Объединение: стиль на весь диапазон, иначе Excel рисует рамку
			// только у левой верхней ячейки.
			last := ref
			if cell.ColSpan > 1 || cell.RowSpan > 1 {
				cs, rs := max(cell.ColSpan, 1), max(cell.RowSpan, 1)
				last, err = excelize.CoordinatesToCellName(col+cs, row+rs)
				if err != nil {
					return nil, err
				}
				if err := f.MergeCell(sheetName, ref, last); err != nil {
					return nil, err
				}
			}
			if err := f.SetCellStyle(sheetName, ref, last, id); err != nil {
				return nil, err
			}
			// Индивидуальная ширина/высота ячейки — на колонку/строку, если
			// колоночной/строчной не задано.
			if cell.Width > 0 && d.ColWidths[col+1] <= 0 && cell.ColSpan <= 1 {
				name, _ := excelize.ColumnNumberToName(col + 1)
				_ = f.SetColWidth(sheetName, name, name, pxToExcelWidth(cell.Width))
			}
			if cell.Height > 0 && d.RowHeights[row+1] <= 0 && cell.RowSpan <= 1 {
				_ = f.SetRowHeight(sheetName, row+1, cell.Height*0.75)
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	if outlineLevel > 0 {
		return xlsxOutlineXML(buf.Bytes(), collapsed, outlineLevel)
	}
	return buf.Bytes(), nil
}

// xlsxView keeps grouping as worksheet metadata; all cell values are retained.
func (d *Document) xlsxView(f *excelize.File, sh string, maxRow int) (map[int]bool, int, error) {
	grid := !d.HideGrid
	if err := f.SetSheetView(sh, 0, &excelize.ViewOptions{ShowGridLines: &grid}); err != nil {
		return nil, 0, err
	}
	rows, cols := max(d.FreezeRows, 0), max(d.FreezeCols, 0)
	if rows > 0 || cols > 0 {
		ref, err := excelize.CoordinatesToCellName(cols+1, rows+1)
		if err != nil {
			return nil, 0, err
		}
		pane := "bottomRight"
		if rows == 0 {
			pane = "topRight"
		}
		if cols == 0 {
			pane = "bottomLeft"
		}
		if err = f.SetPanes(sh, &excelize.Panes{Freeze: true, XSplit: cols, YSplit: rows, TopLeftCell: ref, ActivePane: pane}); err != nil {
			return nil, 0, err
		}
	}
	orientation := "portrait"
	if orientLandscape(d.Page.Orientation) {
		orientation = "landscape"
	}
	size := 9
	if n := map[string]int{"a3": 8, "a4": 9, "a5": 11, "a6": 70, "letter": 1, "legal": 5, "tabloid": 3}[strings.ToLower(strings.TrimSpace(d.Page.Format))]; n != 0 {
		size = n
	}
	layout := &excelize.PageLayoutOptions{Orientation: &orientation, Size: &size}
	if d.AutoScale {
		one, zero := 1, 0
		layout.FitToWidth, layout.FitToHeight = &one, &zero
		fit := true
		if err := f.SetSheetProps(sh, &excelize.SheetPropsOptions{FitToPage: &fit}); err != nil {
			return nil, 0, err
		}
	}
	if err := f.SetPageLayout(sh, layout); err != nil {
		return nil, 0, err
	}
	if d.RepeatHeader && d.HeaderArea != nil && d.HeaderArea.Rows() > 0 {
		ref := fmt.Sprintf("'%s'!$1:$%d", strings.ReplaceAll(sh, "'", "''"), d.HeaderArea.Rows())
		if err := f.SetDefinedName(&excelize.DefinedName{Name: "_xlnm.Print_Titles", Scope: sh, RefersTo: ref}); err != nil {
			return nil, 0, err
		}
	}
	collapsed := map[int]bool{}
	if maxRow < 0 || len(d.Groups) == 0 {
		return collapsed, 0, nil
	}
	below := false
	if err := f.SetSheetProps(sh, &excelize.SheetPropsOptions{OutlineSummaryBelow: &below}); err != nil {
		return nil, 0, err
	}
	levels := make([]int, maxRow+1)
	hidden := make([]bool, maxRow+1)
	maxLevel := 0
	for _, g := range d.ResolvedGroups() {
		header := max(g.HeaderEnd, g.Start)
		end := min(g.End, maxRow)
		if header < 0 || header >= end {
			continue
		}
		level := min(max(g.Level, 1), 7)
		maxLevel = max(maxLevel, level)
		for r := header + 1; r <= end; r++ {
			levels[r] = max(levels[r], level)
			hidden[r] = hidden[r] || !g.Expanded
		}
		if !g.Expanded {
			collapsed[header+1] = true
		}
	}
	for r, level := range levels {
		if level > 0 && level <= 7 {
			if err := f.SetRowOutlineLevel(sh, r+1, uint8(level)); err != nil {
				return nil, 0, err
			}
		}
		if level > 0 || collapsed[r+1] {
			if err := f.SetRowVisible(sh, r+1, !hidden[r]); err != nil {
				return nil, 0, err
			}
		}
	}
	return collapsed, maxLevel, nil
}

var xlsxRowTag = regexp.MustCompile(`<row\b[^>]*>`)
var xlsxRowNumber = regexp.MustCompile(`\br="([0-9]+)"`)
var xlsxSheetFormat = regexp.MustCompile(`<sheetFormatPr\b`)

// Excelize exposes row levels/visibility, but not the summary-row collapsed
// marker or outlineLevelRow. Complete those attributes in our own generated
// workbook; Copy preserves every other ZIP entry without recompression.
func xlsxOutlineXML(data []byte, collapsed map[int]bool, level int) ([]byte, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, entry := range z.File {
		if entry.Name != "xl/worksheets/sheet1.xml" {
			if err = w.Copy(entry); err != nil {
				return nil, err
			}
			continue
		}
		r, openErr := entry.Open()
		if openErr != nil {
			return nil, openErr
		}
		// G110: input is the workbook generated just above, never an upload.
		xml, readErr := io.ReadAll(r) //nolint:gosec
		closeErr := r.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		xml = xlsxRowTag.ReplaceAllFunc(xml, func(tag []byte) []byte {
			m := xlsxRowNumber.FindSubmatch(tag)
			if len(m) == 0 {
				return tag
			}
			row, _ := strconv.Atoi(string(m[1]))
			if !collapsed[row] {
				return tag
			}
			return bytes.Replace(tag, []byte("<row"), []byte(`<row collapsed="1"`), 1)
		})
		xml = xlsxSheetFormat.ReplaceAll(xml, []byte(fmt.Sprintf(`<sheetFormatPr outlineLevelRow="%d"`, level)))
		h := entry.FileHeader
		part, createErr := w.CreateHeader(&h)
		if createErr != nil {
			return nil, createErr
		}
		if _, err = part.Write(xml); err != nil {
			return nil, err
		}
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func pxToExcelWidth(px float64) float64 {
	w := (px - 5) / 7
	if w < 1 {
		w = 1
	}
	return w
}

// xlsxCellStyle собирает стиль ячейки; isDate — значение ячейки дата.
func xlsxCellStyle(cell *Cell) (*excelize.Style, bool) {
	st := &excelize.Style{
		Font: &excelize.Font{
			Family: strings.TrimSpace(cell.FontFamily),
			Size:   float64(cell.FontSize),
			Bold:   cell.Bold,
			Italic: cell.Italic,
		},
		Alignment: &excelize.Alignment{
			Horizontal: xlsxHAlign(cell.Align),
			Vertical:   xlsxVAlign(cell.Vertical),
			// Перенос по словам — как в HTML/PDF, где текст переносится в
			// границах колонки.
			WrapText: true,
		},
	}
	if st.Font.Size <= 0 {
		st.Font.Size = 10
	}
	if cell.Underline {
		st.Font.Underline = "single"
	}
	if c := xlsxColor(cell.TextColor); c != "" {
		st.Font.Color = c
	}
	if c := xlsxColor(cell.BackColor); c != "" {
		st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{c}}
	}
	st.Border = xlsxBorders(cell)
	isDate := false
	if t, ok := cell.Value.(time.Time); ok && !t.IsZero() {
		isDate = true
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
			st.NumFmt = 14 // дата
		} else {
			st.NumFmt = 22 // дата и время
		}
	}
	if format := cell.ExcelFormat(); strings.TrimSpace(format) != "" {
		st.NumFmt = 0
		st.CustomNumFmt = &format
	}
	return st, isDate
}

// setXLSXValue пишет значение: число и дату — значением, остальное — текстом
// ячейки (как его показывает документ).
func setXLSXValue(f *excelize.File, sheetName, ref string, cell *Cell, isDate bool) error {
	switch v := cell.Value.(type) {
	case float64:
		return f.SetCellValue(sheetName, ref, v)
	case int:
		return f.SetCellValue(sheetName, ref, v)
	case int64:
		return f.SetCellValue(sheetName, ref, v)
	case decimal.Decimal:
		return f.SetCellValue(sheetName, ref, v.InexactFloat64())
	case time.Time:
		if isDate {
			return f.SetCellValue(sheetName, ref, v)
		}
	}
	text := cell.DisplayText()
	if text == "" && cell.RichHTML != "" {
		text = stripTags(cell.RichHTML)
	}
	if text == "" {
		return nil
	}
	return f.SetCellStr(sheetName, ref, text)
}

var xlsxTagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n").Replace(s)
	s = xlsxTagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`).Replace(s)
	return strings.TrimSpace(s)
}

func xlsxHAlign(a string) string {
	switch strings.ToLower(a) {
	case "center", "центр":
		return "center"
	case "right", "право":
		return "right"
	case "justify":
		return "justify"
	case "left", "лево":
		return "left"
	}
	return ""
}

func xlsxVAlign(a string) string {
	switch strings.ToLower(a) {
	case "center", "middle", "центр":
		return "center"
	case "bottom", "низ":
		return "bottom"
	case "top", "верх":
		return "top"
	}
	return ""
}

// xlsxBorders — та же логика, что у PDF: рамки по сторонам главнее пресета;
// пресет all/thin — тонкая со всех сторон, thick — толстая, ""/none — без рамки.
func xlsxBorders(cell *Cell) []excelize.Border {
	side := func(kind, v string) (excelize.Border, bool) {
		style := 0
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "none":
			return excelize.Border{}, false
		case "medium":
			style = 2
		case "thick":
			style = 5
		case "hair":
			style = 7
		default:
			style = 1
		}
		return excelize.Border{Type: kind, Color: "000000", Style: style}, true
	}
	var out []excelize.Border
	if hasPerSideBorders(cell) {
		for _, s := range []struct{ kind, v string }{
			{"left", cell.BorderLeft}, {"top", cell.BorderTop}, {"right", cell.BorderRight}, {"bottom", cell.BorderBottom},
		} {
			if b, ok := side(s.kind, s.v); ok {
				out = append(out, b)
			}
		}
		return out
	}
	preset := strings.ToLower(strings.TrimSpace(cell.Border))
	if preset == "all" {
		preset = "thin"
	}
	for _, kind := range []string{"left", "top", "right", "bottom"} {
		if b, ok := side(kind, preset); ok {
			out = append(out, b)
		}
	}
	return out
}

var xlsxNamedColors = map[string]string{
	"black": "000000", "white": "FFFFFF", "red": "FF0000", "green": "008000", "blue": "0000FF",
	"yellow": "FFFF00", "gray": "808080", "grey": "808080", "silver": "C0C0C0", "orange": "FFA500",
	"navy": "000080", "lightgray": "D3D3D3", "lightgrey": "D3D3D3", "lightyellow": "FFFFE0",
	"lightblue": "ADD8E6", "lightgreen": "90EE90", "darkgray": "A9A9A9", "maroon": "800000",
}

var xlsxRGBRe = regexp.MustCompile(`^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})`)

// xlsxColor — цвет документа (#rgb, #rrggbb, rgb(), CSS-имя) в RRGGBB; "" —
// цвет не задан или не распознан.
func xlsxColor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) == 6 {
			if _, err := strconv.ParseUint(h, 16, 32); err == nil {
				return strings.ToUpper(h)
			}
		}
		return ""
	}
	if m := xlsxRGBRe.FindStringSubmatch(s); m != nil {
		var out strings.Builder
		for _, part := range m[1:4] {
			n, _ := strconv.Atoi(part)
			if n > 255 {
				n = 255
			}
			fmt.Fprintf(&out, "%02X", n)
		}
		return out.String()
	}
	return xlsxNamedColors[s]
}
