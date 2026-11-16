package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestRenderGroupConditional(t *testing.T) {
	// Условное оформление должно отрисовываться на строке группы и подытоге
	// (при detail:false), а не только на детальных строках.
	rows := []compose.Row{
		{"М": "Убыток", "Сумма": "-100"},
		{"М": "Прибыль", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings:   []string{"М"},
		Measures:    []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:      report.Totals{Subtotals: true},
		Conditional: []report.CondRule{{When: "Сумма < 0", Field: "", Style: report.CellStyle{Color: "#c00", Bold: true}}},
	}
	res, _ := compose.Compose(rows, spec, newInterpEvaluator(interpreter.New()))
	out := string(renderComposedTable(res, &spec))
	if !strings.Contains(out, "color:#c00") || !strings.Contains(out, "font-weight:bold") {
		t.Fatalf("строка убыточной группы должна иметь стиль:\n%s", out)
	}
}

// TestCssOfColorValidation: cssOf пропускает только валидные цвета (hex/rgb/имя),
// а вредоносное значение (CSS-инъекция) отбрасывает (issue #16). Источник цвета
// теперь пользовательский (__settings → Conditional[].Style).
func TestCssOfColorValidation(t *testing.T) {
	// Валидные значения проходят.
	if got := cssOf(report.CellStyle{Color: "#c00", Background: "rgb(255,0,0)"}); !strings.Contains(got, "color:#c00") || !strings.Contains(got, "background:rgb(255,0,0)") {
		t.Fatalf("валидные цвета должны пройти: %q", got)
	}
	if got := cssOf(report.CellStyle{Color: "red"}); got != "color:red" {
		t.Fatalf("известное имя цвета должно пройти: %q", got)
	}
	// Вредоносные/невалидные значения отбрасываются (в строке стиля их нет).
	evil := []string{
		`red;background:url(javascript:alert(1))`,
		`expression(alert(1))`,
		`#c00;}/**/body{display:none`,
		`url('x')`,
		`нечто`,
	}
	for _, c := range evil {
		got := cssOf(report.CellStyle{Color: c})
		if strings.Contains(got, c) {
			t.Fatalf("вредоносный цвет %q не должен попадать в style, got %q", c, got)
		}
		if got != "" {
			t.Fatalf("ожидали пустой стиль для невалидного цвета %q, got %q", c, got)
		}
	}
	// Невалидный цвет отбрасывается, но валидные части (bold) сохраняются.
	if got := cssOf(report.CellStyle{Color: "javascript:alert(1)", Bold: true}); got != "font-weight:bold" {
		t.Fatalf("невалидный цвет должен отброситься, bold — остаться: %q", got)
	}
}

func TestBuildComposedChart(t *testing.T) {
	rows := []compose.Row{{"М": "Иванов", "Сумма": "150"}, {"М": "Петров", "Сумма": "30"}}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Chart:     &report.ChartSpec{Type: "bar", Category: "М", Series: []string{"Сумма"}},
	}
	res, _ := compose.Compose(rows, spec, nil)
	opt := buildComposedChart(res, spec.Chart, rows, spec, nil)
	if opt == nil {
		t.Fatal("nil chart option")
	}
	xAxis, _ := opt["xAxis"].(map[string]any)
	cats, _ := xAxis["data"].([]string)
	if len(cats) != 2 || cats[0] != "Иванов" {
		t.Fatalf("categories: %v", cats)
	}
}

