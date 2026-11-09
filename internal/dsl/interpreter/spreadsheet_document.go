package interpreter

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/sheet"
)

// Этот файл — тонкая DSL-обвязка (ТабличныйДокумент/Область/Ячейка) над
// нейтральной моделью internal/sheet (план 64, этап 1). Все ДАННЫЕ документа
// и рендеры живут в sheet.Document/sheet.Area/sheet.Cell; здесь — только
// DSL-протокол: CallMethod/Get/Set, фабрика и R1C1-доступ.

// SpreadsheetDocumentCell — псевдоним модельной ячейки. Используется maket.go
// при материализации областей из макета (доступ к полям напрямую).
type SpreadsheetDocumentCell = sheet.Cell

// NewSpreadsheetDocumentCell создаёт ячейку с дефолтным форматированием.
func NewSpreadsheetDocumentCell(text string) *SpreadsheetDocumentCell {
	return sheet.NewCell(text)
}

// ─── SpreadsheetDocumentArea (ОбластьТабличногоДокумента) ─────────────────────

// SpreadsheetDocumentArea — DSL-обёртка над sheet.Area (прямоугольная область-
// шаблон с собственными ячейками в относительных координатах). Данные — в Area.
type SpreadsheetDocumentArea struct {
	document *SpreadsheetDocument
	Area     *sheet.Area
	// colWidths — ширины колонок макета (1-based px), из которого взята
	// область: Вывести переносит их в документ, как 1С переносит ширины
	// колонок макета.
	colWidths map[int]float64
}

func (a *SpreadsheetDocumentArea) CallMethod(name string, args []any) any {
	switch name {
	case "параметры", "parameters":
		return a.parameters(args)
	case "параметр", "parameter":
		if len(args) > 0 {
			return a.getParameter(strArg(args, 0))
		}
	case "установитьпараметр", "setparameter":
		if len(args) >= 2 {
			a.setParameter(strArg(args, 0), args[1])
		}
	case "ширина", "width":
		return float64(a.Area.Cols())
	case "высота", "height":
		return float64(a.Area.Rows())
	case "очистить", "clear":
		a.Area.Clear()
	case "объединить", "merge":
		a.Area.Merge()
	}
	return nil
}

// parameters returns an AreaParameters object for accessing area cells via dot notation.
func (a *SpreadsheetDocumentArea) parameters(args []any) any {
	return &AreaParameters{area: a}
}

// getParameter returns the value of a parameter by name (e.g., "R1C1") — relative to area.
func (a *SpreadsheetDocumentArea) getParameter(name string) any {
	row, col, ok := parseRC(name)
	if !ok {
		return ""
	}
	key := fmt.Sprintf("%d,%d", row-1, col-1)
	if cell, exists := a.Area.Cells[key]; exists {
		return cell.Text
	}
	return ""
}

// setParameter sets the value of a parameter (cell) by name — relative to area.
func (a *SpreadsheetDocumentArea) setParameter(name string, value any) {
	row, col, ok := parseRC(name)
	if !ok {
		return
	}
	cell := a.Area.GetOrCreateCell(row-1, col-1)
	cell.Text = strArg([]any{value}, 0)
	cell.Value = value
}

// Get allows accessing cells via dot notation (Area.R1C1) or properties (Area.Параметры).
func (a *SpreadsheetDocumentArea) Get(field string) any {
	switch strings.ToLower(field) {
	case "текст", "text":
		if cell, ok := a.Area.Cells["0,0"]; ok {
			return cell.Text
		}
		return ""
	case "параметры", "parameters":
		return &AreaParameters{area: a}
	}
	return a.getParameter(field)
}

// Set allows setting cells via dot notation (Area.R1C1 = "value") or area properties.
func (a *SpreadsheetDocumentArea) Set(field string, v any) {
	a.setProperty(field, v)
}

