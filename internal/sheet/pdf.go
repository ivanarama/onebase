package sheet

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// PDF-рендер табличного документа (план 64, этап 2). Рисуем по координатам:
// сетка колонок (px→мм/%/mm), авто-высоты строк по SplitText, спаны через карту
// covered (как в html.go), границы legacy-пресетом, фоны, выравнивания H/V,
// авто- и явные разрывы страниц с повтором HeaderArea. Кириллица — через
// встроенные PT-шрифты (fonts.go), без транслитерации.
//
// Известные ограничения (roadmap, этап 3+):
//   - Границы соседних ячеек рисуются дважды (каждая ячейка обводит свой
//     прямоугольник через drawCellBorder), из-за чего на общих рёбрах линия
//     удваивается по толщине. Этап 3 заменит legacy-пресет на per-side Borders
//     с единым обходом рёбер сетки.
//   - registerFonts парсит встроенные TTF на каждый запрос PDF(). При большом
//     потоке печати стоит закэшировать разобранные шрифты/инстанс fpdf.

// Единицы и константы геометрии.
const (
	pxPerInch = 96.0
	mmPerInch = 25.4
	ptToMM    = mmPerInch / 72.0 // 1pt = 0.352777… мм
	lineGap   = 1.2              // межстрочный коэффициент
	cellPadMM = 1.0              // горизонтальный/вертикальный паддинг ячейки
	minRowMM  = 4.0              // минимальная высота строки
)

// pxToMM конвертирует пиксели (CSS, 96dpi) в миллиметры.
func pxToMM(px float64) float64 { return px * mmPerInch / pxPerInch }

// PDFOptions — параметры PDF-рендера. Title уходит в метаданные документа.
// Геометрия страницы берётся из Document.Page (PageSetup).
type PDFOptions struct {
	Title string
}

// orientFlag нормализует ориентацию в флаг fpdf ("P"/"L").
func orientFlag(orientation string) string {
	switch strings.ToLower(strings.TrimSpace(orientation)) {
	case "landscape", "ландшафт", "альбомная", "l":
		return "L"
	default:
		return "P"
	}
}

// pageFormat нормализует формат страницы для fpdf (по умолчанию A4).
func pageFormat(format string) string {
	f := strings.TrimSpace(format)
	if f == "" {
		return "A4"
	}
	return f
}

// orientLandscape сообщает, ландшафтная ли ориентация.
func orientLandscape(orientation string) bool { return orientFlag(orientation) == "L" }

// formatSizeMM возвращает портретные размеры (ширина, высота) известных
// форматов в мм. Для неизвестного формата — A4. Используется для пагинации
// DSL-вывода без создания fpdf-документа.
func formatSizeMM(format string) (w, h float64) {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "A3":
		return 297, 420
	case "A5":
		return 148, 210
	case "LETTER":
		return 215.9, 279.4
	case "LEGAL":
		return 215.9, 355.6
	default: // A4
		return 210, 297
	}
}

// customFormatRe распознаёт кастомный размер страницы вида "229x162",
// "229x162mm", "229 × 162 мм", "210,5x297" — Ш×В в миллиметрах. Разделитель —
// латинские x/X, кириллическая х, знак умножения × или *. Десятичный разделитель
// — точка или запятая. Суффикс mm/мм необязателен.
var customFormatRe = regexp.MustCompile(`^\s*([0-9]+(?:[.,][0-9]+)?)\s*[xXхХ×*]\s*([0-9]+(?:[.,][0-9]+)?)\s*(?:mm|мм)?\s*$`)