// TestBuildComposedChart_HonorsCategory: когда Chart.Category отличается от
// верхней группировки, ось X строится по полю Category (отдельный свод), а не
// по группировке.
func TestBuildComposedChart_HonorsCategory(t *testing.T) {
	rows := []compose.Row{
		{"Менеджер": "Иванов", "Регион": "Юг", "Сумма": "100"},
		{"Менеджер": "Петров", "Регион": "Юг", "Сумма": "50"},
		{"Менеджер": "Иванов", "Регион": "Север", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"Менеджер"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Chart:     &report.ChartSpec{Type: "bar", Category: "Регион", Series: []string{"Сумма"}},
	}
	res, _ := compose.Compose(rows, spec, nil)
	opt := buildComposedChart(res, spec.Chart, rows, spec, nil)
	if opt == nil {
		t.Fatal("nil chart option")
	}
	xAxis, _ := opt["xAxis"].(map[string]any)
	cats, _ := xAxis["data"].([]string)
	if len(cats) != 2 || cats[0] != "Юг" || cats[1] != "Север" {
		t.Fatalf("ось X должна строиться по Регион (Юг, Север), got %v", cats)
	}
	series, _ := opt["series"].([]map[string]any)
	if len(series) != 1 {
		t.Fatalf("ожидалась одна серия, got %d", len(series))
	}
	data, _ := series[0]["data"].([]float64)
	if len(data) != 2 || data[0] != 150 || data[1] != 30 {
		t.Fatalf("данные серии по Регион: ожидалось [150 30], got %v", data)
	}
}

// TestBuildComposedChart_Category_RespectsCap: при пивоте по Category диаграмма
// агрегирует только строки в пределах потолка (как таблица), а не все исходные.
func TestBuildComposedChart_Category_RespectsCap(t *testing.T) {
	rows := []compose.Row{
		{"Менеджер": "Иванов", "Регион": "Юг", "Сумма": "100"},
		{"Менеджер": "Петров", "Регион": "Север", "Сумма": "50"},
	}
	spec := report.Composition{
		Groupings: []string{"Менеджер"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Chart:     &report.ChartSpec{Type: "bar", Category: "Регион", Series: []string{"Сумма"}},
	}
	res, _ := compose.ComposeN(rows, spec, nil, 1) // потолок 1 строка
	opt := buildComposedChart(res, spec.Chart, rows, spec, nil)
	xAxis, _ := opt["xAxis"].(map[string]any)
	cats, _ := xAxis["data"].([]string)
	if len(cats) != 1 || cats[0] != "Юг" {
		t.Fatalf("диаграмма по Category должна соблюдать потолок (только Юг), got %v", cats)
	}
}

func TestRenderComposedTable(t *testing.T) {
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "150"},
		{"М": "Петров", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Title: "Сумма, ₽"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))
	for _, want := range []string{"Иванов", "Петров", "150", "Сумма, ₽", "Итого", "data-group", "<table"} {
		if !strings.Contains(out, want) {
			t.Fatalf("HTML не содержит %q:\n%s", want, out)
		}
	}
}

func TestRenderMeasureAlign(t *testing.T) {
	// Показатель с Align:"center" должен давать text-align:center,
	// показатель без Align — text-align:right по умолчанию.
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "150", "Кол": "3"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures: []report.Measure{
			{Field: "Сумма", Agg: "sum", Align: "center"},
			{Field: "Кол", Agg: "sum"},
		},
		Totals: report.Totals{Grand: true, Subtotals: true},
		Detail: true,
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))

	if !strings.Contains(out, "text-align:center") {
		t.Fatalf("ожидали text-align:center для показателя Сумма:\n%s", out)
	}
	if !strings.Contains(out, "text-align:right") {
		t.Fatalf("ожидали text-align:right для показателя Кол:\n%s", out)
	}
}

func TestRenderMeasureFormat(t *testing.T) {
	// Показатель с Format:"#,##0.00" должен выводить значение в RU-формате с разрядкой.
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "12333.32"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Format: "#,##0.00"}},
		Totals:    report.Totals{Grand: true, Subtotals: true},
		Detail:    true,
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))
	// Значение 12333.32 при формате "#,##0.00" → "12 333,32" (неразрывный пробел)
	want := "12 333,32"
	if !strings.Contains(out, want) {
		t.Fatalf("HTML не содержит %q (форматированное число):\n%s", want, out)
	}
}

