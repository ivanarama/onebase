// Package sheet — нейтральная модель табличного документа (ячейки, спаны,
// страницы) и его рендеры. Не зависит ни от DSL-интерпретатора, ни от
// printform — здесь сходятся все пути формирования печатных форм (план 64).
//
// Пакет импортирует только stdlib: цикл импортов interpreter↔printform решается
// тем, что и interpreter (тонкая DSL-обвязка), и будущий декларативный движок
// строят именно sheet.Document, а рендереры (HTML здесь, PDF — этап 2) живут
// рядом с моделью.
package sheet

import (
	"fmt"
	"strings"
)

// Cell — одна ячейка табличного документа со стилями и спанами.
// Поля экспортированы: interpreter-обёртки (Ячейка/Область/Параметры)
// читают и пишут их напрямую, сохраняя прежний DSL-контракт.
//
// ColSpan/RowSpan: значения 0 и 1 эквивалентны (нет объединения); рендереры
// обязаны трактовать любое значение <1 как 1.
//
// Width/Height — индивидуальные размеры ячейки (DSL Ячейка.Ширина/Высота).
// Колоночные/строчные размеры (ШиринаКолонки/ВысотаСтроки) живут на Document
// в ColWidths/RowHeights и при рендере накладываются на колонку/строку целиком.
type Cell struct {
	Format    string
	Drilldown *Reference
	// Builder data stays server-side. Only the validated neutral tree is cached.
	DrilldownSource  any             `json:"-"`
	DrilldownPayload *DrilldownValue `json:"-"`
	Text             string
	Value            any
	Align            string // left, center, right, justify
	Vertical         string // top, center, bottom
	Width            float64
	Height           float64
	Bold             bool
	Italic           bool
	Underline        bool
	FontSize         int
	FontFamily       string
	BackColor        string
	TextColor        string
	// Border — legacy-пресет рамки ("all"/""/"thin"/"thick"/"none"). Если задана
	// хотя бы одна из per-side границ (BorderLeft/Top/Right/Bottom), она имеет
	// приоритет над этим пресетом при рендере (html.go/pdf.go).
	Border string
	// BorderLeft/Top/Right/Bottom — per-side рамки ("" = не задана, иначе
	// none/thin/medium/thick). Заполняет декларативный движок из LayoutCell.Borders.
	BorderLeft   string
	BorderTop    string
	BorderRight  string
	BorderBottom string
	Picture      string
	ColSpan      int
	RowSpan      int
	// ParameterName — для макета: имя именованного параметра, заполняющего ячейку.
	ParameterName string
	// RichHTML — санитизированный HTML-контент ячейки (поле типа richtext,
	// план 65, этап 3). КОНТРАКТ: значение ДОЛЖНО быть предварительно
	// санитизировано вызывающим (printform прогоняет его через richtext.Sanitize
	// при сборке ячейки) — пакет sheet НЕ зависит от санитайзера (bluemonday) и
	// выводит RichHTML как доверенный HTML БЕЗ экранирования. Источник обязан
	// быть доверенным/санитизированным.
	//
	// Рендер: html.go выводит RichHTML как HTML-блок внутри <td> (вместо Text);
	// pdf.go строит из ограниченного HTML текстово-картиночную проекцию (fpdf не
	// рендерит произвольный HTML). Пустой RichHTML → ячейка ведёт себя как
	// раньше (Text), бит-в-бит — golden не затрагивается.
	RichHTML string
}

// CellKey — типизированный ключ карты ячеек документа (0-based индексы строки
// и колонки). Заменяет прежние строковые ключи "row,col": убирает Sprintf при
// записи и Sscanf при чтении границ. На порядок HTML-рендера не влияет — обход
// идёт по индексам строк/колонок, а не по карте.
type CellKey struct {
	Row, Col int
}

// NewCell создаёт ячейку с дефолтным форматированием (как в 1С:ТабличныйДокумент).
func NewCell(text string) *Cell {
	return &Cell{
		Text:       text,
		Value:      text,
		Align:      "left",
		Vertical:   "top",
		FontSize:   10,
		FontFamily: "Times New Roman",
		Border:     "all",
	}
}

// Area — прямоугольная область-шаблон с собственным хранилищем ячеек в
// относительных координатах (R1C1 = левый-верхний угол). Параметры
// устанавливаются относительно области и копируются в документ при Вывести/
// Присоединить. Это чистая модель-вью; DSL-протокол (CallMethod/Get/Set) —
// в обёртке interpreter.SpreadsheetDocumentArea.
type Area struct {
	Cells  map[string]*Cell // относительные координаты "row,col"
	Top    int
	Left   int
	Bottom int
	Right  int
	Name   string // имя для именованных областей (необязательно)
}

