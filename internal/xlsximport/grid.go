package xlsximport

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/sheet"
)

// grid — лист, разобранный в матрицу ячеек макета.
//
// Covered отмечает позиции, накрытые объединением: у них нет своей ячейки, и в
// YAML они не выводятся вовсе — декларативный движок сам пропускает позиции под
// спаном (см. buildAreaSheet в internal/printform/declarative.go).
type grid struct {
	Rows    int
	Cols    int
	Cells   [][]printform.LayoutCell
	Covered [][]bool
	ColW    []string // CSS-ширина колонки ("120px")
	RowH    []string // CSS-высота строки ("" — по умолчанию)
	Page    *sheet.PageSetup

	// Named — именованные диапазоны Excel (Диспетчер имён): явная разметка
	// областей, перебивающая автоматику.
	Named []namedRange
	// PrintTitles — «сквозные строки» листа (_xlnm.Print_Titles): та же мысль,
	// что и binding.repeat_header.
	PrintTitles []namedRange
}

// readGrid разбирает лист в матрицу ячеек: тексты, оформление, объединения,
// картинки, размеры колонок/строк и параметры страницы.
func readGrid(f *excelize.File, name string, w *warnings) (*grid, error) {
	texts, textRows, textCols, err := readRows(f, name)
	if err != nil {
		return nil, err
	}
	rows, cols := usedRange(f, name, textRows, textCols, w)
	if rows == 0 || cols == 0 {
		return nil, ErrEmptySheet
	}

	g := &grid{Rows: rows, Cols: cols}
	g.Cells = make([][]printform.LayoutCell, rows)
	g.Covered = make([][]bool, rows)
	for r := range g.Cells {
		g.Cells[r] = make([]printform.LayoutCell, cols)
		g.Covered[r] = make([]bool, cols)
	}

	applyMerges(f, name, g, w)

	st := newStyleCache(f, w)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if g.Covered[r][c] {
				continue
			}
			cell := &g.Cells[r][c]
			cell.Text = strings.TrimRight(cellText(texts, r, c), " ")
			axis, aerr := excelize.CoordinatesToCellName(c+1, r+1)
			if aerr != nil {
				continue
			}
			if id, serr := f.GetCellStyle(name, axis); serr == nil && id != 0 {
				st.apply(id, cell)
			}
			if cell.Text != "" {
				if fx, ferr := f.GetCellFormula(name, axis); ferr == nil && fx != "" {
					w.add("Формулы не переносятся — в макет попало вычисленное значение ячейки.")
				}
			}
		}
	}

	applyPictures(f, name, g, w)
	trim(g)
	if g.Rows == 0 || g.Cols == 0 {
		return nil, ErrEmptySheet
	}

	readSizes(f, name, g)
	g.Page = readPage(f, name, w)
	g.Named, g.PrintTitles = readDefinedNames(f, name, g.Rows)

	if cfs, cerr := f.GetConditionalFormats(name); cerr == nil && len(cfs) > 0 {
		w.add("Условное форматирование не переносится — цвета и правила придётся задать в макете.")
	}
	return g, nil
}

// readRows читает значения листа одним потоковым проходом. Rows.Next()
// возвращает в том числе пропуски перед sparse-строкой, поэтому MaxRows+1
// достаточно, чтобы предупредить об обрезке, не доходя до содержимого, например,
// строки 1 048 576. В texts никогда не копится больше MaxRows x MaxCols значений.
func readRows(f *excelize.File, name string) (texts [][]string, rows, cols int, err error) {
	it, openErr := f.Rows(name)
	if openErr != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrParse, openErr)
	}
	defer func() {
		if closeErr := it.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("%w: %v", ErrParse, closeErr)
		}
	}()

	scannedRows := 0
	for it.Next() {
		scannedRows++
		if scannedRows > MaxRows {
			// Точного номера дальней sparse-строки узнавать не нужно: для
			// прежнего предупреждения достаточно sentinel за границей.
			rows = MaxRows + 1
			break
		}

		row, rowErr := it.Columns()
		if rowErr != nil {
			return nil, 0, 0, fmt.Errorf("%w: %v", ErrParse, rowErr)
		}
		// GetRows, который был здесь раньше, не включал в результат хвостовые
		// явно пустые строки. Сохраняем этот контракт used range.
		if len(row) > 0 {
			rows = scannedRows
		}
		cols = max(cols, len(row))
		if len(row) > MaxCols {
			row = row[:MaxCols]
		}
		// Копия с ограниченной capacity не удерживает целиком широкий backing
		// array, который Columns был вынужден собрать для текущей строки.
		texts = append(texts, append([]string(nil), row...))
	}
	if iterErr := it.Error(); iterErr != nil {
		return nil, 0, 0, fmt.Errorf("%w: %v", ErrParse, iterErr)
	}
	return texts, rows, cols, nil
}