// customFormatMM разбирает кастомный размер страницы в мм. Возвращает
// (ширина, высота, true) при совпадении с customFormatRe; иначе (0,0,false) —
// формат считается именованным (A4/A5/…). Размер трактуется ЛИТЕРАЛЬНО (как
// CSS @page size: W H): ориентация к нему не применяется. Позволяет печатать
// нестандартные бланки (конверты, ярлыки) без новых полей — через тот же
// page.format в макете и РазмерСтраницы в DSL.
func customFormatMM(format string) (w, h float64, ok bool) {
	m := customFormatRe.FindStringSubmatch(format)
	if m == nil {
		return 0, 0, false
	}
	w, err1 := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
	h, err2 := strconv.ParseFloat(strings.Replace(m[2], ",", ".", 1), 64)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// pageSizeMM возвращает ФИНАЛЬНЫЙ размер страницы (мм) с учётом ориентации:
// для именованного формата — портретные размеры со свопом в ландшафте; для
// кастомного "Ш×В" — литерально (ориентация уже заложена в размер). Единая точка
// для пагинации (usablePageHeightMM) и сборки fpdf.
func pageSizeMM(page PageSetup) (w, h float64) {
	if cw, ch, ok := customFormatMM(page.Format); ok {
		return cw, ch
	}
	w, h = formatSizeMM(page.Format)
	if orientLandscape(page.Orientation) {
		w, h = h, w
	}
	return w, h
}

// resolveFont выбирает семейство и начертание PT-шрифта по стилю ячейки.
// FontFamily с признаком serif/Times → PT Serif, иначе PT Sans.
// PT Sans Italic в комплекте нет: курсив-sans честно падает на PT Serif Italic.
func resolveFont(family string, bold, italic bool) (fontFamily, style string) {
	lf := strings.ToLower(family)
	serif := strings.Contains(lf, "serif") || strings.Contains(lf, "times") ||
		strings.Contains(lf, "georgia") || family == ""
	if italic && !serif {
		// PT Sans Italic отсутствует → используем PT Serif Italic.
		serif = true
	}
	if serif {
		fontFamily = "PTSerif"
	} else {
		fontFamily = "PTSans"
	}
	if bold {
		style += "B"
	}
	if italic {
		style += "I"
	}
	return fontFamily, style
}

// registerFonts регистрирует все 6 начертаний из встроенных байтов.
// fpdf субсетит шрифты при выводе, так что регистрация всех начертаний дёшева
// (см. спайк: ~70КБ на весь набор при реальном использовании одного).
func registerFonts(pdf *fpdf.Fpdf) {
	pdf.AddUTF8FontFromBytes("PTSerif", "", PTSerifRegular)
	pdf.AddUTF8FontFromBytes("PTSerif", "B", PTSerifBold)
	pdf.AddUTF8FontFromBytes("PTSerif", "I", PTSerifItalic)
	pdf.AddUTF8FontFromBytes("PTSerif", "BI", PTSerifBoldItalic)
	pdf.AddUTF8FontFromBytes("PTSans", "", PTSansRegular)
	pdf.AddUTF8FontFromBytes("PTSans", "B", PTSansBold)
}

// resolveColumnWidthsMM вычисляет ширины всех колонок документа в мм по доступной
// ширине usable (мм). Источник — Document.ColWidths (px, как из DSL ШиринаКолонки
// и HTML-рендера); незаданные колонки делят остаток поровну. Это обобщение
// computeColWidths из printform/pdf.go (там работали строки "%"/"mm"; здесь —
// числовые px из модели). colCount — число колонок (0-based maxCol+1).
func (d *Document) resolveColumnWidthsMM(usable float64, colCount int) []float64 {
	widths := make([]float64, colCount)
	used := 0.0
	free := make([]int, 0, colCount)
	for c := 0; c < colCount; c++ {
		px := d.ColWidths[c+1] // 1-based
		if px <= 0 {
			// fallback на индивидуальную ширину ячейки в этой колонке (px).
			if w := d.maxCellWidthPx(c); w > 0 {
				px = w
			}
		}
		if px > 0 {
			mm := pxToMM(px)
			widths[c] = mm
			used += mm
		} else {
			free = append(free, c)
		}
	}
	if len(free) > 0 {
		remaining := usable - used
		if remaining < float64(len(free))*5 {
			// Слишком тесно — дать каждой свободной колонке разумный минимум.
			remaining = float64(len(free)) * 20
		}
		each := remaining / float64(len(free))
		for _, c := range free {
			widths[c] = each
		}
	}

	// Клампинг: если суммарная ширина не помещается в usable (например, 5 колонок
	// по 400px ≈ 529мм против 190мм usable A4), масштабируем ВСЕ колонки
	// коэффициентом usable/sum — контент не уезжает за правый край листа.
	sum := 0.0
	for _, w := range widths {
		sum += w
	}
	if sum > usable && sum > 0 {
		scale := usable / sum
		for c := range widths {
			widths[c] *= scale
		}
	}
	return widths
}

// maxCellWidthPx возвращает максимальную индивидуальную ширину ячейки в колонке
// col (0-based), если ширина колонки не задана на документе.
func (d *Document) maxCellWidthPx(col int) float64 {
	max := 0.0
	for key, cell := range d.Cells {
		if key.Col == col && cell != nil && cell.Width > max {
			max = cell.Width
		}
	}
	return max
}

// fontSizeOr возвращает размер шрифта ячейки или дефолт (10pt).
func fontSizeOr(cell *Cell) float64 {
	if cell != nil && cell.FontSize > 0 {
		return float64(cell.FontSize)
	}
	return 10
}

// PDF рендерит документ в PDF и возвращает байты. Кириллица — встроенными
// PT-шрифтами без транслитерации.
func (d *Document) PDF(opts PDFOptions) ([]byte, error) {
	page := d.Page
	if page.Format == "" {
		page = DefaultPageSetup()
	}
	var pdf *fpdf.Fpdf
	if cw, ch, ok := customFormatMM(page.Format); ok {
		// Кастомный размер: литеральные Ш×В в портретной ориентации fpdf
		// (свопа нет — ориентация уже в размере). Size приоритетнее SizeStr.
		pdf = fpdf.NewCustom(&fpdf.InitType{
			OrientationStr: "P",
			UnitStr:        "mm",
			Size:           fpdf.SizeType{Wd: cw, Ht: ch},
		})
	} else {
		pdf = fpdf.New(orientFlag(page.Orientation), "mm", pageFormat(page.Format), "")
	}
	// Поле ячейки задаёт cellPadMM. Собственное поле fpdf (cMargin, по умолчанию
	// 1 мм) иначе применяется второй раз: SplitText переносит по ширине
	// avail−2·cMargin, а CellFormat сдвигает текст ещё на cMargin. В узкой
	// колонке переносилось то, что помещается: «ОТ» шириной 2,4 мм — при
	// 4,35 мм места.
	pdf.SetCellMargin(0)
	registerFonts(pdf)

	m := page.MarginsMM
	pdf.SetMargins(m.Left, m.Top, m.Right)
	pdf.SetAutoPageBreak(false, m.Bottom)
	if opts.Title != "" {
		pdf.SetTitle(opts.Title, true)
	}
	pdf.AddPage()

	pageW, pageH := pdf.GetPageSize()
	usable := pageW - m.Left - m.Right
	pageBottom := pageH - m.Bottom

	maxRow, maxCol := d.ContentBounds()
	colCount := maxCol + 1
	colWidths := d.resolveColumnWidthsMM(usable, colCount)

	// Левый край колонки (мм) для быстрого позиционирования.
	colX := make([]float64, colCount+1)
	colX[0] = m.Left
	for c := 0; c < colCount; c++ {
		colX[c+1] = colX[c] + colWidths[c]
	}

	// Высоты строк считаются тем же движком, что и рендер: pdf-инстанс со
	// шрифтами уже создан выше (registerFonts), поэтому передаём его в
	// измеритель — wrapText/SplitText с установленным шрифтом ячейки.
	rowHeights := d.computeRowHeightsMM(pdf, maxRow, colWidths)

	// Множество строк с явным разрывом страницы ПЕРЕД ними.
	breakBefore := make(map[int]bool, len(d.PageBreaks))
	for _, r := range d.PageBreaks {
		breakBefore[r] = true
	}

	covered := make(map[CellKey]bool)

	// headerHeights — высоты строк повторяемой шапки (если задана). Считаем тем
	// же измерителем, что и тело документа: многострочная шапка УПД на 2-й и
	// далее страницах не сожмётся в minRowMM (дефект 2).
	var headerRows int
	var headerHeights []float64
	if d.RepeatHeader && d.HeaderArea != nil {
		headerRows = d.HeaderArea.Rows()
		headerHeights = d.HeaderArea.computeAreaRowHeightsMM(pdf, colWidths)
	}

	y := m.Top

	drawHeader := func() float64 {
		if d.HeaderArea == nil {
			return y
		}
		hy := m.Top
		for hr := 0; hr < headerRows; hr++ {
			d.drawAreaRow(pdf, d.HeaderArea, hr, colX, colWidths, colCount, hy, headerHeights[hr])
			hy += headerHeights[hr]
		}
		return hy
	}

	for row := 0; row <= maxRow; row++ {
		rh := rowHeights[row]

		// Явный разрыв страницы перед строкой.
		if breakBefore[row] && row > 0 {
			pdf.AddPage()
			y = m.Top
			if d.RepeatHeader && d.HeaderArea != nil {
				y = drawHeader()
			}
		}

		// Авто-разрыв: строка не помещается на текущей странице.
		if y+rh > pageBottom && row > 0 {
			pdf.AddPage()
			y = m.Top
			if d.RepeatHeader && d.HeaderArea != nil {
				y = drawHeader()
			}
		}

		d.drawDocRow(pdf, row, maxCol, colX, colWidths, colCount, y, rh, covered)
		y += rh
	}

	var w byteWriter
	if err := pdf.Output(&w); err != nil {
		return nil, err
	}
	return w.data, nil
}

// computeRowHeightsMM вычисляет высоты всех строк документа в мм. Заданная
// строчная высота (RowHeights, px) приоритетна; иначе авто = max по ячейкам
// строки: число строк текста при переносе по ширине ячейки × line height +
// паддинг.
//
// Измеряем ТЕМ ЖЕ движком, что и рендер (drawCell): wrapText → pdf.SplitText с
// установленным шрифтом ячейки. Шрифт/размер/начертание влияют на SplitText,
// поэтому SetFont перед измерением обязателен. Это устраняет расхождение со
// старой эвристикой по средней ширине символа, из-за которого кириллический
// текст недооценивался и вываливался за ячейку (репро: «Стол письменный
// дубовый с ящиками» @25мм/10pt — реально 4 строки, эвристика 3). Требует
// готовый pdf-инстанс с зарегистрированными шрифтами, поэтому PDF() создаёт его
// до расчёта высот.
func (d *Document) computeRowHeightsMM(pdf *fpdf.Fpdf, maxRow int, colWidths []float64) []float64 {
	heights := make([]float64, maxRow+1)
	for row := 0; row <= maxRow; row++ {
		if px := d.RowHeights[row+1]; px > 0 {
			heights[row] = pxToMM(px)
			if heights[row] < minRowMM {
				heights[row] = minRowMM
			}
			continue
		}
		h := minRowMM
		for col := 0; col < len(colWidths); col++ {
			cell := d.GetCell(row, col)
			if cell == nil || (cell.Text == "" && cell.RichHTML == "") {
				continue
			}
			// Ширина для переноса с учётом colspan.
			cw := colWidths[col]
			for cs := 1; cs < cell.ColSpan && col+cs < len(colWidths); cs++ {
				cw += colWidths[col+cs]
			}
			needed := cellHeightMM(pdf, cell, cw)
			// rowspan распределяет высоту на несколько строк — здесь упрощённо
			// учитываем как высоту одной строки (известное ограничение MVP).
			if cell.RowSpan > 1 {
				needed = needed / float64(cell.RowSpan)
			}
			if needed > h {
				h = needed
			}
		}
		heights[row] = h
	}
	return heights
}

// computeAreaRowHeightsMM вычисляет высоты строк области (HeaderArea) тем же
// измерителем, что и computeRowHeightsMM. Используется для повторяемой шапки:
// раньше высоты строк шапки фиксировались на minRowMM, из-за чего многострочная
// шапка (УПД) на 2-й и далее страницах сжималась и текст обрезался.
func (a *Area) computeAreaRowHeightsMM(pdf *fpdf.Fpdf, colWidths []float64) []float64 {
	rows := a.Rows()
	cols := a.Cols()
	heights := make([]float64, rows)
	for r := 0; r < rows; r++ {
		h := minRowMM
		for c := 0; c < cols && c < len(colWidths); c++ {
			cell := a.Cells[fmt.Sprintf("%d,%d", r, c)]
			if cell == nil || cell.Text == "" {
				continue
			}
			cw := colWidths[c]
			for cs := 1; cs < cell.ColSpan && c+cs < len(colWidths); cs++ {
				cw += colWidths[c+cs]
			}
			if needed := cellHeightMM(pdf, cell, cw); needed > h {
				h = needed
			}
		}
		heights[r] = h
	}
	return heights
}

// cellHeightMM возвращает высоту ячейки (мм), необходимую для размещения её
// текста при переносе по ширине cw (мм). Шрифт ячейки устанавливается перед
// измерением — он влияет на разбиение pdf.SplitText.
func cellHeightMM(pdf *fpdf.Fpdf, cell *Cell, cw float64) float64 {
	// richtext-проекция считает высоту собственным измерителем (план 65, этап 3).
	if cell.RichHTML != "" {
		return richCellHeightMM(pdf, cell, cw)
	}
	fs := fontSizeOr(cell)
	lineH := fs * ptToMM * lineGap
	avail := cw - 2*cellPadMM
	if avail <= 0 {
		avail = cw
	}
	family, style := resolveFont(cell.FontFamily, cell.Bold, cell.Italic)
	pdf.SetFont(family, style, fs)
	lines := len(wrapText(pdf, cell.Text, avail))
	return float64(lines)*lineH + 2*cellPadMM
}

// drawDocRow рисует одну строку документа: фон, границы, текст для каждой не
// перекрытой ячейки. covered обновляется colspan/rowspan.
func (d *Document) drawDocRow(pdf *fpdf.Fpdf, row, maxCol int, colX, colWidths []float64, colCount int, y, rh float64, covered map[CellKey]bool) {
	for col := 0; col <= maxCol && col < colCount; col++ {
		if covered[CellKey{row, col}] {
			continue
		}
		cell := d.GetCell(row, col)
		x := colX[col]

		// Ширина с учётом colspan.
		w := colWidths[col]
		if cell != nil && cell.ColSpan > 1 {
			for cs := 1; cs < cell.ColSpan && col+cs < colCount; cs++ {
				w += colWidths[col+cs]
			}
			for c := col + 1; c < col+cell.ColSpan; c++ {
				covered[CellKey{row, c}] = true
			}
		}
		// Высота с учётом rowspan (суммой высот строк документа недоступна
		// здесь — высоты строк ниже могут быть ещё не посчитаны; в MVP
		// rowspan-высота приближается высотой текущей строки × rowspan).
		h := rh
		if cell != nil && cell.RowSpan > 1 {
			h = rh * float64(cell.RowSpan)
			for r := row + 1; r < row+cell.RowSpan; r++ {
				for c := col; c < col+maxInt(cell.ColSpan, 1) && c < colCount; c++ {
					covered[CellKey{r, c}] = true
				}
			}
		}
		drawCell(pdf, cell, x, y, w, h)
	}
}

// drawAreaRow рисует строку области (HeaderArea) с относительными координатами.
func (d *Document) drawAreaRow(pdf *fpdf.Fpdf, area *Area, relRow int, colX, colWidths []float64, colCount int, y, rh float64) {
	cols := area.Cols()
	for c := 0; c < cols && c < colCount; c++ {
		key := fmt.Sprintf("%d,%d", relRow, c)
		cell := area.Cells[key]
		x := colX[c]
		w := colWidths[c]
		if cell != nil && cell.ColSpan > 1 {
			for cs := 1; cs < cell.ColSpan && c+cs < colCount; cs++ {
				w += colWidths[c+cs]
			}
		}
		drawCell(pdf, cell, x, y, w, rh)
	}
}

// drawCell рисует одну ячейку: фон, текст (с выравниванием H/V), границы.
func drawCell(pdf *fpdf.Fpdf, cell *Cell, x, y, w, h float64) {
	if cell == nil {
		return
	}

	// Фон.
	if cell.BackColor != "" {
		if r, g, b, ok := parseHexColor(cell.BackColor); ok {
			pdf.SetFillColor(r, g, b)
			pdf.Rect(x, y, w, h, "F")
		}
	}

	// richtext-проекция (план 65, этап 3): вместо plain-текста рисуем разбор
	// ограниченного HTML (абзацы/начертания/списки + data-URI картинки). recover
	// гасит панику на битом HTML/картинке — печать документа не падает.
	if cell.RichHTML != "" {
		func() {
			defer func() {
				if r := recover(); r != nil {
					pdf.ClearError()
				}
			}()
			drawRichText(pdf, cell, x, y, w, h, false)
		}()
		drawCellBorder(pdf, cell, x, y, w, h)
		return
	}

	// Текст.
	if cell.Text != "" {
		family, style := resolveFont(cell.FontFamily, cell.Bold, cell.Italic)
		fs := fontSizeOr(cell)
		pdf.SetFont(family, style, fs)
		if cell.TextColor != "" {
			if r, g, b, ok := parseHexColor(cell.TextColor); ok {
				pdf.SetTextColor(r, g, b)
			}
		} else {
			pdf.SetTextColor(0, 0, 0)
		}

		lineH := fs * ptToMM * lineGap
		avail := w - 2*cellPadMM
		if avail <= 0 {
			avail = w
		}
		lines := wrapText(pdf, cell.Text, avail)
		textH := float64(len(lines)) * lineH

		// Вертикальное выравнивание блока текста.
		ty := y + cellPadMM
		switch strings.ToLower(cell.Vertical) {
		case "center", "middle", "центр":
			ty = y + (h-textH)/2
		case "bottom", "низ":
			ty = y + h - textH - cellPadMM
		}
		if ty < y {
			ty = y
		}

		align := pdfAlign(cell.Align)
		for _, line := range lines {
			pdf.SetXY(x+cellPadMM, ty)
			pdf.CellFormat(avail, lineH, line, "", 0, align+"M", false, 0, "")
			ty += lineH
		}
	}

	// Картинка (после текста, до границ — чтобы рамка осталась поверх).
	if cell.Picture != "" {
		drawCellPicture(pdf, cell.Picture, x, y, w, h)
	}

	// Границы (legacy-пресет).
	drawCellBorder(pdf, cell, x, y, w, h)
}

// drawCellPicture вписывает картинку ячейки в её прямоугольник (с паддингом),
// сохраняя пропорции и центрируя. Поддерживаются только data-URI картинок
// (PNG/JPEG/GIF) — внешние URL в серверном PDF не загружаются (нет сети). Сбои
// декодирования/неподдерживаемый формат тихо игнорируются (картинка не
// блокирует печать документа).
// pictureCacheName строит имя ресурса картинки для fpdf по типу и содержимому.
// Хэш содержимого (а не длина) гарантирует, что разные картинки одного формата
// и длины получают разные имена, а идентичные — одно (дедупликация в PDF).
func pictureCacheName(imgType string, data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("pic_%s_%x", imgType, sum[:8])
}

func drawCellPicture(pdf *fpdf.Fpdf, pic string, x, y, w, h float64) {
	data, imgType, ok := decodeDataURIImage(pic)
	if !ok {
		return
	}
	// Имя регистрации = тип + хэш содержимого: одинаковые картинки делят один
	// зарегистрированный ресурс (fpdf не дублирует поток), а РАЗНЫЕ картинки
	// одного формата и длины не сталкиваются (длина в ключе давала коллизию —
	// fpdf возвращал первую из кэша).
	name := pictureCacheName(imgType, data)
	info := pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: imgType}, bytes.NewReader(data))
	if pdf.Err() || info == nil {
		// Сбросить ошибку парсера картинки — она не должна валить весь PDF.
		pdf.ClearError()
		return
	}
	iw, ih := info.Extent()
	if iw <= 0 || ih <= 0 {
		return
	}

	availW := w - 2*cellPadMM
	availH := h - 2*cellPadMM
	if availW <= 0 {
		availW = w
	}
	if availH <= 0 {
		availH = h
	}

	// Масштаб «вписать» (contain): сохраняем пропорции.
	scale := availW / iw
	if s := availH / ih; s < scale {
		scale = s
	}
	dw := iw * scale
	dh := ih * scale

	// Центрирование в ячейке.
	ix := x + (w-dw)/2
	iy := y + (h-dh)/2
	pdf.ImageOptions(name, ix, iy, dw, dh, false, fpdf.ImageOptions{ImageType: imgType}, 0, "")
	if pdf.Err() {
		pdf.ClearError()
	}
}