// setProperty handles both R1C1 parameters and named properties on all area cells.
func (a *SpreadsheetDocumentArea) setProperty(field string, v any) {
	rows := a.Area.Rows()
	cols := a.Area.Cols()
	switch strings.ToLower(field) {
	case "текст", "text":
		text := strArg([]any{v}, 0)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				cell := a.Area.GetOrCreateCell(r, c)
				cell.Text = text
				cell.Value = text
			}
		}
	case "шрифтжирный", "bold":
		bold := truthy(v)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).Bold = bold
			}
		}
	case "размершрифта", "fontsize":
		size := int(toFloatOr0(v))
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).FontSize = size
			}
		}
	case "курсив", "italic":
		italic := truthy(v)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).Italic = italic
			}
		}
	case "горизонтальноеположение", "horizontalalign", "halign":
		align := strings.ToLower(strArg([]any{v}, 0))
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).Align = align
			}
		}
	case "вертикальноеположение", "verticalalign", "valign":
		vAlign := strings.ToLower(strArg([]any{v}, 0))
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).Vertical = vAlign
			}
		}
	case "цветфона", "backcolor", "backgroundcolor":
		color := strArg([]any{v}, 0)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).BackColor = color
			}
		}
	case "цветтекста", "textcolor", "fontcolor":
		color := strArg([]any{v}, 0)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				a.Area.GetOrCreateCell(r, c).TextColor = color
			}
		}
	default:
		// Try R1C1 format
		a.setParameter(field, v)
	}
}

// ─── AreaParameters ────────────────────────────────────────────────────────────

// AreaParameters provides dot-notation access to area cell values (R1C1, R1C2, etc.)
// Used when DSL code accesses Обл.Параметры.R1C1 = "value".
type AreaParameters struct {
	area *SpreadsheetDocumentArea
}

func (p *AreaParameters) Get(field string) any {
	// First try named parameter (from макет)
	for _, cell := range p.area.Area.Cells {
		if cell.ParameterName != "" && strings.EqualFold(cell.ParameterName, field) {
			return cell.Text
		}
	}
	// Then try R1C1 format
	return p.area.getParameter(field)
}

func (p *AreaParameters) Set(field string, v any) {
	// First try named parameter (from макет)
	found := false
	for _, cell := range p.area.Area.Cells {
		if cell.ParameterName != "" && strings.EqualFold(cell.ParameterName, field) {
			cell.Text = strArg([]any{v}, 0)
			cell.Value = v
			found = true
		}
	}
	if found {
		return
	}
	// Then try R1C1 format
	p.area.setParameter(field, v)
}

func (p *AreaParameters) CallMethod(name string, args []any) any {
	return nil
}

// ─── parseRC helper ────────────────────────────────────────────────────────────

// parseRC parses "R<row>C<col>" format and returns 1-based row, col.
func parseRC(name string) (int, int, bool) {
	if !strings.HasPrefix(strings.ToUpper(name), "R") {
		return 0, 0, false
	}
	parts := strings.Split(strings.ToUpper(name), "C")
	if len(parts) != 2 {
		return 0, 0, false
	}
	row, err := strconv.Atoi(parts[0][1:])
	if err != nil {
		return 0, 0, false
	}
	col, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return row, col, true
}

// ─── SpreadsheetDocument (ТабличныйДокумент) ─────────────────────────────────

// SpreadsheetDocument — DSL-обёртка над sheet.Document. Данные и рендеры — в Doc;
// здесь — диспетчер DSL-методов и именованные области (DSL-уровень).
type SpreadsheetDocument struct {
	Doc        *sheet.Document
	namedAreas map[string]*SpreadsheetDocumentArea
}

// NewSpreadsheetDocument creates a new empty spreadsheet document.
func NewSpreadsheetDocument() *SpreadsheetDocument {
	return &SpreadsheetDocument{
		Doc:        sheet.NewDocument(),
		namedAreas: make(map[string]*SpreadsheetDocumentArea),
	}
}

// BackURL делегирует одноимённое поле модели (используется handlers_print.go).
func (d *SpreadsheetDocument) SetBackURL(url string) { d.Doc.BackURL = url }