// usedRange определяет размеры разбираемой области.
//
// Границы берутся по максимуму из четырёх источников: заполненные строки,
// объединения, ячейки с картинками и объявленный листом «использованный
// диапазон». Одного диапазона мало — книга, записанная программно, объявляет
// «A1», и импорт увидел бы одну ячейку; одних заполненных строк тоже мало —
// пустая ячейка с рамкой (клетка под подпись) текста не имеет, но в бланке
// нужна. Хвост лишнего потом срезает trim. Заполненные строки уже измерены
// потоковым readRows, повторно лист здесь не читается.
func usedRange(f *excelize.File, name string, textRows, textCols int, w *warnings) (rows, cols int) {
	rows, cols = textRows, textCols
	grow := func(r, c int) {
		rows = max(rows, r)
		cols = max(cols, c)
	}

	if merges, err := f.GetMergeCells(name); err == nil {
		for _, m := range merges {
			if c, r, cerr := excelize.CellNameToCoordinates(m.GetEndAxis()); cerr == nil {
				grow(r, c)
			}
		}
	}
	if cells, err := f.GetPictureCells(name); err == nil {
		for _, axis := range cells {
			if c, r, cerr := excelize.CellNameToCoordinates(axis); cerr == nil {
				grow(r, c)
			}
		}
	}
	// Содержательные границы — то, о чём имеет смысл предупреждать: раздутый
	// «использованный диапазон» обрезается молча, он не потеря.
	if rows > MaxRows {
		w.addf("Лист длиннее %d строк — импортированы первые %d.", MaxRows, MaxRows)
	}
	if cols > MaxCols {
		w.addf("Лист шире %d колонок — импортированы первые %d.", MaxCols, MaxCols)
	}

	if dim, err := f.GetSheetDimension(name); err == nil && dim != "" {
		last := dim
		if i := strings.LastIndex(dim, ":"); i >= 0 {
			last = dim[i+1:]
		}
		if c, r, cerr := excelize.CellNameToCoordinates(strings.ReplaceAll(last, "$", "")); cerr == nil {
			grow(r, c)
		}
	}

	return min(rows, MaxRows), min(cols, MaxCols)
}

// cellText достаёт текст ячейки из результата потокового чтения (там строки
// короче на хвост пустых ячеек).
func cellText(texts [][]string, r, c int) string {
	if r >= len(texts) || c >= len(texts[r]) {
		return ""
	}
	return texts[r][c]
}

// applyMerges переносит объединения в colspan/rowspan якорной ячейки и метит
// накрытые позиции.
func applyMerges(f *excelize.File, name string, g *grid, w *warnings) {
	merges, err := f.GetMergeCells(name)
	if err != nil {
		return
	}
	for _, m := range merges {
		c1, r1, e1 := excelize.CellNameToCoordinates(m.GetStartAxis())
		c2, r2, e2 := excelize.CellNameToCoordinates(m.GetEndAxis())
		if e1 != nil || e2 != nil {
			continue
		}
		if c1 > c2 {
			c1, c2 = c2, c1
		}
		if r1 > r2 {
			r1, r2 = r2, r1
		}
		// В 0-based координаты сетки.
		r1, c1, r2, c2 = r1-1, c1-1, r2-1, c2-1
		if r1 < 0 || c1 < 0 || r1 >= g.Rows || c1 >= g.Cols {
			continue
		}
		if r2 >= g.Rows || c2 >= g.Cols {
			w.add("Объединение выходит за импортированную область листа и обрезано.")
		}
		r2 = min(r2, g.Rows-1)
		c2 = min(c2, g.Cols-1)

		g.Cells[r1][c1].ColSpan = c2 - c1 + 1
		g.Cells[r1][c1].RowSpan = r2 - r1 + 1
		for r := r1; r <= r2; r++ {
			for c := c1; c <= c2; c++ {
				if r == r1 && c == c1 {
					continue
				}
				g.Covered[r][c] = true
			}
		}
	}
}

