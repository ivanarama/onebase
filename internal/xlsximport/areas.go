package xlsximport

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/printform"
)

// areas.go — разбиение листа на именованные области макета и разворот строк
// табличной части.
//
// Ключевое решение (план 155): признак «эта строка размножается по ТЧ» берётся
// из самих тегов — ячейка со {{Товары.Количество}} делает свою строку
// repeat-областью по «Товары», а внутри области теги схлопываются до
// {{Количество}}. Нового языка вроде Carbone-овского {d.rows[i]} не заводим:
// внутри repeat-области выражением колонки и так является её голое имя (так же
// пишет панель «Данные» визуального редактора), а InterpolateText уже резолвит
// поля строки. Отличить колонку ТЧ от поля по ссылке ({{Склад.Наименование}})
// по написанию невозможно — поэтому список ТЧ приходит снаружи, из метаданных
// документа, и без него разворот просто не делается.

var reTag = regexp.MustCompile(`\{\{([^}]*)\}\}`)

// reservedPrefixes — приставки языка выражений, которые никогда не означают
// табличную часть (см. printform/binding.go).
var reservedPrefixes = map[string]bool{"итог": true, "константы": true}

// namedRange — именованный диапазон Excel, суженный до строк листа (0-based,
// включительно). Диспетчер имён — прямой аналог именованных областей табличного
// документа 1С, поэтому имя перебивает автоматику.
type namedRange struct {
	Name   string
	Top    int
	Bottom int
}

// block — будущая область макета: диапазон строк, имя и, для строк табличной
// части, имя ТЧ.
type block struct {
	Top    int
	Bottom int
	Name   string
	Source string
	Header bool // повторять на каждой странице (binding.repeat_header)
}

// buildLayout собирает макет из разобранной сетки.
func buildLayout(g *grid, tableParts []string, w *warnings) *printform.LayoutTemplate {
	rowTP := detectRepeatRows(g, tableParts, w)
	blocks := splitBlocks(g, rowTP)
	nameBlocks(g, blocks)
	blocks = markHeader(g, blocks)
	stripPrefixes(g, blocks)

	lt := &printform.LayoutTemplate{Page: g.Page}
	lt.Columns = make([]printform.LayoutColumn, g.Cols)
	for c := 0; c < g.Cols; c++ {
		lt.Columns[c] = printform.LayoutColumn{Width: g.ColW[c]}
	}

	binding := &printform.Binding{}
	for _, b := range blocks {
		lt.Areas = append(lt.Areas, buildArea(g, b))
		binding.Sequence = append(binding.Sequence, b.Name)
		if b.Source != "" {
			binding.Repeat = append(binding.Repeat, printform.RepeatBinding{Area: b.Name, Source: b.Source})
		}
		if b.Header {
			binding.RepeatHeader = b.Name
		}
	}
	lt.Binding = binding
	return lt
}

// buildArea материализует блок строк в область макета.
func buildArea(g *grid, b block) *printform.LayoutArea {
	area := &printform.LayoutArea{Name: b.Name}
	for r := b.Top; r <= b.Bottom; r++ {
		area.Rows = append(area.Rows, printform.LayoutRow{
			Height: g.RowH[r],
			Cells:  rowCells(g, r),
		})
	}
	return area
}

// rowCells выкладывает ячейки строки по порядку колонок. Накрытые объединением
// позиции пропускаются — декларативный движок сам сдвигает индекс колонки под
// спаном. Хвост пустых ячеек отбрасывается: на ширину он не влияет (её задаёт
// columns:), а YAML раздувает.
func rowCells(g *grid, r int) []printform.LayoutCell {
	var cells []printform.LayoutCell
	last := -1
	for c := 0; c < g.Cols; c++ {
		if g.Covered[r][c] {
			continue
		}
		cell := g.Cells[r][c]
		if cell.ColSpan <= 1 {
			cell.ColSpan = 0
		}
		if cell.RowSpan <= 1 {
			cell.RowSpan = 0
		}
		cells = append(cells, cell)
		if !blankCell(cell) {
			last = len(cells) - 1
		}
	}
	return cells[:last+1]
}

func blankCell(c printform.LayoutCell) bool {
	return c.Text == "" && c.Picture == "" && c.BackColor == "" &&
		c.Borders.IsZero() && c.ColSpan == 0 && c.RowSpan == 0
}

