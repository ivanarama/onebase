package interpreter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/shopspring/decimal"
)

// Объект-построитель «Страница» (план 66). Передаётся в обработчик страницы
// (src/<имя>.page.os, Процедура ПриФормировании(Страница, Параметры)) как
// мутируемый объект — обработчик наполняет его блоками, а UI-слой
// (internal/ui) рендерит собранные блоки в оболочку приложения с
// автоэкранированием и i18n. Строится UI-слоем через NewPageBuilder().
//
// Сознательно НЕ принимаем «строку HTML» как основной путь: блоки описывают
// структуру (заголовок/текст/показатель/таблица/кнопка), которую шаблон
// экранирует. Произвольный HTML доступен только явным ДобавитьСыройHTML и
// помечается «на ответственности автора».

// PageBlock — один отрендеренный блок страницы. Экспортирован, чтобы UI-слой
// мог пройтись по результату (PageBuilder.Blocks()). Поля заполняются по Kind.
type PageBlock struct {
	Kind string // heading | paragraph | kpi | table | button | divider | raw

	Text   string // heading/paragraph/button: текст; kpi: подпись не здесь (см. Label)
	URL    string // button: адрес перехода
	Action string // button: имя серверной процедуры-действия (КнопкаДействие); взаимоисключимо с URL
	Label  string // kpi: подпись
	Value  string // kpi: уже отформатированное значение
	Title  string // table: заголовок таблицы

	Columns      []string  // table: КЛЮЧИ колонок — адресация ячеек в Rows (не переводятся)
	ColumnLabels []string  // table: отображаемые заголовки колонок (переводятся i18n)
	Rows         []PageRow // table: строки

	Items []PageListItem // list: пункты списка
	Chart *PageChart     // chart: данные графика

	HTML string // raw: санитизированный HTML (только ДобавитьСыройHTML)
}

// PageListItem — пункт списка: текст и необязательная ссылка.
type PageListItem struct {
	Text string
	URL  string
}

// PageChart — данные графика (план 66). Сериализуется в опции ECharts тем же
// EChartsOption, что и виджеты рабочего стола.
type PageChart struct {
	Kind   string // bar | line | pie
	XAxis  []string
	Series []PageSeries
}

// PageSeries — одна серия графика, выровненная по XAxis.
type PageSeries struct {
	Name string
	Data []float64
}

// PageRow — строка таблицы. Ячейки адресуются по имени колонки.
type PageRow struct {
	Cells map[string]PageCell
}

// PageCell — ячейка таблицы: текст и необязательная ссылка (кликабельная ячейка).
type PageCell struct {
	Text string
	URL  string
}

// DSLPageBuilder — объект «Страница» в DSL. Реализует This (Get/Set) и
// MethodCallable (CallMethod).
type DSLPageBuilder struct {
	blocks []PageBlock
}

// NewPageBuilder создаёт пустой построитель страницы (для UI-роутера).
func NewPageBuilder() *DSLPageBuilder { return &DSLPageBuilder{} }

// NewStringMap строит Соответствие (Map) из строковых пар — UI-слой передаёт им
// «Параметры» страницы в обработчик.
func NewStringMap(src map[string]string) *Map { return mapFromStrings(src) }

// Blocks возвращает собранные блоки в порядке добавления (для рендера).
func (b *DSLPageBuilder) Blocks() []PageBlock { return b.blocks }

func (b *DSLPageBuilder) Get(string) any  { return nil }
func (b *DSLPageBuilder) Set(string, any) {}

func (b *DSLPageBuilder) CallMethod(name string, args []any) any {
	switch name {
	case "заголовок", "heading":
		b.blocks = append(b.blocks, PageBlock{Kind: "heading", Text: argStr(args, 0)})
		return b
	case "абзац", "текст", "paragraph", "text":
		b.blocks = append(b.blocks, PageBlock{Kind: "paragraph", Text: argStr(args, 0)})
		return b
	case "показатель", "kpi":
		format := ""
		if len(args) >= 3 {
			format = argStr(args, 2)
		}
		var val any
		if len(args) >= 2 {
			val = args[1]
		}
		b.blocks = append(b.blocks, PageBlock{
			Kind:  "kpi",
			Label: argStr(args, 0),
			Value: pageKPIDisplay(val, format),
		})
		return b
	case "кнопка", "ссылка", "button", "link":
		b.blocks = append(b.blocks, PageBlock{Kind: "button", Text: argStr(args, 0), URL: argStr(args, 1)})
		return b
	case "кнопкадействие", "actionbutton":
		// Кнопка вызывает серверную процедуру-действие из того же .page.os
		// (POST /ui/page/{name}/action/{action}), а не переходит по URL.
		b.blocks = append(b.blocks, PageBlock{Kind: "button", Text: argStr(args, 0), Action: argStr(args, 1)})
		return b
	case "разделитель", "divider":
		b.blocks = append(b.blocks, PageBlock{Kind: "divider"})
		return b
	case "сыройhtml", "добавитьсыройhtml", "rawhtml", "addrawhtml":
		b.blocks = append(b.blocks, PageBlock{Kind: "raw", HTML: sanitizePageHTML(argStr(args, 0))})
		return b
	case "таблица", "table":
		b.blocks = append(b.blocks, PageBlock{Kind: "table", Title: argStr(args, 0)})
		return &DSLPageTable{builder: b, idx: len(b.blocks) - 1}
	case "список", "list":
		b.blocks = append(b.blocks, PageBlock{Kind: "list", Title: argStr(args, 0)})
		return &DSLPageList{builder: b, idx: len(b.blocks) - 1}
	case "график", "chart":
		kind := "bar"
		if len(args) >= 2 {
			kind = strings.ToLower(argStr(args, 1))
		}
		b.blocks = append(b.blocks, PageBlock{Kind: "chart", Title: argStr(args, 0), Chart: &PageChart{Kind: kind}})
		return &DSLPageChart{builder: b, idx: len(b.blocks) - 1}
	}
	panic(userError{Msg: "Страница: неизвестный метод " + name})
}