// Get обеспечивает чтение свойств страницы из DSL (ТабДок.ОриентацияСтраницы).
// Метод также делает SpreadsheetDocument реализацией This — на диспетчер
// методов (CallMethod) это не влияет (методы идут через MethodCallable).
func (d *SpreadsheetDocument) Get(field string) any {
	switch strings.ToLower(field) {
	case "ориентациястраницы", "pageorientation", "orientation":
		return d.Doc.Page.Orientation
	case "размерстраницы", "pagesize", "format":
		return d.Doc.Page.Format
	}
	return nil
}

// Set обеспечивает запись свойств страницы из DSL (план 64, этап 2):
//   - ОриентацияСтраницы = "Портрет"/"Ландшафт" (и англ. Portrait/Landscape);
//   - РазмерСтраницы = "A4"/"A5"/"Letter" и т.п. либо кастомный "Ш×В" в мм
//     ("229x162mm") — литеральный размер бланка (конверты, ярлыки);
//   - ПоляПечати = число мм (все четыре поля) ЛИБО Массив [верх,низ,лево,право] мм.
func (d *SpreadsheetDocument) Set(field string, v any) {
	switch strings.ToLower(field) {
	case "ориентациястраницы", "pageorientation", "orientation":
		d.Doc.Page.Orientation = normalizeOrientation(strArg([]any{v}, 0))
	case "размерстраницы", "pagesize", "format":
		d.Doc.Page.Format = strings.TrimSpace(strArg([]any{v}, 0))
	case "поляпечати", "printmargins", "margins":
		d.setMargins(v)
	}
}

// normalizeOrientation приводит DSL-значение ориентации к "portrait"/"landscape".
func normalizeOrientation(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ландшафт", "альбомная", "landscape", "l":
		return "landscape"
	case "портрет", "книжная", "portrait", "p":
		return "portrait"
	default:
		return s
	}
}

// setMargins трактует ПоляПечати: число → все поля; Массив [в,н,л,п] → по сторонам.
func (d *SpreadsheetDocument) setMargins(v any) {
	if arr, ok := v.(*Array); ok {
		get := func(i int) float64 {
			if i < len(arr.items) {
				return toFloatOr0(arr.items[i])
			}
			return d.Doc.Page.MarginsMM.Top
		}
		d.Doc.Page.MarginsMM = sheet.Margins{
			Top:    get(0),
			Bottom: get(1),
			Left:   get(2),
			Right:  get(3),
		}
		return
	}
	m := toFloatOr0(v)
	d.Doc.Page.MarginsMM = sheet.Margins{Top: m, Bottom: m, Left: m, Right: m}
}