// TestWriteDetailLink проверяет, что детальная строка с DetailLink и UUID-значением
// содержит ссылку на документ вида <a href="/ui/document/<entity>/<uuid>">→</a>.
// Если DetailLink пуст — первая ячейка остаётся пустой (обратная совместимость).
func TestWriteDetailLink(t *testing.T) {
	const uuid = "550e8400-e29b-41d4-a716-446655440000"
	rows := []compose.Row{
		{"Контрагент": "Иванов", "Сумма": "100", "Регистратор": uuid},
	}
	spec := report.Composition{
		Groupings:    []string{"Контрагент"},
		Measures:     []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:       true,
		DetailLink:   "Регистратор",
		DetailEntity: "расходнаянакладная",
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))

	// Ссылка должна присутствовать: href содержит UUID и маркер →.
	// Имя сущности может быть URL-закодировано (кириллица → %XX), поэтому
	// проверяем только UUID-сегмент, который не кодируется.
	if !strings.Contains(out, uuid) {
		t.Fatalf("HTML не содержит UUID %q в ссылке:\n%s", uuid, out)
	}
	if !strings.Contains(out, `href=`) {
		t.Fatalf("HTML не содержит атрибут href:\n%s", out)
	}
	if !strings.Contains(out, `/ui/document/`) {
		t.Fatalf("HTML не содержит путь /ui/document/:\n%s", out)
	}
	if !strings.Contains(out, `→`) {
		t.Fatalf("HTML не содержит маркер → :\n%s", out)
	}

	// Без DetailLink — первая ячейка пустая (нет <a href=...>)
	specNoLink := report.Composition{
		Groupings: []string{"Контрагент"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
	}
	res2, _ := compose.Compose(rows, specNoLink, nil)
	out2 := string(renderComposedTable(res2, &specNoLink))
	if strings.Contains(out2, `<a href=`) {
		t.Fatalf("без DetailLink не должно быть ссылки:\n%s", out2)
	}
}

// TestWriteDetailLinkNoEntity проверяет, что при заданном DetailLink, но пустом
// DetailEntity, ссылка НЕ генерируется (иначе получается битый путь /ui/document//<uuid>).
// Первая ячейка должна быть пустой — без <a href=...>.
func TestWriteDetailLinkNoEntity(t *testing.T) {
	const uuid = "11112222-3333-4444-5555-666677778888"
	rows := []compose.Row{
		{"Контрагент": "Сидоров", "Сумма": "200", "Регистратор": uuid},
	}
	// DetailLink задан, DetailEntity — нет (пустая строка)
	spec := report.Composition{
		Groupings:    []string{"Контрагент"},
		Measures:     []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:       true,
		DetailLink:   "Регистратор",
		DetailEntity: "", // намеренно пусто
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))

	if strings.Contains(out, `<a href`) {
		t.Fatalf("при пустом DetailEntity не должно быть ссылки <a href=...>:\n%s", out)
	}
	if strings.Contains(out, `/ui/document/`) {
		t.Fatalf("при пустом DetailEntity не должно быть пути /ui/document/ в HTML:\n%s", out)
	}
}

// TestWriteDetailLinkCaseInsensitive проверяет выравнивание ключей:
// колонка запроса в нижнем регистре («регистратор») должна находиться
// через DetailLink в исходном регистре («Регистратор»).
func TestWriteDetailLinkCaseInsensitive(t *testing.T) {
	const uuid = "aaaabbbb-cccc-dddd-eeee-ffffffffffff"
	// Колонка приходит в нижнем регистре (как от компилятора запросов)
	rows := []compose.Row{
		{"Контрагент": "Петров", "сумма": "50", "регистратор": uuid},
	}
	spec := report.Composition{
		Groupings:    []string{"Контрагент"},
		Measures:     []report.Measure{{Field: "сумма", Agg: "sum"}},
		Detail:       true,
		DetailLink:   "Регистратор", // исходный регистр из composition
		DetailEntity: "поступление",
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))

	// UUID должен присутствовать в href — регистронезависимый поиск сработал
	if !strings.Contains(out, uuid) {
		t.Fatalf("UUID не найден через регистронезависимый ключ — нет %q в:\n%s", uuid, out)
	}
	if !strings.Contains(out, `/ui/document/`) {
		t.Fatalf("нет пути /ui/document/ в:\n%s", out)
	}
}