// detectRepeatRows определяет для каждой строки имя табличной части, теги
// которой в ней встретились ("" — обычная строка).
func detectRepeatRows(g *grid, tableParts []string, w *warnings) []string {
	out := make([]string, g.Rows)
	if len(tableParts) == 0 {
		if hasDottedTag(g) {
			w.add("Табличные части документа не заданы — строки таблицы не размножаются; добавьте binding.repeat в макет вручную.")
		}
		return out
	}
	found := false
	for r := 0; r < g.Rows; r++ {
		var seen []string
		for c := 0; c < g.Cols; c++ {
			for _, tag := range reTag.FindAllStringSubmatch(g.Cells[r][c].Text, -1) {
				tp := matchTablePart(tagPrefix(tag[1]), tableParts)
				if tp != "" && !containsFold(seen, tp) {
					seen = append(seen, tp)
				}
			}
		}
		if len(seen) == 0 {
			continue
		}
		if len(seen) > 1 {
			w.addf("В строке %d встретились теги нескольких табличных частей (%s) — строка размножена по «%s».",
				r+1, strings.Join(seen, ", "), seen[0])
		}
		out[r] = seen[0]
		found = true
	}
	if !found {
		w.add("Строк табличной части не найдено: пометьте строку таблицы тегами вида {{Товары.Количество}}, чтобы она размножалась.")
	}
	return out
}

// hasDottedTag сообщает, есть ли на листе тег с приставкой, которая могла бы
// быть табличной частью (нужно только для предупреждения).
func hasDottedTag(g *grid) bool {
	for r := 0; r < g.Rows; r++ {
		for c := 0; c < g.Cols; c++ {
			for _, tag := range reTag.FindAllStringSubmatch(g.Cells[r][c].Text, -1) {
				if p := tagPrefix(tag[1]); p != "" && !reservedPrefixes[strings.ToLower(p)] {
					return true
				}
			}
		}
	}
	return false
}

// tagPrefix возвращает первый сегмент выражения тега — то, что стоит до первой
// точки, без части форматирования. Для «Товары.Цена | number:2» это «Товары».
func tagPrefix(tag string) string {
	expr := tag
	if i := strings.IndexByte(expr, '|'); i >= 0 {
		expr = expr[:i]
	}
	expr = strings.TrimSpace(expr)
	i := strings.IndexByte(expr, '.')
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(expr[:i])
}