// applyPictures кладёт картинки ячеек в макет как data-URI (sheet рендерит их и
// в HTML, и в PDF). Крупные и неподдерживаемые форматы пропускаются с
// предупреждением: раздувать YAML мегабайтом base64 хуже, чем сказать вслух,
// что картинку надо вставить отдельно.
func applyPictures(f *excelize.File, name string, g *grid, w *warnings) {
	cells, err := f.GetPictureCells(name)
	if err != nil || len(cells) == 0 {
		return
	}
	for _, axis := range cells {
		c, r, cerr := excelize.CellNameToCoordinates(axis)
		if cerr != nil || r > g.Rows || c > g.Cols {
			continue
		}
		pics, perr := f.GetPictures(name, axis)
		if perr != nil || len(pics) == 0 {
			continue
		}
		pic := pics[0]
		mime := pictureMIME(pic.Extension)
		if mime == "" {
			w.addf("Картинка формата %s не поддерживается — перенесите её в макет как PNG или JPEG.", strings.TrimPrefix(pic.Extension, "."))
			continue
		}
		if len(pic.File) > MaxPictureBytes {
			w.addf("Картинка в ячейке %s больше %d КБ — не перенесена.", axis, MaxPictureBytes>>10)
			continue
		}
		// Накрытую объединением позицию рисовать нельзя — у неё нет ячейки;
		// картинка уезжает в якорь объединения.
		ar, ac, ok := anchorOf(g, r-1, c-1)
		if !ok {
			w.addf("Картинка в ячейке %s не перенесена: не нашлась ячейка, которой она принадлежит.", axis)
			continue
		}
		g.Cells[ar][ac].Picture = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(pic.File)
	}
}

// MaxPictureBytes — потолок размера переносимой картинки (256 КБ). Больше —
// data-URI в YAML перевешивает пользу.
const MaxPictureBytes = 256 << 10

func pictureMIME(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	}
	return ""
}

// anchorOf находит якорную ячейку объединения, накрывшего позицию (r,c).
//
// Проверять «не накрыта» мало: слева в той же строке легко оказывается ячейка
// вне этого объединения, и картинка уезжала бы в неё. Например, при объединении
// B2:C3 позиция C3 нашла бы A3 — соседнюю ячейку, к объединению не относящуюся.
// Поэтому кандидат обязан ещё и накрывать (r,c) своим спаном.
func anchorOf(g *grid, r, c int) (int, int, bool) {
	if !g.Covered[r][c] {
		return r, c, true
	}
	for rr := r; rr >= 0; rr-- {
		for cc := c; cc >= 0; cc-- {
			if g.Covered[rr][cc] {
				continue
			}
			a := g.Cells[rr][cc]
			if rr+max(a.RowSpan, 1)-1 >= r && cc+max(a.ColSpan, 1)-1 >= c {
				return rr, cc, true
			}
		}
	}
	return 0, 0, false
}

// trim срезает пустые хвостовые строки и колонки. Пустая — без текста,
// картинки, рамок, заливки и объединения: раздутый «использованный диапазон»
// иначе дал бы макет из сотни пустых строк.
func trim(g *grid) {
	for g.Rows > 0 && rowBlank(g, g.Rows-1) {
		g.Rows--
	}
	for g.Cols > 0 && colBlank(g, g.Cols-1) {
		g.Cols--
	}
	g.Cells = g.Cells[:g.Rows]
	g.Covered = g.Covered[:g.Rows]
	for r := range g.Cells {
		g.Cells[r] = g.Cells[r][:g.Cols]
		g.Covered[r] = g.Covered[r][:g.Cols]
	}
}

func rowBlank(g *grid, r int) bool {
	for c := 0; c < g.Cols; c++ {
		if !cellBlank(g, r, c) {
			return false
		}
	}
	return true
}

func colBlank(g *grid, c int) bool {
	for r := 0; r < g.Rows; r++ {
		if !cellBlank(g, r, c) {
			return false
		}
	}
	return true
}

func cellBlank(g *grid, r, c int) bool {
	if g.Covered[r][c] {
		return false // накрытая позиция — часть живого объединения
	}
	cell := g.Cells[r][c]
	return cell.Text == "" && cell.Picture == "" && cell.BackColor == "" &&
		cell.Borders.IsZero() && cell.ColSpan <= 1 && cell.RowSpan <= 1
}

// readSizes переносит ширины колонок (символы Excel → px) и высоты строк
// (пункты → px). Высота пишется только там, где отличается от умолчания листа:
// иначе height: попал бы в каждую строку макета.
func readSizes(f *excelize.File, name string, g *grid) {
	g.ColW = make([]string, g.Cols)
	for c := 0; c < g.Cols; c++ {
		col, err := excelize.ColumnNumberToName(c + 1)
		if err != nil {
			continue
		}
		if wch, werr := f.GetColWidth(name, col); werr == nil && wch > 0 {
			g.ColW[c] = fmt.Sprintf("%dpx", int(math.Round(wch*7+5)))
		}
	}

	defaultRowH := 15.0
	if props, err := f.GetSheetProps(name); err == nil && props.DefaultRowHeight != nil && *props.DefaultRowHeight > 0 {
		defaultRowH = *props.DefaultRowHeight
	}
	g.RowH = make([]string, g.Rows)
	for r := 0; r < g.Rows; r++ {
		h, err := f.GetRowHeight(name, r+1)
		if err != nil || h <= 0 || math.Abs(h-defaultRowH) < 0.5 {
			continue
		}
		g.RowH[r] = fmt.Sprintf("%dpx", int(math.Round(h*96/72)))
	}
}

