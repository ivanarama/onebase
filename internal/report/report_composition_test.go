package report

import "testing"

func TestParseComposition(t *testing.T) {
	src := []byte(`
name: Прод
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Менеджер, Клиент]
  measures:
    - { field: Сумма, agg: sum, title: "Сумма, ₽" }
  totals: { grand: true, subtotals: true }
  detail: true
  sort: [ { field: Сумма, dir: desc } ]
  conditional:
    - { when: "Сумма < 0", field: "", style: { color: "#c00", bold: true } }
  chart: { type: bar, category: Менеджер, series: [Сумма] }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition == nil {
		t.Fatal("Composition is nil")
	}
	c := r.Composition
	if len(c.Groupings) != 2 || c.Groupings[0] != "Менеджер" {
		t.Fatalf("groupings: %v", c.Groupings)
	}
	if len(c.Measures) != 1 || c.Measures[0].Field != "Сумма" || c.Measures[0].Agg != "sum" || c.Measures[0].Title != "Сумма, ₽" {
		t.Fatalf("measures: %+v", c.Measures)
	}
	if !c.Totals.Grand || !c.Totals.Subtotals || !c.Detail {
		t.Fatalf("totals/detail: %+v %v", c.Totals, c.Detail)
	}
	if len(c.Conditional) != 1 || c.Conditional[0].Field != "" || c.Conditional[0].Style.Color != "#c00" || !c.Conditional[0].Style.Bold || c.Conditional[0].Style.Italic {
		t.Fatalf("conditional: %+v", c.Conditional)
	}
	if len(c.Sort) != 1 || c.Sort[0].Field != "Сумма" || c.Sort[0].Dir != "desc" {
		t.Fatalf("sort: %+v", c.Sort)
	}
	if c.Chart == nil || c.Chart.Type != "bar" || c.Chart.Category != "Менеджер" || len(c.Chart.Series) != 1 || c.Chart.Series[0] != "Сумма" {
		t.Fatalf("chart: %+v", c.Chart)
	}
}

func TestParseMeasureAlign(t *testing.T) {
	src := []byte(`
name: Тест
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Менеджер]
  measures:
    - { field: Сумма, agg: sum, align: center }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition == nil {
		t.Fatal("Composition is nil")
	}
	if len(r.Composition.Measures) != 1 {
		t.Fatalf("ожидали 1 показатель, получили %d", len(r.Composition.Measures))
	}
	if r.Composition.Measures[0].Align != "center" {
		t.Fatalf("Align = %q, ожидали \"center\"", r.Composition.Measures[0].Align)
	}
}

func TestParseAppearance(t *testing.T) {
	src := []byte(`
name: Тест
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Менеджер]
  measures:
    - { field: Сумма, agg: sum }
  appearance: { lines: both, zebra: true }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition == nil {
		t.Fatal("Composition is nil")
	}
	if r.Composition.Appearance.Lines != "both" || !r.Composition.Appearance.Zebra {
		t.Fatalf("appearance: %+v", r.Composition.Appearance)
	}
}

func TestParseAppearanceDefault(t *testing.T) {
	// Без блока appearance — нулевое значение (исторический вид): пустые Lines,
	// зебра выключена. Гарантия обратной совместимости существующих отчётов.
	src := []byte(`
name: Тест
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Менеджер]
  measures:
    - { field: Сумма, agg: sum }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition.Appearance.Lines != "" || r.Composition.Appearance.Zebra {
		t.Fatalf("ожидали нулевое оформление, получили %+v", r.Composition.Appearance)
	}
}

