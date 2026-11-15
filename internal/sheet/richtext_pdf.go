package sheet

import (
	"bytes"
	"strings"

	"github.com/go-pdf/fpdf"
	"golang.org/x/net/html"
)

// Проекция richtext (Cell.RichHTML) в PDF (план 65, этап 3). fpdf не рендерит
// произвольный HTML, поэтому ограниченный HTML (тот же allowlist, что у
// санитайзера internal/richtext: p/br, b/strong, i/em, u, ul/ol/li, h1-h3,
// blockquote, span/div, a, img[data-URI]) разбирается в последовательность
// блоков и инлайновых сегментов и рисуется текстом нужного начертания + картинки.
//
// Контракт: RichHTML уже санитизирован вызывающим (printform). Парсер устойчив к
// мусору (golang.org/x/net/html толерантен), неизвестные теги трактуются как
// прозрачные обёртки. Вёрстка не пиксель-в-пиксель: текст читаем, форматирование
// базовое (жирный/курсив/абзацы/списки), картинки видны.
//
// Зависимость golang.org/x/net/html — quasi-stdlib (golang.org/x), НЕ bluemonday
// и НЕ onebase-пакет: пакет sheet остаётся нейтральным (см. go list -deps).

// richSegment — один инлайновый сегмент проекции: текст с начертанием, либо
// картинка (data-URI). Сегменты группируются в блоки (richBlock).
type richSegment struct {
	text   string
	bold   bool
	italic bool
	img    string // непустой → сегмент-картинка (data-URI), text игнорируется
}

// richBlock — блок проекции (абзац / элемент списка / заголовок). marker —
// префикс списка («• » или «N. »); indent — отступ в «уровнях» вложенности.
type richBlock struct {
	segs   []richSegment
	marker string
	indent int
	header bool // h1-h3: рисуем жирным крупнее
}

// parseRichHTML разбирает ограниченный HTML в последовательность блоков.
// Возвращает nil на пустом/нечитаемом входе (вызывающий тогда ничего не рисует).
func parseRichHTML(s string) []richBlock {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	root, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return nil
	}
	b := &richBuilder{}
	b.walk(root, richStyle{})
	b.flush()
	// Отбрасываем пустые блоки (без сегментов и без маркера).
	out := b.blocks[:0]
	for _, blk := range b.blocks {
		if len(blk.segs) > 0 || blk.marker != "" {
			out = append(out, blk)
		}
	}
	return out
}

// richStyle — наследуемое инлайновое начертание при обходе дерева.
type richStyle struct {
	bold   bool
	italic bool
	indent int
	header bool
}

// richBuilder накапливает блоки/сегменты при обходе HTML-дерева.
type richBuilder struct {
	blocks  []richBlock
	cur     richBlock
	started bool  // открыт ли текущий блок (для авто-переноса между блочными тегами)
	olCount []int // счётчики нумерации вложенных <ol>
}

// flush завершает текущий блок (если непуст) и начинает новый.
func (b *richBuilder) flush() {
	if b.started || len(b.cur.segs) > 0 || b.cur.marker != "" {
		b.blocks = append(b.blocks, b.cur)
	}
	b.cur = richBlock{}
	b.started = false
}

// addText добавляет текстовый сегмент в текущий блок.
func (b *richBuilder) addText(text string, st richStyle) {
	if text == "" {
		return
	}
	b.cur.indent = st.indent
	b.cur.header = st.header
	b.cur.segs = append(b.cur.segs, richSegment{text: text, bold: st.bold, italic: st.italic})
	b.started = true
}

// addImage добавляет сегмент-картинку.
func (b *richBuilder) addImage(src string, st richStyle) {
	if src == "" {
		return
	}
	b.cur.indent = st.indent
	b.cur.segs = append(b.cur.segs, richSegment{img: src})
	b.started = true
}

