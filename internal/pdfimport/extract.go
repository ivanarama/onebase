package pdfimport

import (
	"fmt"
	"math"

	pdf "github.com/ivantit66/onebase/internal/pdfimport/pdfparse"
)

// textRun — один кусочек текста на странице с координатами в пунктах (pt).
// Координаты в системе PDF: X слева направо, Y снизу вверх. dslipak/pdf
// разбивает текст на очень мелкие runs (часто по одному глифу), поэтому
// кластеризацию в слова/ячейки делаем сами (grid.go).
type textRun struct {
	X, Y     float64 // левый-нижний угол базовой линии (pt)
	W        float64 // ширина глифа (pt)
	FontSize float64 // кегль (pt)
	Font     string  // имя шрифта (BaseFont без сабсет-префикса)
	Bold     bool
	Italic   bool
	S        string // UTF-8 текст
}

// lineSeg — горизонтальный или вертикальный отрезок линии сетки (pt).
// Координаты PDF (Y снизу вверх). Диагонали отбрасываются на этапе извлечения.
type lineSeg struct {
	X1, Y1, X2, Y2 float64
	Width          float64 // толщина линии (pt), из оператора w
	Horizontal     bool
}

// Верхние границы объёма извлечения — защита от OOM на враждебном/распухшем PDF
// (таймаут аллокацию не останавливает). Решение по обработке превышения разное:
//   - maxRuns: текст частично полезен → молча обрезаем сбор runs (черновик
//     остаётся пригодным к доводке, просто неполный).
//   - maxLines: сетка из десятков тысяч линий всё равно бессмысленна и опасна
//     для buildFromGrid → возвращаем ErrParse (честнее, чем тихо отдать кашу).
const (
	maxRuns  = 200000
	maxLines = 50000
)

// pageGeom — геометрия страницы из словаря Page.
type pageGeom struct {
	MediaX0, MediaY0 float64
	MediaX1, MediaY1 float64
	Rotate           int
}

func (g pageGeom) widthPt() float64  { return g.MediaX1 - g.MediaX0 }
func (g pageGeom) heightPt() float64 { return g.MediaY1 - g.MediaY0 }

// extractedPage — сырые данные одной страницы: текст, линии, геометрия.
type extractedPage struct {
	Runs  []textRun
	Lines []lineSeg
	Geom  pageGeom
}

// extractPage извлекает из страницы PDF текстовые runs и отрезки линий сетки.
//
// Линии сетки 1С/Excel рисуются операторами пути m/l/S (см. спайк: 282 m/l на
// УПД), а НЕ заливками re — поэтому Content().Rect здесь недостаточно и мы сами
// проходим content stream через экспортированный pdf.Interpret. Текстовые runs
// берём из p.Content().Text (там уже применён ToUnicode CMap — кириллица сабсет-
// шрифтов 1С декодируется верно, спайк подтвердил 0 мусора).
func extractPage(r *pdf.Reader, pageNum int) (*extractedPage, error) {
	p := r.Page(pageNum)
	if p.V.IsNull() {
		return nil, errPageNotFound
	}

	geom := readGeom(p)

	out := &extractedPage{Geom: geom}

	// Текстовые runs — через высокоуровневый Content() (ToUnicode уже применён).
	content := p.Content()
	for _, t := range content.Text {
		if t.S == "" {
			continue
		}
		if len(out.Runs) >= maxRuns {
			// Враждебный/распухший PDF: обрезаем сбор — текст частично полезен.
			break
		}
		bold, italic := fontFlags(t.Font)
		out.Runs = append(out.Runs, textRun{
			X:        t.X,
			Y:        t.Y,
			W:        t.W,
			FontSize: t.FontSize,
			Font:     t.Font,
			Bold:     bold,
			Italic:   italic,
			S:        t.S,
		})
	}

	// Отрезки линий — проход по content stream: m задаёт текущую точку, l ведёт
	// линию; учитываем только под оператором обводки (S/s/B/b) — заливочные
	// контуры (f/W n) пропускаем. Для устойчивости копим все m/l-сегменты, а
	// фильтруем по обводке через флаг последней операции рисования пути.
	out.Lines = extractLines(p)
	if len(out.Lines) > maxLines {
		// Десятки тысяч линий — сетка бессмысленна и опасна для buildFromGrid.
		return nil, fmt.Errorf("%w: слишком много линий (%d)", ErrParse, len(out.Lines))
	}

	return out, nil
}

// readGeom читает MediaBox и Rotate (с учётом наследования от родителя).
func readGeom(p pdf.Page) pageGeom {
	g := pageGeom{MediaX1: 595.32, MediaY1: 841.92} // A4 по умолчанию (pt)
	mb := findInherited(p, "MediaBox")
	if mb.Len() == 4 {
		g.MediaX0 = mb.Index(0).Float64()
		g.MediaY0 = mb.Index(1).Float64()
		g.MediaX1 = mb.Index(2).Float64()
		g.MediaY1 = mb.Index(3).Float64()
	}
	rot := findInherited(p, "Rotate")
	g.Rotate = int(rot.Int64())
	return g
}

// findInherited повторяет логику Page.findInherited (она не экспортирована):
// идёт вверх по цепочке Parent, пока не найдёт ключ.
func findInherited(p pdf.Page, key string) pdf.Value {
	for v := p.V; !v.IsNull(); v = v.Key("Parent") {
		if r := v.Key(key); !r.IsNull() {
			return r
		}
	}
	return pdf.Value{}
}