// drawCellBorder рисует рамку ячейки. Per-side границы (BorderLeft/Top/Right/
// Bottom) имеют приоритет: каждая сторона рисуется отдельной линией своей
// толщиной (thin=0.2/medium=0.4/thick=0.6мм). Иначе — legacy-пресет Border
// ("all"/"thin"/"thick"/"none"/"") целиком прямоугольником. Цвет — чёрный.
func drawCellBorder(pdf *fpdf.Fpdf, cell *Cell, x, y, w, h float64) {
	if hasPerSideBorders(cell) {
		drawPerSideBorders(pdf, cell, x, y, w, h)
		return
	}
	preset := strings.ToLower(strings.TrimSpace(cell.Border))
	var lw float64
	switch preset {
	case "", "none":
		return
	case "thick":
		lw = 0.5
	case "thin", "all":
		lw = 0.2
	default:
		lw = 0.2
	}
	pdf.SetLineWidth(lw)
	pdf.SetDrawColor(0, 0, 0)
	pdf.Rect(x, y, w, h, "D")
}

// drawPerSideBorders рисует каждую заданную сторону рамки отдельной линией.
func drawPerSideBorders(pdf *fpdf.Fpdf, cell *Cell, x, y, w, h float64) {
	pdf.SetDrawColor(0, 0, 0)
	if lw := sideWidthMM(cell.BorderTop); lw > 0 {
		pdf.SetLineWidth(lw)
		pdf.Line(x, y, x+w, y)
	}
	if lw := sideWidthMM(cell.BorderBottom); lw > 0 {
		pdf.SetLineWidth(lw)
		pdf.Line(x, y+h, x+w, y+h)
	}
	if lw := sideWidthMM(cell.BorderLeft); lw > 0 {
		pdf.SetLineWidth(lw)
		pdf.Line(x, y, x, y+h)
	}
	if lw := sideWidthMM(cell.BorderRight); lw > 0 {
		pdf.SetLineWidth(lw)
		pdf.Line(x+w, y, x+w, y+h)
	}
}