// walk обходит узел дерева, накапливая блоки/сегменты.
func (b *richBuilder) walk(n *html.Node, st richStyle) {
	switch n.Type {
	case html.TextNode:
		// Нормализуем пробелы внутри текстового узла (как inline HTML).
		txt := collapseWS(n.Data)
		if txt != "" {
			b.addText(txt, st)
		}
		return
	case html.ElementNode:
		// no-op, обработка ниже
	default:
		// Document/Doctype/Comment — просто спускаемся в детей.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, st)
		}
		return
	}

	tag := strings.ToLower(n.Data)
	childStyle := st
	switch tag {
	case "b", "strong":
		childStyle.bold = true
	case "i", "em":
		childStyle.italic = true
	case "h1", "h2", "h3":
		b.flush()
		childStyle.header = true
		childStyle.bold = true
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, childStyle)
		}
		b.flush()
		return
	case "br":
		b.flush()
		return
	case "p", "div", "blockquote":
		b.flush()
		if tag == "blockquote" {
			childStyle.indent++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, childStyle)
		}
		b.flush()
		return
	case "ul":
		childStyle.indent++
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, childStyle)
		}
		return
	case "ol":
		childStyle.indent++
		b.olCount = append(b.olCount, 0)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, childStyle)
		}
		b.olCount = b.olCount[:len(b.olCount)-1]
		return
	case "li":
		b.flush()
		// Маркер: нумерованный, если внутри <ol> (есть активный счётчик).
		if len(b.olCount) > 0 {
			b.olCount[len(b.olCount)-1]++
			b.cur.marker = itoa(b.olCount[len(b.olCount)-1]) + ". "
		} else {
			b.cur.marker = "• "
		}
		b.cur.indent = childStyle.indent
		b.started = true
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.walk(c, childStyle)
		}
		b.flush()
		return
	case "img":
		if src := attrVal(n, "src"); src != "" {
			b.addImage(src, childStyle)
		}
		return
	}

	// span/u/s/a и неизвестные теги — прозрачные обёртки (инлайн).
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.walk(c, childStyle)
	}
}

// attrVal возвращает значение атрибута key узла (пустое, если нет).
func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// collapseWS сжимает последовательности пробельных символов в один пробел
// (HTML inline-семантика). Края НЕ обрезаются — пробел между инлайн-тегами значим.
func collapseWS(s string) string {
	var sb strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			if !prevSpace {
				sb.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		sb.WriteRune(r)
		prevSpace = false
	}
	return sb.String()
}

// itoa — маленький int→string без strconv-импорта (положительные счётчики списка).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// richIndentMM — горизонтальный отступ одного уровня вложенности (мм).
const richIndentMM = 4.0

// richImageMaxMM — максимальная высота встроенной картинки в проекции (мм),
// чтобы крупный скриншот не занимал страницу целиком в ячейке печатной формы.
const richImageMaxMM = 40.0

// drawRichText рисует проекцию richtext в прямоугольник ячейки (x,y,w,h).
// Возвращает фактическую использованную высоту (мм) — измеритель высоты строки
// вызывает её в «сухом» режиме (measureOnly=true) без вывода в PDF.
//
// Устойчивость: парс/рендер обёрнуты так, что любая паника (битый HTML/картинка)
// гасится — печать документа не падает (recover в вызывающем drawCell/cellHeightMM).
func drawRichText(pdf *fpdf.Fpdf, cell *Cell, x, y, w, h float64, measureOnly bool) float64 {
	blocks := parseRichHTML(cell.RichHTML)
	if len(blocks) == 0 {
		return 0
	}
	baseFS := fontSizeOr(cell)
	avail := w - 2*cellPadMM
	if avail <= 0 {
		avail = w
	}
	family, _ := resolveFont(cell.FontFamily, false, false)

	cy := y + cellPadMM
	for _, blk := range blocks {
		fs := baseFS
		if blk.header {
			fs = baseFS + 2
		}
		lineH := fs * lineGap * ptToMM
		indentMM := float64(blk.indent) * richIndentMM
		blockX := x + cellPadMM + indentMM
		blockAvail := avail - indentMM
		if blockAvail <= 2 {
			blockAvail = avail
		}

		// Маркер списка рисуется перед первой строкой блока.
		markerW := 0.0
		if blk.marker != "" {
			pdf.SetFont(family, "", fs)
			markerW = pdf.GetStringWidth(blk.marker)
			if !measureOnly {
				pdf.SetXY(blockX, cy)
				pdf.CellFormat(markerW, lineH, blk.marker, "", 0, "LM", false, 0, "")
			}
		}

		cy = drawRichBlockSegs(pdf, blk, family, fs, lineH, blockX+markerW, cy, blockAvail-markerW, measureOnly, cell.Underline)
		// Небольшой межблочный зазор.
		cy += lineH * 0.2
	}
	return cy - y
}