func (d *SpreadsheetDocument) CallMethod(name string, args []any) any {
	switch name {
	case "вывести", "put":
		return d.put(args)
	case "присоединить", "append":
		return d.append(args)
	case "область", "area":
		return d.area(args)
	case "получитьобласть", "getarea":
		if len(args) > 0 {
			return d.getNamedArea(strArg(args, 0))
		}
	case "показать", "show":
		d.show()
	case "записать", "write":
		if len(args) > 0 {
			return d.write(strArg(args, 0), args)
		}
	case "очистить", "clear":
		d.Doc.Clear()
	case "удалитьобласть", "removearea":
		if len(args) > 0 {
			d.removeArea(args[0])
		}
	case "разделительстраниц", "pagebreak":
		d.Doc.PageBreak()
	case "проверитьвывод", "checkoutput":
		return d.checkOutput(args)
	case "закончитьстраницу", "endpage":
		d.Doc.EndPage()
	case "повторитьприпечати", "repeatonprint":
		if len(args) >= 2 {
			d.repeatOnPrint(args[0], args[1])
		}
	case "нарисовать", "draw":
		if len(args) > 0 {
			d.draw(args[0])
		}
	case "получитьрисунок", "getpicture":
		if len(args) > 0 {
			return d.getPicture(args[0])
		}
	case "установитьимяобласти", "setareaname":
		if len(args) >= 2 {
			d.setAreaName(args[0], strArg(args, 1))
		}
	case "ширинаколонки", "columnwidth":
		if len(args) >= 2 {
			d.Doc.SetColumnWidth(int(floatArg(args, 0)), toFloatOr0(args[1]))
		}
	case "высотастроки", "rowheight":
		if len(args) >= 2 {
			d.Doc.SetRowHeight(int(floatArg(args, 0)), toFloatOr0(args[1]))
		}
	case "выровнять", "align":
		if len(args) >= 3 {
			d.setAlign(args[0], strArg(args, 1), strArg(args, 2))
		}
	case "объединить", "merge":
		if len(args) >= 4 {
			d.Doc.Merge(int(floatArg(args, 0)), int(floatArg(args, 1)),
				int(floatArg(args, 2)), int(floatArg(args, 3)))
		}
	case "ячейка", "cell":
		if len(args) >= 2 {
			return d.getCellObj(int(floatArg(args, 0)), int(floatArg(args, 1)))
		}
	case "ширина", "width":
		return float64(d.Doc.ColCount)
	case "высота", "height":
		return float64(d.Doc.RowCount)
	case "текущаястрока", "currentrow":
		return float64(d.Doc.CurrentRow + 1)
	case "текущаяколонка", "currentcol":
		return float64(d.Doc.CurrentCol + 1)
	}
	return nil
}

// put outputs an area at the current position and moves to the next line.
func (d *SpreadsheetDocument) put(args []any) any {
	if len(args) == 0 {
		return nil
	}
	area, ok := args[0].(*SpreadsheetDocumentArea)
	if !ok {
		return nil
	}
	// Ширины колонок макета — только незаданные: ШиринаКолонки модуля главнее.
	for col, px := range area.colWidths {
		if d.Doc.ColumnWidth(col) <= 0 {
			d.Doc.SetColumnWidth(col, px)
		}
	}
	d.Doc.Put(area.Area)
	return nil
}

// append appends an area to the right of the last output area.
func (d *SpreadsheetDocument) append(args []any) any {
	if len(args) == 0 {
		return nil
	}
	area, ok := args[0].(*SpreadsheetDocumentArea)
	if !ok {
		return nil
	}
	d.Doc.Append(area.Area)
	return nil
}

// area returns an area defined by coordinates (top, left, bottom, right) — 1-based.
func (d *SpreadsheetDocument) area(args []any) any {
	if len(args) < 4 {
		return nil
	}
	top := int(floatArg(args, 0)) - 1 // 1-based to 0-based
	left := int(floatArg(args, 1)) - 1
	bottom := int(floatArg(args, 2)) - 1
	right := int(floatArg(args, 3)) - 1

	return &SpreadsheetDocumentArea{
		document: d,
		Area:     sheet.NewArea(top, left, bottom, right),
	}
}

// getNamedArea returns a named area.
func (d *SpreadsheetDocument) getNamedArea(name string) *SpreadsheetDocumentArea {
	return d.namedAreas[strings.ToLower(name)]
}

// setAreaName assigns a name to an area.
func (d *SpreadsheetDocument) setAreaName(areaArg any, name string) {
	area, ok := areaArg.(*SpreadsheetDocumentArea)
	if ok {
		area.Area.Name = name
		d.namedAreas[strings.ToLower(name)] = area
	}
}

// show displays the document in a dialog (for now, just prints info).
func (d *SpreadsheetDocument) show() {
	d.Doc.ShowMode = true
}

// HTMLString returns the full HTML representation of the document.
func (d *SpreadsheetDocument) HTMLString() string {
	return d.Doc.HTMLString()
}