// matchTablePart сопоставляет приставку тега со списком ТЧ (без учёта регистра),
// отсекая служебные приставки языка выражений.
func matchTablePart(prefix string, tableParts []string) string {
	if prefix == "" || reservedPrefixes[strings.ToLower(prefix)] {
		return ""
	}
	for _, tp := range tableParts {
		if strings.EqualFold(tp, prefix) {
			return tp
		}
	}
	return ""
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// splitBlocks режет лист на блоки строк: по границам именованных диапазонов и
// по смене табличной части.
func splitBlocks(g *grid, rowTP []string) []block {
	key := func(r int) string {
		return fmt.Sprintf("%d|%s", namedAt(g.Named, r), rowTP[r])
	}
	var blocks []block
	start := 0
	for r := 1; r <= g.Rows; r++ {
		if r == g.Rows || key(r) != key(start) {
			blocks = append(blocks, block{Top: start, Bottom: r - 1, Source: rowTP[start]})
			start = r
		}
	}
	return blocks
}

// namedAt возвращает индекс именованного диапазона, накрывшего строку (-1 — нет).
func namedAt(named []namedRange, r int) int {
	for i, n := range named {
		if r >= n.Top && r <= n.Bottom {
			return i
		}
	}
	return -1
}

// nameBlocks раздаёт областям имена: имя из Диспетчера имён Excel, иначе
// Шапка / Строка / Подвал.
func nameBlocks(g *grid, blocks []block) {
	used := make(map[string]bool)
	unique := func(name string) string {
		if !used[strings.ToLower(name)] {
			used[strings.ToLower(name)] = true
			return name
		}
		for i := 2; ; i++ {
			try := name + strconv.Itoa(i)
			if !used[strings.ToLower(try)] {
				used[strings.ToLower(try)] = true
				return try
			}
		}
	}

	repeats := 0
	for _, b := range blocks {
		if b.Source != "" {
			repeats++
		}
	}

	plain := 0
	for _, b := range blocks {
		if b.Source == "" {
			plain++
		}
	}

	plainSeen := 0
	for i := range blocks {
		b := &blocks[i]
		if idx := namedAt(g.Named, b.Top); idx >= 0 {
			b.Name = unique(g.Named[idx].Name)
			continue
		}
		if b.Source != "" {
			if repeats == 1 {
				b.Name = unique("Строка")
			} else {
				b.Name = unique("Строка" + b.Source)
			}
			continue
		}
		plainSeen++
		switch {
		case len(blocks) == 1:
			b.Name = unique("Макет")
		case plainSeen == 1:
			b.Name = unique("Шапка")
		case plainSeen == plain:
			b.Name = unique("Подвал")
		default:
			b.Name = unique("Блок" + strconv.Itoa(plainSeen))
		}
	}
}

// markHeader помечает область, повторяемую на каждой странице PDF.
//
// Источников два. Первый — «Сквозные строки» Excel (_xlnm.Print_Titles): это
// ровно та же мысль, что и binding.repeat_header, и если пользователь их задал,
// гадать не нужно. Второй — эвристика для бланка, где их не задали: строка
// прямо перед строкой ТЧ, если в ней нет тегов и есть хотя бы две заполненные
// ячейки, — это шапка таблицы в 99% накладных. Ошибка эвристики видна только на
// многостраничной печати и лечится правкой одной строки YAML.
func markHeader(g *grid, blocks []block) []block {
	if len(g.PrintTitles) > 0 {
		for i := range blocks {
			b := &blocks[i]
			for _, pt := range g.PrintTitles {
				if b.Top >= pt.Top && b.Bottom <= pt.Bottom {
					b.Header = true
					return blocks
				}
			}
		}
	}
	if len(g.Named) > 0 {
		return blocks // разметку задал человек — не домысливаем
	}

	first := -1
	for i, b := range blocks {
		if b.Source != "" {
			first = i
			break
		}
	}
	if first <= 0 {
		return blocks
	}
	prev := &blocks[first-1]
	row := prev.Bottom
	if !headerRow(g, row) {
		return blocks
	}
	if prev.Top == row {
		prev.Name = "ШапкаТаблицы"
		prev.Header = true
		return blocks
	}
	// Последняя строка предыдущего блока отрезается в отдельную область.
	prev.Bottom = row - 1
	out := make([]block, 0, len(blocks)+1)
	out = append(out, blocks[:first]...)
	out = append(out, block{Top: row, Bottom: row, Name: "ШапкаТаблицы", Header: true})
	out = append(out, blocks[first:]...)
	return out
}

// headerRow — «похоже на шапку таблицы»: без тегов и не меньше двух заполненных
// ячеек.
func headerRow(g *grid, r int) bool {
	filled := 0
	for c := 0; c < g.Cols; c++ {
		t := g.Cells[r][c].Text
		if t == "" {
			continue
		}
		if strings.Contains(t, "{{") {
			return false
		}
		filled++
	}
	return filled >= 2
}

// stripPrefixes убирает приставку табличной части у тегов внутри repeat-области:
// {{Товары.Цена | number:2}} → {{Цена | number:2}}. Внутри области выражением
// колонки является её голое имя.
func stripPrefixes(g *grid, blocks []block) {
	for _, b := range blocks {
		if b.Source == "" {
			continue
		}
		for r := b.Top; r <= b.Bottom; r++ {
			for c := 0; c < g.Cols; c++ {
				cell := &g.Cells[r][c]
				if strings.Contains(cell.Text, "{{") {
					cell.Text = stripTagPrefix(cell.Text, b.Source)
				}
			}
		}
	}
}

// stripTagPrefix снимает приставку tp у всех тегов текста, сохраняя часть
// форматирования и пробельную раскладку тега.
func stripTagPrefix(text, tp string) string {
	return reTag.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[2 : len(m)-2]
		expr := inner
		if i := strings.IndexByte(inner, '|'); i >= 0 {
			expr = inner[:i]
		}
		lead := len(expr) - len(strings.TrimLeft(expr, " 	"))
		body := expr[lead:]
		if !strings.HasPrefix(strings.ToLower(body), strings.ToLower(tp)+".") {
			return m
		}
		// Хвост тега (пробелы и часть форматирования) сохраняется как был:
		// вырезается ровно приставка с точкой.
		cut := lead + len(tp) + 1
		return "{{" + inner[:lead] + inner[cut:] + "}}"
	})
}