// pathBuilder копит сегменты текущего пути до оператора рисования.
type pathBuilder struct {
	curX, curY float64
	startX     float64
	startY     float64
	segs       []lineSeg // накопленные сегменты текущего подпути
	lineWidth  float64
	out        *[]lineSeg
}

// extractLines проходит content stream страницы и собирает горизонтальные и
// вертикальные отрезки, нарисованные операторами обводки (S/s/B/b). Заливочные
// прямоугольники-границы (re ... f) тоже превращаем в 4 стороны: тонкие границы
// 1С иногда заливает. Диагонали и кривые (c/v) отбрасываются.
func extractLines(p pdf.Page) []lineSeg {
	strm := p.V.Key("Contents")
	var out []lineSeg
	pb := &pathBuilder{lineWidth: 1, out: &out}

	pdf.Interpret(strm, func(stk *pdf.Stack, op string) {
		n := stk.Len()
		args := make([]pdf.Value, n)
		for i := n - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		switch op {
		case "w": // set line width
			if len(args) == 1 {
				pb.lineWidth = args[0].Float64()
			}
		case "m": // moveto — начало нового подпути
			if len(args) == 2 {
				pb.curX, pb.curY = args[0].Float64(), args[1].Float64()
				pb.startX, pb.startY = pb.curX, pb.curY
			}
		case "l": // lineto — добавить сегмент
			if len(args) == 2 {
				x, y := args[0].Float64(), args[1].Float64()
				pb.addSeg(pb.curX, pb.curY, x, y)
				pb.curX, pb.curY = x, y
			}
		case "re": // прямоугольник — 4 стороны как сегменты
			if len(args) == 4 {
				x, y := args[0].Float64(), args[1].Float64()
				w, h := args[2].Float64(), args[3].Float64()
				pb.addRect(x, y, w, h)
				pb.curX, pb.curY = x, y
				pb.startX, pb.startY = x, y
			}
		case "h": // closepath
			pb.addSeg(pb.curX, pb.curY, pb.startX, pb.startY)
			pb.curX, pb.curY = pb.startX, pb.startY
		case "c", "v", "y": // кривые Безье — путь не прямой, сбрасываем накопленное
			pb.segs = nil
		case "S", "s", "B", "b", "B*", "b*": // обводка — фиксируем сегменты
			pb.flush(true)
		case "f", "F", "f*": // заливка без обводки — границы-заливки тоже считаем
			pb.flushFills()
		case "n": // конец пути без рисования (часто W n — клип) — отбрасываем
			pb.segs = nil
		}
	})
	return out
}

// addSeg добавляет сегмент в текущий путь, если он гориз./вертик. (с допуском).
func (pb *pathBuilder) addSeg(x1, y1, x2, y2 float64) {
	dx := math.Abs(x2 - x1)
	dy := math.Abs(y2 - y1)
	const eps = 0.6 // pt: допуск «почти прямой»
	if dy <= eps && dx > eps {
		pb.segs = append(pb.segs, lineSeg{X1: x1, Y1: y1, X2: x2, Y2: y1, Width: pb.lineWidth, Horizontal: true})
	} else if dx <= eps && dy > eps {
		pb.segs = append(pb.segs, lineSeg{X1: x1, Y1: y1, X2: x1, Y2: y2, Width: pb.lineWidth, Horizontal: false})
	}
	// диагонали игнорируем
}

// addRect раскладывает прямоугольник на 4 стороны.
func (pb *pathBuilder) addRect(x, y, w, h float64) {
	x2, y2 := x+w, y+h
	pb.segs = append(pb.segs,
		lineSeg{X1: x, Y1: y, X2: x2, Y2: y, Width: pb.lineWidth, Horizontal: true},
		lineSeg{X1: x, Y1: y2, X2: x2, Y2: y2, Width: pb.lineWidth, Horizontal: true},
		lineSeg{X1: x, Y1: y, X2: x, Y2: y2, Width: pb.lineWidth, Horizontal: false},
		lineSeg{X1: x2, Y1: y, X2: x2, Y2: y2, Width: pb.lineWidth, Horizontal: false},
	)
}

// flush переносит накопленные сегменты в результат (stroke=true) и очищает путь.
func (pb *pathBuilder) flush(stroke bool) {
	if stroke {
		*pb.out = append(*pb.out, pb.segs...)
	}
	pb.segs = nil
}

// flushFills переносит сегменты заливки. Заливочные прямоугольники-границы 1С
// тонкие (одна из сторон узкая) — но могут быть и крупные фоны. Чтобы не превратить
// фоновую заливку всей таблицы в «линии», берём только сегменты, у которых
// перпендикулярный размер прямоугольника был мал. На уровне сегмента эту
// информацию мы теряем, поэтому фильтрацию делает grid.go (тонкие полосы → линии).
func (pb *pathBuilder) flushFills() {
	*pb.out = append(*pb.out, pb.segs...)
	pb.segs = nil
}

// fontFlags определяет жирность/курсив по имени шрифта (BaseFont).
func fontFlags(font string) (bold, italic bool) {
	lf := toLowerASCII(font)
	bold = containsAny(lf, "bold", "black", "heavy", "semibold", "demibold")
	italic = containsAny(lf, "italic", "oblique")
	return
}