// collapse_to читается из appearance и отличает «0» от «ключа нет» (issue #575):
// именно поэтому поле указательное. Вариант отчёта задаёт своё значение.
func TestParseAppearanceCollapseTo(t *testing.T) {
	src := []byte(`
name: Тест
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Участник, Категория]
  measures:
    - { field: Сумма, agg: sum }
  appearance: { collapse_to: 0 }
variants:
  - name: Подробный
    composition:
      groupings: [Участник]
      measures:
        - { field: Сумма, agg: sum }
      appearance: { collapse_to: 2 }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Composition.Appearance.CollapseTo
	if got == nil || *got != 0 {
		t.Fatalf("collapse_to: 0 должен читаться как заданный ноль, получили %v", got)
	}
	if len(r.Variants) != 1 || r.Variants[0].Composition == nil {
		t.Fatalf("вариант не разобран: %+v", r.Variants)
	}
	if v := r.Variants[0].Composition.Appearance.CollapseTo; v == nil || *v != 2 {
		t.Fatalf("collapse_to варианта: %v", v)
	}

	// Без ключа — nil: отчёт открывается развёрнутым, как было.
	plain, err := ParseBytes([]byte("name: Т\nquery: \"ВЫБРАТЬ 1\"\ncomposition:\n  groupings: [М]\n  measures:\n    - { field: С, agg: sum }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Composition.Appearance.CollapseTo != nil {
		t.Fatalf("без ключа ожидали nil, получили %v", *plain.Composition.Appearance.CollapseTo)
	}
}

func TestParseNoComposition(t *testing.T) {
	r, err := ParseBytes([]byte("name: X\nquery: \"ВЫБРАТЬ 1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Composition != nil {
		t.Fatal("Composition must be nil when absent")
	}
}

func TestParseVariants(t *testing.T) {
	src := []byte(`
name: Продажи
query: "ВЫБРАТЬ 1"
composition:
  groupings: [Менеджер]
  measures:
    - { field: Сумма, agg: sum }
variants:
  - name: "По складам"
    composition:
      groupings: [Склад, Номенклатура]
      measures:
        - { field: Сумма, agg: sum }
  - name: "Кросс по месяцам"
    composition:
      groupings: [Номенклатура]
      columns: [Месяц]
      measures:
        - { field: Сумма, agg: sum }
`)
	r, err := ParseBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Variants) != 2 {
		t.Fatalf("ожидали 2 варианта, получили %d", len(r.Variants))
	}
	if r.Variants[0].Name != "По складам" {
		t.Fatalf("имя варианта 0 = %q", r.Variants[0].Name)
	}
	if r.Variants[0].Composition == nil || len(r.Variants[0].Composition.Groupings) != 2 ||
		r.Variants[0].Composition.Groupings[0] != "Склад" {
		t.Fatalf("composition варианта 0: %+v", r.Variants[0].Composition)
	}
	if r.Variants[1].Name != "Кросс по месяцам" {
		t.Fatalf("имя варианта 1 = %q", r.Variants[1].Name)
	}
	if r.Variants[1].Composition == nil || len(r.Variants[1].Composition.Columns) != 1 ||
		r.Variants[1].Composition.Columns[0] != "Месяц" {
		t.Fatalf("columns варианта 1: %+v", r.Variants[1].Composition)
	}
}

func TestParseNoVariants(t *testing.T) {
	r, err := ParseBytes([]byte("name: X\nquery: \"ВЫБРАТЬ 1\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Variants != nil {
		t.Fatalf("Variants must be nil when absent, got %+v", r.Variants)
	}
}

func TestActiveComposition(t *testing.T) {
	main := &Composition{Groupings: []string{"Менеджер"}}
	byWh := &Composition{Groupings: []string{"Склад"}}
	r := &Report{
		Composition: main,
		Variants: []ReportVariant{
			{Name: "По складам", Composition: byWh},
		},
	}
	// Пустое имя → основной вариант.
	if got := r.ActiveComposition(""); got != main {
		t.Fatalf("пустое имя: ожидали основной composition, получили %+v", got)
	}
	// Имя варианта → его composition.
	if got := r.ActiveComposition("По складам"); got != byWh {
		t.Fatalf("\"По складам\": ожидали вариант, получили %+v", got)
	}
	// Неизвестное имя → основной (graceful fallback).
	if got := r.ActiveComposition("Нет такого"); got != main {
		t.Fatalf("неизвестное имя: ожидали основной composition, получили %+v", got)
	}
}