// NewArea создаёт пустую область с заданными границами (0-based).
func NewArea(top, left, bottom, right int) *Area {
	return &Area{
		Cells:  make(map[string]*Cell),
		Top:    top,
		Left:   left,
		Bottom: bottom,
		Right:  right,
	}
}

// Rows возвращает число строк в области.
func (a *Area) Rows() int { return a.Bottom - a.Top + 1 }

// Cols возвращает число колонок в области.
func (a *Area) Cols() int { return a.Right - a.Left + 1 }

// GetOrCreateCell возвращает (создавая при необходимости) ячейку в
// относительных координатах (row, col) внутри области.
func (a *Area) GetOrCreateCell(row, col int) *Cell {
	key := fmt.Sprintf("%d,%d", row, col)
	if cell, ok := a.Cells[key]; ok {
		return cell
	}
	cell := NewCell("")
	a.Cells[key] = cell
	return cell
}

// Clear очищает все ячейки области.
func (a *Area) Clear() {
	a.Cells = make(map[string]*Cell)
}

// Merge объединяет все ячейки области в одну (через colSpan/rowSpan ячейки 0,0).
func (a *Area) Merge() {
	cols := a.Cols()
	rows := a.Rows()
	if rows == 1 && cols == 1 {
		return
	}
	cell := a.GetOrCreateCell(0, 0)
	cell.ColSpan = cols
	cell.RowSpan = rows
}

// Margins — поля страницы PDF в миллиметрах.
type Margins struct {
	Top    float64 `yaml:"top,omitempty"`
	Bottom float64 `yaml:"bottom,omitempty"`
	Left   float64 `yaml:"left,omitempty"`
	Right  float64 `yaml:"right,omitempty"`
}

// PageSetup — параметры страницы для PDF-рендера (план 64, этап 2).
// Orientation: "portrait"/"landscape" (плюс рус. синонимы нормализуются
// в DSL-обвязке). Format: именованный "A4"/"A5"/"Letter" и т.п. либо кастомный
// "Ш×В" в мм ("229x162mm") — литеральный размер бланка (ориентация к нему не
// применяется).
// MarginsMM — поля в мм; YAML-ключ — естественный margins: (поле остаётся
// MarginsMM в Go). Наполняется из page: YAML-макета (план 64, этап 3+).
type PageSetup struct {
	Orientation string  `yaml:"orientation,omitempty"`
	Format      string  `yaml:"format,omitempty"`
	MarginsMM   Margins `yaml:"margins,omitempty"`
}

// DefaultPageSetup — A4, портрет, поля 10 мм со всех сторон.
func DefaultPageSetup() PageSetup {
	return PageSetup{
		Orientation: "portrait",
		Format:      "A4",
		MarginsMM:   Margins{Top: 10, Bottom: 10, Left: 10, Right: 10},
	}
}

// Document — табличный документ для печатных форм. Содержит данные: ячейки,
// размеры, разрывы страниц, повтор шапки, текущую позицию вывода. Рендеры
// (HTML/PDF) и операции вывода — методы этого пакета; DSL-протокол поверх —
// в interpreter.SpreadsheetDocument, делегирующем сюда.
type Document struct {
	Groups     []RowGroup
	FreezeRows int
	FreezeCols int
	HideGrid   bool
	AutoScale  bool              // print width fit: one uniform scale, without changing screen/data
	Cells      map[CellKey]*Cell // (row,col) -> cell
	RowCount   int
	ColCount   int
	// ColWidths/RowHeights — размеры колонок/строк (1-based индекс → размер в px).
	// ШиринаКолонки/ВысотаСтроки пишут сюда, НЕ материализуя ячейки; HTML-рендер
	// накладывает их на колонку/строку целиком. PDF-рендер конвертирует px → мм.
	ColWidths    map[int]float64
	RowHeights   map[int]float64
	CurrentRow   int
	CurrentCol   int
	PageBreaks   []int
	ShowMode     bool
	FileName     string
	RepeatHeader bool
	HeaderArea   *Area
	BackURL      string    // URL для кнопки «Назад» в тулбаре HTML
	Page         PageSetup // параметры страницы для PDF (этап 2)
}

// NewDocument создаёт пустой документ с дефолтным размером сетки (как в старом
// SpreadsheetDocument: 100×50).
func NewDocument() *Document {
	return &Document{
		Cells:      make(map[CellKey]*Cell),
		ColWidths:  make(map[int]float64),
		RowHeights: make(map[int]float64),
		RowCount:   100,
		ColCount:   50,
		CurrentRow: 0,
		CurrentCol: 0,
		Page:       DefaultPageSetup(),
	}
}

// GetCell возвращает ячейку или nil.
func (d *Document) GetCell(row, col int) *Cell {
	return d.Cells[CellKey{row, col}]
}

