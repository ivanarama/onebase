package metadata

import (
	"strings"
	"testing"
)

// Контракт раскладки (#1185): ключи width/height/halign/valign, которые до этого
// принимались YAML и молча не действовали. Проверяем ровно то, что обещано
// пользователю: пиксели у внешнего блока, словарь выравнивания, и — главное —
// что форма БЕЗ этих ключей не меняется ни на символ.

func TestFormElementLayoutCSS_EmptyWithoutKeys(t *testing.T) {
	for _, el := range []*FormElement{
		nil,
		{Kind: FormElementField, Name: "Поле"},
		{Kind: FormElementField, Name: "Поле", Width: 0, Height: 0, HorizontalAlign: "", VerticalAlign: ""},
	} {
		if css := FormElementLayoutCSS(el); css != "" {
			t.Errorf("элемент без ключей раскладки обязан рисоваться прежней разметкой, получено style=%q", css)
		}
	}
}

func TestFormElementLayoutCSS_SizeInPixels(t *testing.T) {
	css := FormElementLayoutCSS(&FormElement{Kind: FormElementField, Width: 200, Height: 120})
	for _, want := range []string{"width:200px", "max-width:100%", "height:120px", "min-width:0"} {
		if !strings.Contains(css, want) {
			t.Errorf("в стиле нет %q: %s", want, css)
		}
	}
	// Без flex:0 0 auto ширина действовала бы только в вертикальной раскладке:
	// flex-basis горизонтальной группы перебивает width.
	if !strings.Contains(css, "flex:0 0 auto") {
		t.Errorf("ширина без flex-basis не переживёт горизонтальную группу: %s", css)
	}
}

func TestFormElementLayoutCSS_IgnoresGarbageSizes(t *testing.T) {
	for _, el := range []*FormElement{
		{Width: -10},
		{Height: -1},
		{Width: FormLayoutMaxSize + 1},
		{Height: FormLayoutMaxSize + 1},
	} {
		if css := FormElementLayoutCSS(el); css != "" {
			t.Errorf("мусорный размер обязан игнорироваться, получено %q", css)
		}
	}
	// Ровно потолок — ещё рабочее значение.
	if css := FormElementLayoutCSS(&FormElement{Width: FormLayoutMaxSize}); !strings.Contains(css, "width:4000px") {
		t.Errorf("значение на потолке должно применяться: %q", css)
	}
}

func TestFormElementLayoutCSS_Alignment(t *testing.T) {
	cases := []struct {
		name    string
		el      FormElement
		want    []string
		notWant []string
	}{
		{"слева", FormElement{HorizontalAlign: "left"}, []string{"margin-right:auto"}, nil},
		{"по центру", FormElement{HorizontalAlign: "center"}, []string{"margin-left:auto", "margin-right:auto"}, nil},
		{"справа", FormElement{HorizontalAlign: "RIGHT"}, []string{"margin-left:auto"}, []string{"margin-right:auto"}},
		{"сверху", FormElement{VerticalAlign: "top"}, []string{"align-self:flex-start"}, nil},
		{"середина", FormElement{VerticalAlign: "middle"}, []string{"align-self:center"}, nil},
		{"снизу", FormElement{VerticalAlign: " bottom "}, []string{"align-self:flex-end"}, nil},
		{"неизвестное значение", FormElement{HorizontalAlign: "centre", VerticalAlign: "up"}, nil, []string{"margin", "align-self"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			css := FormElementLayoutCSS(&c.el)
			for _, w := range c.want {
				if !strings.Contains(css, w) {
					t.Errorf("нет %q в %q", w, css)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(css, w) {
					t.Errorf("лишнее %q в %q", w, css)
				}
			}
		})
	}
}

// stretch и явная ширина противоречат друг другу; побеждает stretch — и это
// ровно то, о чём предупреждает onebase check.
func TestFormElementLayoutCSS_StretchBeatsWidth(t *testing.T) {
	css := FormElementLayoutCSS(&FormElement{Width: 200, HorizontalAlign: "stretch"})
	if !strings.Contains(css, "width:100%") || strings.Contains(css, "200px") {
		t.Errorf("stretch обязан перебивать ширину: %q", css)
	}
	for _, want := range []string{"flex:1 1 100%", "min-width:0"} {
		if !strings.Contains(css, want) {
			t.Errorf("stretch не перебил flex-контракт горизонтальной группы (%s): %q", want, css)
		}
	}
}

// У ПолеКартинки width/height заняты размером самой картинки, поэтому общий
// контракт отдаёт для неё только выравнивание.
func TestFormElementAlignCSS_DropsSize(t *testing.T) {
	css := FormElementAlignCSS(&FormElement{Kind: FormElementPicture, Width: 64, Height: 64, HorizontalAlign: "center"})
	if strings.Contains(css, "px") {
		t.Errorf("размер картинки не должен попадать в стиль обёртки: %q", css)
	}
	if !strings.Contains(css, "margin-left:auto") {
		t.Errorf("выравнивание картинки потеряно: %q", css)
	}
}

func TestFormTablePartGridCSS(t *testing.T) {
	// Умолчание по числу строк — как было до контракта раскладки.
	if css := FormTablePartGridCSS(&FormElement{Kind: FormElementTablePart}, 3); !strings.Contains(css, "height:200px") || !strings.Contains(css, "width:100%") {
		t.Errorf("умолчание сетки изменилось: %q", css)
	}
	if css := FormTablePartGridCSS(&FormElement{Kind: FormElementTablePart}, 20); !strings.Contains(css, "height:300px") {
		t.Errorf("умолчание для длинной ТЧ изменилось: %q", css)
	}
	// Явные размеры перебивают умолчание.
	css := FormTablePartGridCSS(&FormElement{Kind: FormElementTablePart, Height: 500, Width: 640}, 20)
	if !strings.Contains(css, "height:500px") || !strings.Contains(css, "width:640px") {
		t.Errorf("размеры ТЧ не применились: %q", css)
	}
}

func TestFormElementFillsHeight(t *testing.T) {
	if FormElementFillsHeight(&FormElement{}) || FormElementFillsHeight(nil) {
		t.Error("без height растягивать нечего")
	}
	if !FormElementFillsHeight(&FormElement{Height: 120}) {
		t.Error("с height внешнему блоку нужен класс растяжки")
	}
	if FormElementFillsHeight(&FormElement{Height: -1}) {
		t.Error("мусорная высота не должна включать растяжку")
	}
}