func TestAppearanceClass(t *testing.T) {
	cases := []struct {
		app  report.Appearance
		want string
	}{
		{report.Appearance{}, "report-composed"},
		{report.Appearance{Lines: "horizontal"}, "report-composed"}, // синоним умолчания
		{report.Appearance{Lines: "vertical"}, "report-composed rep-lines-v"},
		{report.Appearance{Lines: "both"}, "report-composed rep-lines-both"},
		{report.Appearance{Lines: "none"}, "report-composed rep-lines-none"},
		{report.Appearance{Zebra: true}, "report-composed rep-zebra"},
		{report.Appearance{Lines: "both", Zebra: true}, "report-composed rep-lines-both rep-zebra"},
		{report.Appearance{Lines: "мусор"}, "report-composed"}, // неизвестное → дефолт
	}
	for _, c := range cases {
		spec := &report.Composition{Appearance: c.app}
		if got := appearanceClass(spec); got != c.want {
			t.Errorf("appearanceClass(%+v) = %q, ждали %q", c.app, got, c.want)
		}
	}
	if got := appearanceClass(nil); got != "report-composed" {
		t.Errorf("nil spec → %q, ждали report-composed", got)
	}
}

func TestRenderComposedAppearance(t *testing.T) {
	rows := []compose.Row{{"М": "Иванов", "Сумма": "150"}}
	spec := report.Composition{
		Groupings:  []string{"М"},
		Measures:   []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Appearance: report.Appearance{Lines: "both", Zebra: true},
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))
	if !strings.Contains(out, `class="report-composed rep-lines-both rep-zebra"`) {
		t.Fatalf("ожидали классы оформления на таблице:\n%s", out)
	}
	// Дефолт (нулевой Appearance) — прежний класс, без классов оформления.
	specDef := spec
	specDef.Appearance = report.Appearance{}
	res2, _ := compose.Compose(rows, specDef, nil)
	out2 := string(renderComposedTable(res2, &specDef))
	if !strings.Contains(out2, `class="report-composed"`) || strings.Contains(out2, "rep-lines") || strings.Contains(out2, "rep-zebra") {
		t.Fatalf("дефолт не должен содержать классы оформления:\n%s", out2)
	}
}

func TestComposedPathEscaping(t *testing.T) {
	// Значение группировки с «/» не должно ломать префиксную схему путей:
	// сиблинг «Иванов/Доп» обязан иметь экранированный data-group, иначе
	// сворачивание «Иванов» ложно спрятало бы его.
	rows := []compose.Row{
		{"М": "Иванов", "Сумма": "10"},
		{"М": "Иванов/Доп", "Сумма": "20"},
	}
	spec := report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	res, _ := compose.Compose(rows, spec, nil)
	out := string(renderComposedTable(res, &spec))
	if !strings.Contains(out, `data-group="/Иванов"`) {
		t.Fatalf("нет группы Иванов:\n%s", out)
	}
	if !strings.Contains(out, `data-group="/Иванов%2FДоп"`) {
		t.Fatalf("сегмент с / не экранирован:\n%s", out)
	}
	if strings.Contains(out, `data-group="/Иванов/Доп"`) {
		t.Fatalf("неэкранированный путь ломает префикс-схему:\n%s", out)
	}
	if !strings.Contains(out, "Иванов/Доп") {
		t.Fatalf("видимая подпись должна быть сырой:\n%s", out)
	}
}