// GetOrCreateCell возвращает (создавая при необходимости) ячейку (row, col).
func (d *Document) GetOrCreateCell(row, col int) *Cell {
	key := CellKey{row, col}
	if cell, ok := d.Cells[key]; ok {
		return cell
	}
	cell := NewCell("")
	d.Cells[key] = cell
	return cell
}

// SetCell записывает текст и значение в ячейку.
func (d *Document) SetCell(row, col int, text string) {
	cell := d.GetOrCreateCell(row, col)
	cell.Text = text
	cell.Value = text
}

// Put выводит область в текущую позицию и переходит на следующую строку.
func (d *Document) Put(area *Area) {
	if area == nil {
		return
	}
	rows := area.Rows()
	cols := area.Cols()

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			key := fmt.Sprintf("%d,%d", r, c)
			if srcCell, exists := area.Cells[key]; exists {
				targetRow := d.CurrentRow + r
				targetCol := d.CurrentCol + c
				destCell := d.GetOrCreateCell(targetRow, targetCol)
				*destCell = *srcCell
			}
		}
	}

	d.CurrentRow += rows
	d.CurrentCol = 0
}

// Append выводит область справа от ранее выведенной (без перехода на строку).
func (d *Document) Append(area *Area) {
	if area == nil {
		return
	}
	// Найти правую границу текущей строки (по реальным ячейкам). После выноса
	// размеров на Document фантомных width-ячеек больше нет — соседняя область
	// встаёт вплотную, а не уезжает в конец сетки.
	maxCol := 0
	for col := 0; col < d.ColCount; col++ {
		if d.GetCell(d.CurrentRow, col) != nil {
			maxCol = col + 1
		}
	}
	d.CurrentCol = maxCol

	rows := area.Rows()
	cols := area.Cols()

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			key := fmt.Sprintf("%d,%d", r, c)
			if srcCell, exists := area.Cells[key]; exists {
				targetRow := d.CurrentRow + r
				targetCol := d.CurrentCol + c
				destCell := d.GetOrCreateCell(targetRow, targetCol)
				*destCell = *srcCell
			}
		}
	}
}

// Clear очищает документ и сбрасывает позицию вывода.
func (d *Document) Clear() {
	d.Cells = make(map[CellKey]*Cell)
	d.ColWidths = make(map[int]float64)
	d.RowHeights = make(map[int]float64)
	d.CurrentRow = 0
	d.CurrentCol = 0
	d.PageBreaks = nil
}

// RemoveArea удаляет содержимое прямоугольника области из документа.
func (d *Document) RemoveArea(area *Area) {
	if area == nil {
		return
	}
	for row := area.Top; row <= area.Bottom; row++ {
		for col := area.Left; col <= area.Right; col++ {
			delete(d.Cells, CellKey{row, col})
		}
	}
}

// PageBreak вставляет разрыв страницы в текущей позиции.
func (d *Document) PageBreak() {
	d.PageBreaks = append(d.PageBreaks, d.CurrentRow)
}

// usablePageHeightMM возвращает доступную высоту страницы (мм) по PageSetup:
// высота формата минус верхнее и нижнее поля.
func (d *Document) usablePageHeightMM() float64 {
	_, h := pageSizeMM(d.Page)
	usable := h - d.Page.MarginsMM.Top - d.Page.MarginsMM.Bottom
	if usable <= 0 {
		return h
	}
	return usable
}

// RowsPerPage оценивает число строк на странице по реальной высоте страницы
// (PageSetup) и средней высоте строки документа. Заменяет прежнюю константу
// rowsPerPage=50. Минимум — 1 строка.
func (d *Document) RowsPerPage() int {
	usable := d.usablePageHeightMM()
	rowMM := d.avgRowHeightMM()
	if rowMM <= 0 {
		rowMM = minRowMM
	}
	n := int(usable / rowMM)
	if n < 1 {
		n = 1
	}
	return n
}

// avgRowHeightMM — средняя высота строки документа в мм (для пагинации вывода
// DSL). Заданные строчные высоты учитываются как px→мм, иначе minRowMM.
func (d *Document) avgRowHeightMM() float64 {
	maxRow, _ := d.ContentBounds()
	if maxRow < 0 {
		return minRowMM
	}
	total := 0.0
	count := 0
	for r := 0; r <= maxRow; r++ {
		if px := d.RowHeights[r+1]; px > 0 {
			total += pxToMM(px)
		} else {
			total += minRowMM
		}
		count++
	}
	if count == 0 {
		return minRowMM
	}
	return total / float64(count)
}

// RowsFitOnPage сообщает, помещается ли область высотой areaRows строк на
// текущей странице при позиции currentRow, исходя из реальной высоты страницы.
func (d *Document) RowsFitOnPage(currentRow, areaRows int) bool {
	rpp := d.RowsPerPage()
	rowsRemaining := rpp - (currentRow % rpp)
	return areaRows <= rowsRemaining
}