// drawRichBlockSegs раскладывает инлайновые сегменты блока в строки по ширине
// avail и рисует (или только измеряет). Картинки выносятся на отдельную строку.
// Возвращает y после последней строки блока.
func drawRichBlockSegs(pdf *fpdf.Fpdf, blk richBlock, family string, fs, lineH, x, y, avail float64, measureOnly, underline bool) float64 {
	if avail <= 1 {
		avail = lineH // деградация — хоть что-то
	}
	cx := x
	cy := y
	lineHasContent := false

	newline := func() {
		cy += lineH
		cx = x
		lineHasContent = false
	}

	for _, seg := range blk.segs {
		if seg.img != "" {
			// Картинка — на новой строке.
			if lineHasContent {
				newline()
			}
			ih := drawRichImage(pdf, seg.img, x, cy, avail, measureOnly)
			if ih > 0 {
				cy += ih
			}
			cx = x
			lineHasContent = false
			continue
		}
		style := ""
		if seg.bold {
			style += "B"
		}
		if seg.italic {
			style += "I"
		}
		if underline {
			style += "U"
		}
		pdf.SetFont(family, style, fs)
		// Разбиваем сегмент на слова и пробелы (отдельные токены) и переносим по
		// ширине. Пробел-разделитель — самостоятельный токен, поэтому ширина строки
		// учитывается корректно при переносе по словам.
		for _, piece := range splitWords(seg.text) {
			pw := pdf.GetStringWidth(piece)
			if cx+pw > x+avail && lineHasContent && strings.TrimSpace(piece) != "" {
				newline()
				pdf.SetFont(family, style, fs)
			}
			// Пробел в начале строки не печатаем.
			if !lineHasContent && strings.TrimSpace(piece) == "" {
				continue
			}
			if !measureOnly {
				pdf.SetXY(cx, cy)
				pdf.CellFormat(pw, lineH, piece, "", 0, "LM", false, 0, "")
			}
			cx += pw
			if strings.TrimSpace(piece) != "" {
				lineHasContent = true
			}
		}
	}
	if lineHasContent {
		cy += lineH
	}
	return cy
}

// splitWords делит строку на токены-слова и токены-пробелы, сохраняя пробелы как
// отдельные элементы (для корректного переноса по словам).
func splitWords(s string) []string {
	var out []string
	var sb strings.Builder
	flush := func() {
		if sb.Len() > 0 {
			out = append(out, sb.String())
			sb.Reset()
		}
	}
	inSpace := false
	for i, r := range s {
		isSpace := r == ' '
		if i == 0 {
			inSpace = isSpace
		}
		if isSpace != inSpace {
			flush()
			inSpace = isSpace
		}
		sb.WriteRune(r)
	}
	flush()
	return out
}

// drawRichImage вписывает data-URI картинку шириной avail (мм), сохраняя
// пропорции и ограничивая высоту richImageMaxMM. Возвращает использованную
// высоту (мм). measureOnly → только высота, без вывода. Сбои декодирования тихо
// игнорируются (картинка не валит печать).
func drawRichImage(pdf *fpdf.Fpdf, src string, x, y, avail float64, measureOnly bool) float64 {
	data, imgType, ok := decodeDataURIImage(src)
	if !ok {
		return 0
	}
	name := pictureCacheName(imgType, data)
	info := pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: imgType}, bytes.NewReader(data))
	if pdf.Err() || info == nil {
		pdf.ClearError()
		return 0
	}
	iw, ih := info.Extent()
	if iw <= 0 || ih <= 0 {
		return 0
	}
	scale := avail / iw
	dw := avail
	dh := ih * scale
	if dh > richImageMaxMM {
		s := richImageMaxMM / dh
		dh *= s
		dw *= s
	}
	if !measureOnly {
		pdf.ImageOptions(name, x, y, dw, dh, false, fpdf.ImageOptions{ImageType: imgType}, 0, "")
		if pdf.Err() {
			pdf.ClearError()
		}
	}
	return dh
}

// richCellHeightMM измеряет высоту, нужную проекции richtext в ячейке шириной cw.
// Используется computeRowHeightsMM. Обёрнута recover — битый HTML не валит расчёт.
func richCellHeightMM(pdf *fpdf.Fpdf, cell *Cell, cw float64) (h float64) {
	defer func() {
		if r := recover(); r != nil {
			pdf.ClearError()
			h = minRowMM
		}
	}()
	used := drawRichText(pdf, cell, 0, 0, cw, 0, true)
	if used <= 0 {
		return minRowMM
	}
	return used + 2*cellPadMM
}
