package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Вложенные группы с вариантом группировки: каждая группа раскладывает только
// своих детей, поэтому внутренняя группа со своим orientation не наследует
// вариант внешней и не переставляет соседей (#164, продолжение).
func TestManagedGroup_NestedOrientation(t *testing.T) {
	field := func(name string) *metadata.FormElement {
		return &metadata.FormElement{Kind: metadata.FormElementField, Name: name, DataPath: "Объект." + name}
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заказ", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementGroupBox, Name: "Шапка", Orientation: "horizontal",
			Children: []*metadata.FormElement{
				{
					Kind: metadata.FormElementGroupBox, Name: "Левая", Orientation: "vertical",
					Children: []*metadata.FormElement{field("Номер")},
				},
				{
					Kind: metadata.FormElementGroupBox, Name: "Правая", Orientation: "horizontal",
					Children: []*metadata.FormElement{field("Дата"), field("Сумма")},
				},
			},
		}},
	}
	entity := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		Forms: []*metadata.FormModule{form},
	}
	html := renderManagedForm(t, entity, form, map[string]string{})

	want := []string{
		// Внешняя группа — ряд, внутренние: столбец и ряд соответственно.
		`data-ob-el="Шапка"`,
		`data-ob-el="Левая"`,
		`data-ob-el="Правая"`,
		`class="form-group-box managed-group-horizontal" data-ob-el="Шапка"`,
		`class="form-group-box managed-group-vertical" data-ob-el="Левая"`,
		`class="form-group-box managed-group-horizontal" data-ob-el="Правая"`,
	}
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Errorf("в разметке нет %q", w)
		}
	}

	// Правила обоих вариантов обязаны приехать вместе с классами: иначе класс
	// ничего не меняет и раскладка остаётся прежней.
	css := newStyleCSS(t)
	for _, rule := range []string{
		"html.ob-style-new .card.ob-form-card .managed-group-horizontal > .managed-group-body",
		"html.ob-style-new .card.ob-form-card .managed-group-vertical > .managed-group-body",
		"--obn-gap:",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("в новом виде нет правила %q", rule)
		}
	}

	// Порядок вложенности: группа «Левая» открывается внутри тела «Шапки», а не
	// рядом с ним — рекурсия обязана остаться рекурсией.
	outer := strings.Index(html, `data-ob-el="Шапка"`)
	inner := strings.Index(html, `data-ob-el="Левая"`)
	if outer < 0 || inner < 0 || inner < outer {
		t.Fatalf("вложенная группа отрисована не внутри внешней: outer=%d inner=%d", outer, inner)
	}
}

// Группа без orientation раскладывается столбцом — так же, как до появления
// вариантов. Умолчание для НОВОЙ группы пишет конструктор, поэтому форма,
// написанная раньше, не меняет вид.
func TestManagedGroup_MissingOrientationIsVertical(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заказ", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementGroupBox, Name: "Реквизиты",
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "ПолеНомер", DataPath: "Объект.Номер"},
			},
		}},
	}
	entity := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	html := renderManagedForm(t, entity, form, map[string]string{})
	if !strings.Contains(html, `class="form-group-box managed-group-vertical" data-ob-el="Реквизиты"`) {
		t.Errorf("группа без orientation должна получить класс вертикали")
	}
	if strings.Contains(html, `managed-group-horizontal" data-ob-el="Реквизиты"`) {
		t.Errorf("группа без orientation не должна считаться горизонтальной")
	}
}

// newStyleCSS возвращает таблицу стилей нового вида — ту, что отдаётся по
// /static/theme-new-style.css.
func newStyleCSS(t *testing.T) string {
	t.Helper()
	if len(themeNewStyleCSS) == 0 {
		t.Fatal("theme-new-style.css не встроен в бинарь")
	}
	return string(themeNewStyleCSS)
}