// sideWidthMM возвращает толщину линии стороны рамки в мм (0 = не рисуем).
func sideWidthMM(side string) float64 {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "", "none":
		return 0
	case "medium":
		return 0.4
	case "thick":
		return 0.6
	default: // thin
		return 0.2
	}
}

// pdfAlign конвертирует выравнивание ячейки в горизонтальный флаг fpdf.
func pdfAlign(align string) string {
	switch strings.ToLower(align) {
	case "center", "центр":
		return "C"
	case "right", "право":
		return "R"
	default:
		return "L"
	}
}

// wrapText разбивает текст по ширине avail (мм) текущим установленным шрифтом
// pdf, дополнительно уважая явные переводы строк.
func wrapText(pdf *fpdf.Fpdf, text string, avail float64) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		split := pdf.SplitText(para, avail)
		if len(split) == 0 {
			out = append(out, "")
			continue
		}
		out = append(out, split...)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// parseHexColor парсит "#rrggbb"/"#rgb" в компоненты RGB.
func parseHexColor(s string) (r, g, b int, ok bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	switch len(s) {
	case 3:
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
		// ok
	default:
		return 0, 0, 0, false
	}
	var rv, gv, bv int
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &rv, &gv, &bv); err != nil {
		return 0, 0, 0, false
	}
	return rv, gv, bv, true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// byteWriter — io.Writer, собирающий вывод fpdf в []byte.
type byteWriter struct{ data []byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	w.data = append(w.data, p...)
	return len(p), nil
}
