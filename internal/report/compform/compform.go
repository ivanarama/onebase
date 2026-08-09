// Package compform собирает report.Composition из полей HTML-формы (comp.*).
// Вынесен из internal/launcher, чтобы один и тот же сборщик использовался и
// конфигуратором (сохранение composition в YAML), и рантаймом отчёта
// (пользовательские настройки на форме отчёта, план 70). Зависит только от
// internal/report и stdlib.
package compform

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
)

// Parse собирает report.Composition из полей формы comp.*.
// (nil, false) — маркера comp.present нет (composition не трогаем).
// (nil, true)  — включён, но пуст (composition очищается).
func Parse(f url.Values) (*report.Composition, bool) {
	if f.Get("comp.present") == "" {
		return nil, false
	}
	c := &report.Composition{}

	// Группировки
	for _, i := range rowIndexes(f, "comp.grouping.", "") {
		v := strings.TrimSpace(f.Get("comp.grouping." + strconv.Itoa(i)))
		if v == "" {
			continue
		}
		c.Groupings = append(c.Groupings, v)
	}

	// Колонки кросс-таблицы (непусто → режим кросс-таблицы)
	for _, i := range rowIndexes(f, "comp.column.", "") {
		v := strings.TrimSpace(f.Get("comp.column." + strconv.Itoa(i)))
		if v == "" {
			continue
		}
		c.Columns = append(c.Columns, v)
	}

	// Показатели
	for _, i := range rowIndexes(f, "comp.measure.", ".field") {
		p := "comp.measure." + strconv.Itoa(i)
		fld := strings.TrimSpace(f.Get(p + ".field"))
		if fld == "" {
			continue
		}
		c.Measures = append(c.Measures, report.Measure{
			Field:  fld,
			Agg:    f.Get(p + ".agg"),
			Title:  strings.TrimSpace(f.Get(p + ".title")),
			Align:  f.Get(p + ".align"),
			Format: strings.TrimSpace(f.Get(p + ".format")),
			Expr:   strings.TrimSpace(f.Get(p + ".expr")),
		})
	}

	// Итоги и детальные записи
	c.Totals.Grand = f.Get("comp.totals.grand") != ""
	c.Totals.Subtotals = f.Get("comp.totals.subtotals") != ""
	c.Detail = f.Get("comp.detail") != ""
	c.DetailLink = strings.TrimSpace(f.Get("comp.detail_link"))
	c.DetailEntity = strings.TrimSpace(f.Get("comp.detail_entity"))

	// Сортировка
	for _, i := range rowIndexes(f, "comp.sort.", ".field") {
		p := "comp.sort." + strconv.Itoa(i)
		fld := strings.TrimSpace(f.Get(p + ".field"))
		if fld == "" {
			continue
		}
		c.Sort = append(c.Sort, report.SortKey{Field: fld, Dir: f.Get(p + ".dir")})
	}

	// Условное оформление
	for _, i := range rowIndexes(f, "comp.cond.", ".when") {
		p := "comp.cond." + strconv.Itoa(i)
		when := strings.TrimSpace(f.Get(p + ".when"))
		if when == "" {
			continue
		}
		color := strings.TrimSpace(f.Get(p + ".color"))
		if color == "#000000" {
			color = ""
		}
		background := strings.TrimSpace(f.Get(p + ".background"))
		if background == "#ffffff" {
			background = ""
		}
		c.Conditional = append(c.Conditional, report.CondRule{
			When:  when,
			Field: strings.TrimSpace(f.Get(p + ".field")),
			Style: report.CellStyle{
				Color:      color,
				Background: background,
				Bold:       f.Get(p+".bold") != "",
				Italic:     f.Get(p+".italic") != "",
			},
		})
	}

	// Оформление вывода (план 71): линии сетки + зебра. Lines валидируем по
	// белому списку — значение приходит и из недоверенной рантайм-формы (__settings),
	// а дальше уходит в CSS-класс; неизвестное → "" (исторический вид).
	c.Appearance = report.Appearance{
		Lines:      normLines(f.Get("comp.appearance.lines")),
		Zebra:      f.Get("comp.appearance.zebra") != "",
		CollapseTo: parseCollapseTo(f.Get("comp.appearance.collapse_to")),
	}

	// Диаграмма (пустой type → нет диаграммы)
	if ct := strings.TrimSpace(f.Get("comp.chart.type")); ct != "" {
		var series []string
		for _, s := range strings.Split(f.Get("comp.chart.series"), ",") {
			if s = strings.TrimSpace(s); s != "" {
				series = append(series, s)
			}
		}
		c.Chart = &report.ChartSpec{
			Type:     ct,
			Category: strings.TrimSpace(f.Get("comp.chart.category")),
			Series:   series,
		}
	}

	// Включён, но пуст → сигнал очистки (nil, true). Очищаем только когда пусто
	// ВСЁ — иначе правила оформления / сортировка / график без группировок молча
	// терялись бы при сохранении (work-in-progress конструктора).
	if len(c.Groupings) == 0 && len(c.Columns) == 0 && len(c.Measures) == 0 && len(c.Conditional) == 0 && len(c.Sort) == 0 && c.Chart == nil {
		return nil, true
	}
	return c, true
}

// parseCollapseTo читает начальное состояние групп (issue #575): развёрнуты
// уровни 0..N. Пустое поле или нечисловой ввод — nil, то есть «не задано»:
// отчёт открывается развёрнутым, как было. Отрицательное значение равнозначно
// нулю. Верхняя граница не нужна — уровень глубже группировок означает то же
// «всё развёрнуто».
func parseCollapseTo(v string) *int {
	s := strings.TrimSpace(v)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	if n < 0 {
		n = 0
	}
	return &n
}

// normLines нормализует значение линий сетки по белому списку. "horizontal"
// (синоним умолчания) и любой неизвестный ввод → "" — исторический вид только с
// нижними границами. Значение уходит в CSS-класс рендера, поэтому произвольный
// ввод из недоверенной рантайм-формы (__settings) сюда не просачивается.
func normLines(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "vertical":
		return "vertical"
	case "both":
		return "both"
	case "none":
		return "none"
	default: // "", "horizontal", мусор
		return ""
	}
}

// rowIndexes возвращает индексы строк, фактически присланных формой под ключами
// «<prefix><i>» либо «<prefix><i>.<suffix>», по возрастанию.
//
// Перебирать индексы подряд с остановкой на первом пустом нельзя: клиент
// нумерует строки по их числу в таблице, а удаление строки из середины
// оставляет дыру — на ней разбор обрывался и терял всё, что ниже (issue #678).
func rowIndexes(f url.Values, prefix, suffix string) []int {
	seen := map[int]bool{}
	for key := range f {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		num := key[len(prefix):]
		if suffix != "" {
			if !strings.HasSuffix(num, suffix) {
				continue
			}
			num = num[:len(num)-len(suffix)]
		}
		i, err := strconv.Atoi(num)
		if err != nil || i < 0 {
			continue
		}
		seen[i] = true
	}
	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}
