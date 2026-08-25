package xlsximport

import (
	"math"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ivantit66/onebase/internal/printform"
)

// styleCache переносит оформление ячейки Excel в ячейку макета.
//
// Разбор стиля кэшируется по идентификатору: на бланке из сотен ячеек стилей
// обычно единицы, а GetStyle каждый раз пересобирает структуру из XML.
type styleCache struct {
	f    *excelize.File
	w    *warnings
	m    map[int]*excelize.Style
	base baseFont
}

// baseFont — шрифт книги по умолчанию. Нужен, чтобы не штамповать fontSize и
// fontFamily в каждую ячейку: Excel отдаёт полный стиль даже там, где задана
// одна рамка, и без этого отсечения макет обрастал бы «fontFamily: Calibri»
// на каждой ячейке таблицы.
type baseFont struct {
	Family string
	Size   float64
}

func newStyleCache(f *excelize.File, w *warnings) *styleCache {
	sc := &styleCache{f: f, w: w, m: make(map[int]*excelize.Style)}
	sc.base = baseFont{Family: "Calibri", Size: 11}
	if st, err := f.GetStyle(0); err == nil && st != nil && st.Font != nil {
		if st.Font.Family != "" {
			sc.base.Family = st.Font.Family
		}
		if st.Font.Size > 0 {
			sc.base.Size = st.Font.Size
		}
	}
	return sc
}

func (s *styleCache) get(id int) *excelize.Style {
	if st, ok := s.m[id]; ok {
		return st
	}
	st, err := s.f.GetStyle(id)
	if err != nil {
		st = nil
	}
	s.m[id] = st
	return st
}

// apply накладывает стиль Excel на ячейку макета.
func (s *styleCache) apply(id int, cell *printform.LayoutCell) {
	st := s.get(id)
	if st == nil {
		return
	}
	s.applyFont(st.Font, cell)
	s.applyFill(st.Fill, cell)
	s.applyAlignment(st.Alignment, cell)
	s.applyBorders(st.Border, cell)
}

func (s *styleCache) applyFont(fnt *excelize.Font, cell *printform.LayoutCell) {
	if fnt == nil {
		return
	}
	cell.Bold = fnt.Bold
	cell.Italic = fnt.Italic
	if fnt.Size > 0 && math.Abs(fnt.Size-s.base.Size) >= 0.5 {
		cell.FontSize = int(math.Round(fnt.Size))
	}
	if fnt.Family != "" && !strings.EqualFold(fnt.Family, s.base.Family) {
		cell.FontFamily = fnt.Family
	}
	if c := cssColor(fnt.Color); c != "" && c != "#000000" {
		cell.TextColor = c
	}
}

func (s *styleCache) applyFill(fill excelize.Fill, cell *printform.LayoutCell) {
	// Pattern 0 — «нет заливки»; всё остальное считаем сплошной заливкой первым
	// цветом: узорных заливок макет не умеет, а цвет фона передать важнее.
	if fill.Type != "pattern" || fill.Pattern == 0 || len(fill.Color) == 0 {
		return
	}
	c := cssColor(fill.Color[0])
	if c == "" || c == "#FFFFFF" {
		return
	}
	cell.BackColor = c
	if fill.Pattern > 1 {
		s.w.add("Узорная заливка не переносится — взят её основной цвет.")
	}
}

func (s *styleCache) applyAlignment(al *excelize.Alignment, cell *printform.LayoutCell) {
	if al == nil {
		return
	}
	switch strings.ToLower(al.Horizontal) {
	case "left", "center", "right", "justify":
		cell.Align = strings.ToLower(al.Horizontal)
	case "centercontinuous", "distributed":
		cell.Align = "center"
	}
	switch strings.ToLower(al.Vertical) {
	case "top":
		cell.VAlign = "top"
	case "center", "justify", "distributed":
		// В CSS вертикальная середина — middle, а не center (см. sheet/html.go,
		// значение подставляется в vertical-align как есть).
		cell.VAlign = "middle"
	case "bottom":
		cell.VAlign = "bottom"
	}
	if al.TextRotation != 0 {
		s.w.add("Повёрнутый текст не переносится — в макете он выводится горизонтально.")
	}
}

func (s *styleCache) applyBorders(borders []excelize.Border, cell *printform.LayoutCell) {
	if len(borders) == 0 {
		return
	}
	var cb printform.CellBorders
	color := ""
	for _, b := range borders {
		style := borderStyle(b.Style)
		side := strings.ToLower(b.Type)
		if side == "diagonaldown" || side == "diagonalup" {
			if style != "" {
				s.w.add("Диагональные линии в ячейках не переносятся.")
			}
			continue
		}
		if style == "" {
			continue
		}
		switch side {
		case "left":
			cb.Left = style
		case "top":
			cb.Top = style
		case "right":
			cb.Right = style
		case "bottom":
			cb.Bottom = style
		}
		if color == "" {
			color = cssColor(b.Color)
		}
	}
	if cb.IsZero() {
		return
	}
	cell.Borders = &cb
	if color != "" && color != "#000000" {
		cell.BorderColor = color
	}
}

// borderStyle отображает толщину рамки Excel (0–13) на три градации, которые
// умеет рендер: thin / medium / thick. Пунктиры и штрихи схлопываются в
// сплошные той же весомости — тип линии макет не различает.
func borderStyle(style int) string {
	switch style {
	case 1, 3, 4, 7, 9, 11:
		return "thin"
	case 2, 8, 10, 12, 13:
		return "medium"
	case 5, 6:
		return "thick"
	}
	return ""
}

// cssColor нормализует цвет Excel (RGB или ARGB, с решёткой или без) в CSS-вид
// «#RRGGBB». Тематические цвета (заданные индексом темы, без RGB) не переносятся.
func cssColor(c string) string {
	c = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c), "#"))
	if len(c) == 8 {
		c = c[2:] // ARGB → RGB
	}
	if len(c) != 6 {
		return ""
	}
	for _, r := range c {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return ""
		}
	}
	return "#" + strings.ToUpper(c)
}