// write сериализует документ и ВОЗВРАЩАЕТ его содержимое (а не пишет на диск —
// сохранение делает вызывающий DSL-код через ЗаписатьТекстФайла и т.п.).
// Формат содержимого зависит от типа:
//   - "pdf"        → base64-строка PDF-байтов (как ВыгрузитьВExcel);
//   - "html"/""    → сырой HTML-текст;
//   - "txt"        → сырой табличный текст (колонки \t, строки \n);
//   - "xlsx"/"xls"/"xls97" → base64-строка книги Excel с оформлением макета.
func (d *SpreadsheetDocument) write(fileName string, args []any) any {
	d.Doc.FileName = fileName
	fileType := "html"
	if len(args) > 1 {
		fileType = strings.ToLower(strArg(args, 1))
	}

	switch fileType {
	case "html", "":
		return d.writeHTML(fileName)
	case "pdf":
		return d.writePDF(fileName)
	case "txt":
		return d.writeTXT(fileName)
	case "xlsx", "xls", "xls97":
		return d.writeXLSX(fileName)
	}
	return nil
}

// writeHTML exports the document as HTML.
func (d *SpreadsheetDocument) writeHTML(fileName string) any {
	html := d.Doc.HTMLString()
	fmt.Printf("// Запись файла: %s\n", fileName)
	return html
}

// writePDF экспортирует документ в PDF и возвращает base64-строку PDF-байтов
// (как ВыгрузитьВExcel) — план 64, этап 2. Кириллица рендерится встроенными
// PT-шрифтами без транслитерации. При ошибке НЕ молчит пустой строкой (иначе
// вызывающий код запишет пустой файл, не заметив сбоя), а возвращает текст
// ошибки — он виден в DSL-коде, присвоившем результат Записать.
func (d *SpreadsheetDocument) writePDF(fileName string) any {
	b, err := d.Doc.PDF(sheet.PDFOptions{Title: fileName})
	if err != nil {
		msg := fmt.Sprintf("Ошибка формирования PDF %q: %v", fileName, err)
		fmt.Printf("// %s\n", msg)
		return msg
	}
	return base64.StdEncoding.EncodeToString(b)
}

// writeXLSX выгружает документ в xlsx с оформлением макета (шрифт, рамки,
// заливка, выравнивание, объединения, ширины) и возвращает base64-строку, как
// writePDF: её берёт вложение письма с Base64 = Истина. «xls»/«xls97» —
// синонимы (ТипФайлаТабличногоДокумента.XLS97 в 1С): пишется современный xlsx.
func (d *SpreadsheetDocument) writeXLSX(fileName string) any {
	b, err := d.Doc.XLSX()
	if err != nil {
		msg := fmt.Sprintf("Ошибка формирования XLSX %q: %v", fileName, err)
		fmt.Printf("// %s\n", msg)
		return msg
	}
	return base64.StdEncoding.EncodeToString(b)
}

// writeTXT exports the document as plain text.
func (d *SpreadsheetDocument) writeTXT(fileName string) any {
	result := d.Doc.TextString()
	fmt.Printf("// Запись файла: %s\n", fileName)
	return result
}

// removeArea deletes the specified area content.
func (d *SpreadsheetDocument) removeArea(areaArg any) {
	area, ok := areaArg.(*SpreadsheetDocumentArea)
	if !ok {
		return
	}
	d.Doc.RemoveArea(area.Area)
}

// checkOutput checks if an area will fit on the current page.
func (d *SpreadsheetDocument) checkOutput(args []any) any {
	if len(args) == 0 {
		return true
	}
	area, ok := args[0].(*SpreadsheetDocumentArea)
	if !ok {
		return true
	}
	return d.Doc.CheckOutput(area.Area)
}

// repeatOnPrint sets an area to repeat on each page.
func (d *SpreadsheetDocument) repeatOnPrint(areaArg any, repeat any) {
	area, ok := areaArg.(*SpreadsheetDocumentArea)
	if ok {
		d.Doc.RepeatOnPrint(area.Area, truthy(repeat))
	}
}

// draw inserts a picture at the current position.
func (d *SpreadsheetDocument) draw(pictureArg any) {
	d.Doc.Draw(strArg([]any{pictureArg}, 0))
}