// DSLPageList — дескриптор списка внутри построителя.
type DSLPageList struct {
	builder *DSLPageBuilder
	idx     int
}

func (l *DSLPageList) Get(string) any  { return nil }
func (l *DSLPageList) Set(string, any) {}

func (l *DSLPageList) CallMethod(name string, args []any) any {
	switch name {
	case "пункт", "добавить", "item", "add":
		it := PageListItem{Text: argStr(args, 0)}
		if len(args) >= 2 {
			it.URL = argStr(args, 1)
		}
		l.builder.blocks[l.idx].Items = append(l.builder.blocks[l.idx].Items, it)
		return l
	}
	panic(userError{Msg: "Страница.Список: неизвестный метод " + name})
}

// DSLPageChart — дескриптор графика внутри построителя.
type DSLPageChart struct {
	builder *DSLPageBuilder
	idx     int
}

func (c *DSLPageChart) Get(string) any  { return nil }
func (c *DSLPageChart) Set(string, any) {}

func (c *DSLPageChart) chart() *PageChart { return c.builder.blocks[c.idx].Chart }

func (c *DSLPageChart) CallMethod(name string, args []any) any {
	switch name {
	case "категории", "categories":
		var cols []string
		if len(args) == 1 {
			if a, ok := args[0].(*Array); ok {
				for _, it := range a.items {
					cols = append(cols, pageValueString(it))
				}
				c.chart().XAxis = cols
				return c
			}
		}
		for i := range args {
			cols = append(cols, argStr(args, i))
		}
		c.chart().XAxis = cols
		return c
	case "серия", "series":
		s := PageSeries{Name: argStr(args, 0)}
		if len(args) >= 2 {
			s.Data = toFloatSlice(args[1])
		}
		c.chart().Series = append(c.chart().Series, s)
		return c
	}
	panic(userError{Msg: "Страница.График: неизвестный метод " + name})
}

// toFloatSlice превращает DSL-Массив (или одиночное число) в []float64.
func toFloatSlice(v any) []float64 {
	var out []float64
	if a, ok := v.(*Array); ok {
		for _, it := range a.items {
			f, _ := toFloat(it)
			out = append(out, f)
		}
		return out
	}
	if f, ok := toFloat(v); ok {
		out = append(out, f)
	}
	return out
}

// DSLPageTable — дескриптор табличного блока внутри построителя. Мутирует блок
// по индексу, поэтому добавление других блоков позже его не ломает.
type DSLPageTable struct {
	builder *DSLPageBuilder
	idx     int
}

func (t *DSLPageTable) Get(string) any  { return nil }
func (t *DSLPageTable) Set(string, any) {}

func (t *DSLPageTable) CallMethod(name string, args []any) any {
	switch name {
	case "колонки", "columns":
		cols := make([]string, 0, len(args))
		for i := range args {
			cols = append(cols, argStr(args, i))
		}
		t.builder.blocks[t.idx].Columns = cols
		// Отдельная копия для отображения: i18n переводит ColumnLabels, а Columns
		// остаются ключами для `index $row.Cells` в шаблоне (иначе перевод
		// заголовка рассогласует адресацию ячеек). Копия обязательна — общий
		// backing-массив дал бы мутацию Columns при переводе.
		t.builder.blocks[t.idx].ColumnLabels = append([]string(nil), cols...)
		return t
	case "добавитьстроку", "addrow":
		row := PageRow{Cells: map[string]PageCell{}}
		t.builder.blocks[t.idx].Rows = append(t.builder.blocks[t.idx].Rows, row)
		return &DSLPageRow{builder: t.builder, block: t.idx, row: len(t.builder.blocks[t.idx].Rows) - 1}
	}
	panic(userError{Msg: "Страница.Таблица: неизвестный метод " + name})
}

// DSLPageRow — дескриптор строки таблицы. Ячейки адресуются по имени колонки.
type DSLPageRow struct {
	builder *DSLPageBuilder
	block   int
	row     int
}

func (r *DSLPageRow) Get(string) any  { return nil }
func (r *DSLPageRow) Set(string, any) {}

