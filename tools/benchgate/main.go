// benchgate заваливает сборку, когда benchstat показал значимую просадку
// производительности больше порога (план 115, этап D).
//
// До этого джоб bench в CI только печатал отчёт и грузил его артефактом:
// порога не было, а прогон базового коммита завершался `|| true`. То есть джоб
// создавал впечатление защиты от регрессий, которой не было — просадка вроде
// #623 (полный скан при удалении из полнотекстового индекса) через него
// проходила беспрепятственно.
//
// Читает CSV benchstat (`benchstat -format csv base.txt pr.txt`) и смотрит
// колонку «vs base». Незначимые изменения benchstat помечает `~` — их гейт
// игнорирует, иначе шум разделяемого раннера ронял бы каждый второй PR.
// Строка geomean тоже игнорируется: это агрегат, а не бенчмарк.
//
// Одной значимости мало. Раннер GitHub делит железо с другими задачами, и на
// бенчмарках с диском разброс замеров доходит до сотен процентов. При таком
// разбросе значимость (p) достигается легко — распределения формально
// различаются, — но величина «vs base» не значит ничего: PR из двух правок
// markdown получал «+79.86%» при разбросе ±152% (issue #683). Поэтому строка
// отбрасывается, если разброс (колонки CI, они в CSV есть) не меньше самой
// просадки: отличить одно от другого измерение не позволяет.
//
// Отброшенные строки печатаются, а не глотаются молча: иначе гейт начнёт
// скрывать настоящие просадки на шумных бенчмарках, и это будет незаметно.
//
// Использование:
//
//	benchstat -format csv base.txt pr.txt | benchgate -threshold 25
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// regression — просадка одного бенчмарка по одной метрике.
type regression struct {
	name    string
	metric  string
	percent float64
	noise   float64 // максимум из CI базы и CI ветки, в процентах
}

func main() {
	threshold := flag.Float64("threshold", 25, "порог просадки в процентах, выше которого сборка падает")
	metric := flag.String("metric", "sec/op", "метрика, по которой судим (sec/op, B/op, allocs/op)")
	flag.Parse()

	regs, noisy, err := parse(os.Stdin, *metric, *threshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchgate: %v\n", err)
		os.Exit(2)
	}
	for _, n := range noisy {
		fmt.Printf("benchgate: %s пропущен — разброс замеров ±%.0f%% не меньше просадки %+.2f%%, "+
			"величине верить нельзя\n", n.name, n.noise, n.percent)
	}
	if len(regs) == 0 {
		fmt.Printf("benchgate: значимых просадок %s больше %.0f%% нет\n", *metric, *threshold)
		return
	}
	fmt.Fprintf(os.Stderr, "benchgate: просадка %s больше порога %.0f%%:\n", *metric, *threshold)
	for _, r := range regs {
		fmt.Fprintf(os.Stderr, "  %s: %+.2f%% (разброс ±%.0f%%)\n", r.name, r.percent, r.noise)
	}
	fmt.Fprintf(os.Stderr, "\nЕсли замедление осознанное — поднимите порог в шаге CI или\n"+
		"объясните его в описании PR и временно снимите гейт.\n")
	os.Exit(1)
}

// parse разбирает CSV benchstat и возвращает просадки выше порога.
//
// Формат: секции по метрикам, разделённые пустой строкой. В секции первая
// строка — имена файлов, вторая — заголовки колонок (вида
// «,sec/op,CI,sec/op,CI,vs base,P»), дальше строки бенчмарков.
// Возвращает просадки выше порога и отдельно строки, отброшенные из-за
// разброса, — вызывающий их печатает.
func parse(r io.Reader, metric string, threshold float64) (regs, noisy []regression, err error) {
	rd := csv.NewReader(r)
	rd.FieldsPerRecord = -1 // секции имеют разную ширину
	records, err := rd.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("разбор CSV benchstat: %w", err)
	}

	inSection := false
	vsIdx := -1
	var ciIdx []int

	for _, rec := range records {
		// Заголовок секции: вторая колонка — имя метрики, где-то есть «vs base».
		if idx := indexOf(rec, "vs base"); idx >= 0 {
			inSection = len(rec) > 1 && strings.TrimSpace(rec[1]) == metric
			vsIdx = idx
			ciIdx = indexesOf(rec, "CI")
			continue
		}
		if !inSection || vsIdx < 0 || len(rec) <= vsIdx {
			continue
		}
		name := strings.TrimSpace(rec[0])
		if name == "" || name == "geomean" {
			continue
		}
		pct, ok := parsePercent(rec[vsIdx])
		if !ok {
			continue // «~» — изменение незначимо
		}
		if pct <= threshold {
			continue
		}
		reg := regression{name: name, metric: metric, percent: pct, noise: maxCI(rec, ciIdx)}
		// Разброс не меньше просадки — измерение их не различает.
		if reg.noise >= pct {
			noisy = append(noisy, reg)
			continue
		}
		regs = append(regs, reg)
	}
	return regs, noisy, nil
}

// indexesOf возвращает все позиции колонки с данным заголовком (у benchstat их
// две — по одной на каждый прогон).
func indexesOf(rec []string, want string) []int {
	var out []int
	for i, v := range rec {
		if strings.TrimSpace(v) == want {
			out = append(out, i)
		}
	}
	return out
}

// maxCI — наибольший разброс среди колонок CI строки, в процентах. Пустая или
// неразобранная колонка считается нулём: неизвестный разброс не повод молча
// пропустить просадку.
func maxCI(rec []string, ciIdx []int) float64 {
	max := 0.0
	for _, i := range ciIdx {
		if i >= len(rec) {
			continue
		}
		if v, ok := parsePercent(rec[i]); ok && v > max {
			max = v
		}
	}
	return max
}

func indexOf(rec []string, want string) int {
	for i, v := range rec {
		if strings.TrimSpace(v) == want {
			return i
		}
	}
	return -1
}

// parsePercent разбирает «+49.94%» → 49.94. Возвращает false для «~» и пустых.
func parsePercent(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "~" {
		return 0, false
	}
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimPrefix(s, "+")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