// CheckOutput проверяет, поместится ли область на текущей странице (по реальной
// высоте страницы из PageSetup).
func (d *Document) CheckOutput(area *Area) bool {
	if area == nil {
		return true
	}
	return d.RowsFitOnPage(d.CurrentRow, area.Rows())
}

// EndPage завершает текущую страницу и переходит к началу следующей.
func (d *Document) EndPage() {
	rpp := d.RowsPerPage()
	nextPage := (d.CurrentRow/rpp + 1) * rpp
	d.CurrentRow = nextPage
	d.CurrentCol = 0
}

// RepeatOnPrint помечает область как повторяемую на каждой странице.
func (d *Document) RepeatOnPrint(area *Area, repeat bool) {
	if area != nil && repeat {
		d.RepeatHeader = true
		d.HeaderArea = area
	}
}

// Draw вставляет рисунок в текущую позицию и сдвигает колонку.
func (d *Document) Draw(picture string) {
	cell := d.GetOrCreateCell(d.CurrentRow, d.CurrentCol)
	cell.Picture = picture
	d.CurrentCol++
}

// GetPicture извлекает первый рисунок из области.
func (d *Document) GetPicture(area *Area) string {
	if area == nil {
		return ""
	}
	for row := area.Top; row <= area.Bottom; row++ {
		for col := area.Left; col <= area.Right; col++ {
			if cell := d.GetCell(row, col); cell != nil && cell.Picture != "" {
				return cell.Picture
			}
		}
	}
	return ""
}

// SetColumnWidth задаёт ширину колонки (1-based). Хранится на документе и
// накладывается рендером на колонку целиком — ячейки НЕ материализуются.
func (d *Document) SetColumnWidth(col int, width float64) {
	if d.ColWidths == nil {
		d.ColWidths = make(map[int]float64)
	}
	d.ColWidths[col] = width
}

// ColumnWidth возвращает ширину колонки (1-based) или 0, если не задана.
func (d *Document) ColumnWidth(col int) float64 {
	return d.ColWidths[col]
}

// SetRowHeight задаёт высоту строки (1-based). Хранится на документе и
// накладывается рендером на строку целиком — ячейки НЕ материализуются.
func (d *Document) SetRowHeight(row int, height float64) {
	if d.RowHeights == nil {
		d.RowHeights = make(map[int]float64)
	}
	d.RowHeights[row] = height
}

// RowHeight возвращает высоту строки (1-based) или 0, если не задана.
func (d *Document) RowHeight(row int) float64 {
	return d.RowHeights[row]
}

// SetAlign задаёт выравнивание для прямоугольника области (координаты 0-based).
func (d *Document) SetAlign(area *Area, hAlign, vAlign string) {
	if area == nil {
		return
	}
	for row := area.Top; row <= area.Bottom; row++ {
		for col := area.Left; col <= area.Right; col++ {
			if cell := d.GetOrCreateCell(row, col); cell != nil {
				cell.Align = strings.ToLower(hAlign)
				cell.Vertical = strings.ToLower(vAlign)
			}
		}
	}
}

// Merge объединяет ячейки прямоугольника (1-based координаты, как в DSL).
func (d *Document) Merge(top, left, bottom, right int) {
	if top < 0 || left < 0 || bottom < top || right < left {
		return
	}
	top--
	left--
	bottom--
	right--

	if cell := d.GetOrCreateCell(top, left); cell != nil {
		cell.ColSpan = right - left + 1
		cell.RowSpan = bottom - top + 1
	}
}

// ContentBounds возвращает максимальные индексы строки и колонки с содержимым
// (с учётом colSpan по колонкам и rowSpan по строкам).
func (d *Document) ContentBounds() (int, int) {
	maxRow, maxCol := 0, 0
	for key, cell := range d.Cells {
		if cell == nil {
			continue
		}
		colExtent := key.Col
		if cell.ColSpan > 1 {
			colExtent = key.Col + cell.ColSpan - 1
		}
		rowExtent := key.Row
		if cell.RowSpan > 1 {
			rowExtent = key.Row + cell.RowSpan - 1
		}
		if rowExtent > maxRow {
			maxRow = rowExtent
		}
		if colExtent > maxCol {
			maxCol = colExtent
		}
	}
	return maxRow, maxCol
}

// TextString возвращает табличный текст документа (колонки через \t, строки \n).
func (d *Document) TextString() string {
	var sb strings.Builder
	maxRow, maxCol := d.ContentBounds()
	for row := 0; row <= maxRow; row++ {
		for col := 0; col <= maxCol; col++ {
			if cell := d.GetCell(row, col); cell != nil {
				sb.WriteString(cell.Text)
				sb.WriteString("\t")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