func (r *DSLPageRow) cell() map[string]PageCell {
	return r.builder.blocks[r.block].Rows[r.row].Cells
}

func (r *DSLPageRow) CallMethod(name string, args []any) any {
	switch name {
	case "установить", "set":
		if len(args) >= 2 {
			col := argStr(args, 0)
			c := r.cell()[col]
			c.Text = pageValueString(args[1])
			r.cell()[col] = c
		}
		return r
	case "ссылка", "link":
		if len(args) >= 2 {
			col := argStr(args, 0)
			c := r.cell()[col]
			c.URL = argStr(args, 1)
			r.cell()[col] = c
		}
		return r
	}
	panic(userError{Msg: "Страница.Таблица.Строка: неизвестный метод " + name})
}

// ─── вспомогательные ──────────────────────────────────────────────────────────

// pageHTMLPolicy — bluemonday-санитайзер для блока ДобавитьСыройHTML (план 66).
// Allowlist на базе UGCPolicy (форматирование, списки, таблицы, ссылки и
// картинки только безопасных схем, заголовки) надёжно вырезает
// script/iframe/object, on*-обработчики и javascript:/data:-URI. Это замена
// прежнему блоклисту на регулярках, который обходился разделителем «/» перед
// обработчиком (<img src=x/onerror=…>), табом в схеме (java&#9;script:) и
// data:-iframe.
//
// class разрешаем глобально для вёрстки. Глобальный style — ТОЛЬКО через
// whitelist безопасных свойств (AllowStyles): иначе значение style проходит
// дословно и открывает CSS-инъекцию — position:fixed overlay для кликджекинга,
// background:url(...) для утечки данных/трекинга. Каждое свойство валидируется
// дефолтным CSS-handler'ом bluemonday (color/background-color — только
// именованные/hex/rgb/hsl, url() отбрасывается; expression() не проходит).
// Сознательно НЕ включены position (его handler допускает fixed) и shorthand
// background (допускает url()).
var pageHTMLPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Globally()
	p.AllowStyles(
		"color", "background-color",
		"text-align", "vertical-align",
		"font-weight", "font-style", "font-size", "font-family", "line-height",
		"padding", "margin", "border",
		"width", "height", "display",
	).Globally()
	return p
}()

// sanitizePageHTML очищает произвольный HTML из ДобавитьСыройHTML по allowlist.
// Результат безопасно отдавать через template.HTML (pageRaw).
func sanitizePageHTML(s string) string {
	return pageHTMLPolicy.Sanitize(s)
}

func argStr(args []any, i int) string {
	if i >= len(args) {
		return ""
	}
	return pageValueString(args[i])
}

func pageValueString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// pageKPIDisplay форматирует значение показателя по подсказке формата
// (money/percent/number). Зеркалит поведение виджетов, но самодостаточно, чтобы
// не тянуть в интерпретатор пакет widget.
//
// Деньги и числа форматируются ТОЧНО: для денежных сумм-decimal путь через
// float64 (toFloat → InexactFloat64) терял младшие разряды на крупных суммах,
// поэтому money/number группируем напрямую по строковому представлению decimal,
// не конвертируя в float. Прочие числовые типы (float/int/строка) — как раньше.
func pageKPIDisplay(v any, format string) string {
	switch strings.ToLower(format) {
	case "money":
		if d, ok := toDecimal(v); ok {
			return pageGroupDecimal(d, 2) + " ₽"
		}
	case "percent":
		if f, ok := toFloat(v); ok {
			return strconv.FormatFloat(f, 'f', 1, 64) + "%"
		}
	case "number":
		if d, ok := toDecimal(v); ok {
			return pageGroupDecimal(d, 0)
		}
	}
	return pageValueString(v)
}

// pageGroupDecimal форматирует decimal.Decimal с разделителем тысяч и frac
// знаками после запятой ТОЧНО — без округления через float64. Округляет
// банковской математикой decimal до frac знаков (StringFixed), затем
// группирует целую часть узким неразрывным пробелом между тройками и десятичной
// запятой (русская конвенция).
func pageGroupDecimal(d decimal.Decimal, frac int) string {
	neg := d.IsNegative()
	if neg {
		d = d.Neg()
	}
	s := d.StringFixed(int32(frac)) //nolint:gosec // G115: значение приходит из проверенной модели и заведомо укладывается в целевой тип // округление по правилам decimal, без float
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, fracPart = s[:dot], s[dot+1:]
	}
	return pageGroupDigits(intPart, fracPart, neg)
}

// pageGroupDigits собирает из уже разобранных цифр целой/дробной части строку с
// группировкой тройками (узкий неразрывный пробел) и десятичной запятой.
func pageGroupDigits(intPart, fracPart string, neg bool) string {
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := len(intPart) % 3
	if rem > 0 {
		b.WriteString(intPart[:rem])
	}
	for i := rem; i < len(intPart); i += 3 {
		if i > 0 {
			b.WriteRune(' ')
		}
		b.WriteString(intPart[i : i+3])
	}
	if fracPart != "" {
		b.WriteByte(',')
		b.WriteString(fracPart)
	}
	return b.String()
}