// getPicture extracts a picture from an area.
func (d *SpreadsheetDocument) getPicture(areaArg any) any {
	area, ok := areaArg.(*SpreadsheetDocumentArea)
	if !ok {
		return ""
	}
	return d.Doc.GetPicture(area.Area)
}

// setAlign sets alignment for an area.
func (d *SpreadsheetDocument) setAlign(areaArg any, hAlign, vAlign string) {
	area, ok := areaArg.(*SpreadsheetDocumentArea)
	if !ok {
		return
	}
	d.Doc.SetAlign(area.Area, hAlign, vAlign)
}

// getCellObj returns a cell object for direct manipulation.
func (d *SpreadsheetDocument) getCellObj(row, col int) any {
	row-- // 1-based to 0-based
	col--
	cell := d.Doc.GetOrCreateCell(row, col)
	return &SpreadsheetDocumentCellWrapper{cell: cell, doc: d, row: row, col: col}
}

// ─── SpreadsheetDocumentCellWrapper ───────────────────────────────────────────

// SpreadsheetDocumentCellWrapper provides direct access to a single cell.
type SpreadsheetDocumentCellWrapper struct {
	cell *SpreadsheetDocumentCell
	doc  *SpreadsheetDocument
	row  int
	col  int
}

func (w *SpreadsheetDocumentCellWrapper) Get(field string) any {
	switch strings.ToLower(field) {
	case "текст", "text":
		return w.cell.Text
	case "значение", "value":
		return w.cell.Value
	case "ширина", "width":
		return w.cell.Width
	case "высота", "height":
		return w.cell.Height
	case "выравнивание", "align":
		return w.cell.Align
	case "вервыравнивание", "valign":
		return w.cell.Vertical
	case "жирный", "bold":
		return w.cell.Bold
	case "курсив", "italic":
		return w.cell.Italic
	case "размершрифта", "fontsize":
		return float64(w.cell.FontSize)
	case "цветфона", "backcolor":
		return w.cell.BackColor
	case "цветтекста", "textcolor":
		return w.cell.TextColor
	case "рисунок", "picture":
		return w.cell.Picture
	}
	return nil
}

func (w *SpreadsheetDocumentCellWrapper) Set(field string, v any) {
	switch strings.ToLower(field) {
	case "текст", "text":
		w.cell.Text = strArg([]any{v}, 0)
	case "значение", "value":
		w.cell.Value = v
	case "ширина", "width":
		w.cell.Width = toFloatOr0(v)
	case "высота", "height":
		w.cell.Height = toFloatOr0(v)
	case "выравнивание", "align":
		w.cell.Align = strings.ToLower(strArg([]any{v}, 0))
	case "вервыравнивание", "valign":
		w.cell.Vertical = strings.ToLower(strArg([]any{v}, 0))
	case "жирный", "bold":
		w.cell.Bold = truthy(v)
	case "курсив", "italic":
		w.cell.Italic = truthy(v)
	case "размершрифта", "fontsize":
		w.cell.FontSize = int(toFloatOr0(v))
	case "цветфона", "backcolor":
		w.cell.BackColor = strArg([]any{v}, 0)
	case "цветтекста", "textcolor":
		w.cell.TextColor = strArg([]any{v}, 0)
	case "рисунок", "picture":
		w.cell.Picture = strArg([]any{v}, 0)
	}
}

func (w *SpreadsheetDocumentCellWrapper) CallMethod(name string, args []any) any {
	return nil
}

// ─── Factory function for Новый ТабличныйДокумент ─────────────────────────────

func newSpreadsheetDocument(args []any) any {
	return NewSpreadsheetDocument()
}

// NewSpreadsheetFunctions returns a map of spreadsheet-related functions and factories.
func NewSpreadsheetFunctions() map[string]any {
	return map[string]any{
		"__factory_ТабличныйДокумент":   newSpreadsheetDocument,
		"__factory_SpreadsheetDocument": newSpreadsheetDocument,
	}
}