// readPage переносит формат листа, ориентацию и поля (дюймы → мм).
func readPage(f *excelize.File, name string, w *warnings) *sheet.PageSetup {
	ps := sheet.DefaultPageSetup()
	if pl, err := f.GetPageLayout(name); err == nil {
		if pl.Orientation != nil && *pl.Orientation != "" {
			ps.Orientation = *pl.Orientation
		}
		if pl.Size != nil {
			if format := paperFormat(*pl.Size); format != "" {
				ps.Format = format
			} else {
				w.addf("Размер бумаги листа (код %d) не распознан — в макете стоит %s, поправьте при необходимости.", *pl.Size, ps.Format)
			}
		}
	}
	if m, err := f.GetPageMargins(name); err == nil {
		setMM(&ps.MarginsMM.Left, m.Left)
		setMM(&ps.MarginsMM.Right, m.Right)
		setMM(&ps.MarginsMM.Top, m.Top)
		setMM(&ps.MarginsMM.Bottom, m.Bottom)
	}
	return &ps
}

func setMM(dst *float64, inches *float64) {
	if inches == nil || *inches < 0 {
		return
	}
	*dst = math.Round(*inches*25.4*10) / 10
}

// paperFormat переводит код размера бумаги Excel в имя формата sheet.PageSetup.
func paperFormat(size int) string {
	switch size {
	case 1, 2:
		return "Letter"
	case 5:
		return "Legal"
	case 8:
		return "A3"
	case 9:
		return "A4"
	case 11:
		return "A5"
	}
	return ""
}

// readDefinedNames собирает именованные диапазоны листа, сведённые к строкам.
// Служебные имена Excel пропускаются — кроме «сквозных строк», которые как раз
// и означают повтор шапки на каждой странице.
func readDefinedNames(f *excelize.File, name string, rows int) (named, titles []namedRange) {
	for _, dn := range f.GetDefinedName() {
		// В одном имени может быть несколько диапазонов через запятую
		// (у «сквозных строк» это строки плюс колонки — колонки нас не касаются).
		for _, part := range strings.Split(dn.RefersTo, ",") {
			sheetName, ref, ok := splitRef(part)
			if !ok || !strings.EqualFold(sheetName, name) {
				continue
			}
			top, bottom, ok := rowRange(ref)
			if !ok || top > rows {
				continue
			}
			bottom = min(bottom, rows)
			nr := namedRange{Name: dn.Name, Top: top - 1, Bottom: bottom - 1}
			switch {
			case strings.EqualFold(dn.Name, "_xlnm.Print_Titles"):
				titles = append(titles, nr)
			case strings.HasPrefix(dn.Name, "_xlnm."):
				// Print_Area и прочая служебка областями не являются.
			default:
				named = append(named, nr)
			}
		}
	}
	return named, titles
}

// splitRef разбирает ссылку вида «Лист1!$A$1:$D$3» или «'Мой лист'!$1:$3».
func splitRef(ref string) (sheetName, cells string, ok bool) {
	ref = strings.TrimSpace(ref)
	i := strings.LastIndex(ref, "!")
	if i < 0 {
		return "", "", false
	}
	sheetName, cells = strings.TrimSpace(ref[:i]), strings.TrimSpace(ref[i+1:])
	if strings.HasPrefix(sheetName, "'") && strings.HasSuffix(sheetName, "'") && len(sheetName) >= 2 {
		sheetName = strings.ReplaceAll(sheetName[1:len(sheetName)-1], "''", "'")
	}
	return sheetName, cells, sheetName != "" && cells != ""
}

// rowRange сводит диапазон к номерам строк (1-based). Ссылка на целые колонки
// («$A:$C») строк не задаёт и отбрасывается.
func rowRange(ref string) (top, bottom int, ok bool) {
	ref = strings.ReplaceAll(ref, "$", "")
	first, last := ref, ref
	if i := strings.Index(ref, ":"); i >= 0 {
		first, last = ref[:i], ref[i+1:]
	}
	top, ok = refRow(first)
	if !ok {
		return 0, 0, false
	}
	bottom, ok = refRow(last)
	if !ok {
		return 0, 0, false
	}
	if top > bottom {
		top, bottom = bottom, top
	}
	return top, bottom, true
}

func refRow(ref string) (int, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(ref); err == nil {
		return n, n > 0
	}
	if _, r, err := excelize.CellNameToCoordinates(ref); err == nil {
		return r, true
	}
	return 0, false
}
