package pdfimport

import (
	"math"
	"sort"
	"strings"
)

// word — кластер из соседних глифов-runs одной строки: связный текст с общими
// координатами. dslipak/pdf отдаёт текст по одному глифу (с разрывами внутри
// слова), поэтому сначала склеиваем глифы в слова по X-зазору и общей Y-строке.
type word struct {
	X0, X1   float64 // левый/правый край (pt)
	Y        float64 // базовая линия (pt, исходная PDF-система, снизу вверх)
	FontSize float64
	Bold     bool
	Italic   bool
	S        string
}

// clusterWords склеивает текстовые runs в слова, СОХРАНЯЯ порядок content stream.
//
// Критично (спайк): 1С выводит текст операторами TJ с попозиционированием
// глифов; dslipak/pdf отдаёт каждому глифу X = старт TJ-сегмента, а ширину W=0.
// Поэтому X внутри слова почти не меняется и НЕ монотонен — сортировать глифы по
// X нельзя (порядок букв перемешается). Читаемый порядок задаёт сам поток, мы
// его не нарушаем. Слово рвём, когда X прыгает на заметную величину (новый
// позиционированный сегмент) или меняется строка (Y). Координата слова — X,Y
// первого глифа (это надёжный старт сегмента).
func clusterWords(runs []textRun) []word {
	if len(runs) == 0 {
		return nil
	}

	var words []word
	var cur *word
	var lastX, lastY float64

	flush := func() {
		if cur != nil {
			cur.S = collapseSpaces(cur.S)
			if cur.S != "" {
				// Оценка правого края по длине текста и кеглю (W=0 в потоке).
				cur.X1 = cur.X0 + estTextWidth(cur.S, cur.FontSize)
				words = append(words, *cur)
			}
		}
		cur = nil
	}

	for _, r := range runs {
		dx := r.X - lastX
		dy := r.Y - lastY
		// Порог X-прыжка, рвущего слово: пропорционален кеглю (новый сегмент TJ).
		jumpThreshold := maxF(r.FontSize*0.6, wordGapPt)
		lineChange := math.Abs(dy) > maxF(r.FontSize, 6)*0.4
		newToken := cur == nil || lineChange || math.Abs(dx) > jumpThreshold

		if newToken {
			flush()
			cur = &word{
				X0: r.X, Y: r.Y,
				FontSize: r.FontSize, Bold: r.Bold, Italic: r.Italic,
				S: r.S,
			}
		} else {
			// Глиф продолжает текущее слово (X почти не изменился). Пробелы внутри
			// слова уже приходят отдельным глифом " ", отдельной вставки не нужно.
			cur.S += r.S
			if r.FontSize > cur.FontSize {
				cur.FontSize = r.FontSize
			}
			cur.Bold = cur.Bold || r.Bold
			cur.Italic = cur.Italic || r.Italic
		}
		lastX = r.X
		lastY = r.Y
	}
	flush()
	return words
}

// estTextWidth оценивает ширину строки в pt по числу рун и кеглю (W в потоке = 0).
// Грубая средняя ширина символа ≈ 0.5 кегля; достаточно для отнесения слова к
// региону и эвристики выравнивания.
func estTextWidth(s string, fontSize float64) float64 {
	if fontSize <= 0 {
		fontSize = 8
	}
	return float64(len([]rune(s))) * fontSize * 0.5
}

// collapseSpaces схлопывает повторяющиеся пробелы и тримит края.
func collapseSpaces(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// assignWordsToRegions распределяет слова по регионам сетки и заполняет text/
// fontSize/bold/italic/minX/maxX каждого региона. Слово относится к региону,
// если его центр попадает в прямоугольник региона. Внутри региона слова
// сортируются по Y (сверху вниз), затем X, и склеиваются через пробел/перенос.
func assignWordsToRegions(words []word, regions []*region, flipY func(float64) float64) {
	// Группируем слова по региону.
	type wl struct{ ws []word }
	buckets := make(map[*region]*wl)

	for _, w := range words {
		cx := (w.X0 + w.X1) / 2
		cy := flipY(w.Y) // в top-down систему
		reg := findRegion(regions, cx, cy)
		if reg == nil {
			continue
		}
		b := buckets[reg]
		if b == nil {
			b = &wl{}
			buckets[reg] = b
		}
		b.ws = append(b.ws, w)
	}

	for reg, b := range buckets {
		sort.Slice(b.ws, func(i, j int) bool {
			if math.Abs(b.ws[i].Y-b.ws[j].Y) > maxF(b.ws[i].FontSize, 6)*0.4 {
				return b.ws[i].Y > b.ws[j].Y // выше (больший Y) раньше
			}
			return b.ws[i].X0 < b.ws[j].X0
		})
		var sb strings.Builder
		var prevY float64
		first := true
		maxFS := 0.0
		anyBold, anyItalic := false, false
		minX, maxX := math.MaxFloat64, -math.MaxFloat64
		for _, w := range b.ws {
			if !first {
				if math.Abs(w.Y-prevY) > maxF(w.FontSize, 6)*0.4 {
					sb.WriteByte('\n') // новая визуальная строка внутри ячейки
				} else {
					sb.WriteByte(' ')
				}
			}
			sb.WriteString(w.S)
			prevY = w.Y
			first = false
			if w.FontSize > maxFS {
				maxFS = w.FontSize
			}
			anyBold = anyBold || w.Bold
			anyItalic = anyItalic || w.Italic
			if w.X0 < minX {
				minX = w.X0
			}
			if w.X1 > maxX {
				maxX = w.X1
			}
		}
		reg.text = collapseInlineSpaces(sb.String())
		reg.fontSize = maxFS
		reg.bold = anyBold
		reg.italic = anyItalic
		reg.minX = minX
		reg.maxX = maxX
	}
}

// collapseInlineSpaces убирает лишние пробелы, сохраняя переносы строк.
func collapseInlineSpaces(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = collapseSpaces(ln)
	}
	// Убираем пустые хвостовые/ведущие строки.
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// findRegion возвращает регион, содержащий точку (x,y) в top-down координатах,
// или nil. Граница включается слева/сверху.
func findRegion(regions []*region, x, y float64) *region {
	for _, reg := range regions {
		if x >= reg.x0-0.5 && x < reg.x1+0.5 && y >= reg.y0-0.5 && y < reg.y1+0.5 {
			return reg
		}
	}
	return nil
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
