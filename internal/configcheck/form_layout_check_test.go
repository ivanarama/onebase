package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Раскладка, которую рантайм не применит, обязана называться на check, а не
// обнаруживаться разглядыванием формы: ровно так и нашли саму заявку #1185 —
// чтением шаблона рендера.

func TestCheckFormLayout_UnknownAlignWarns(t *testing.T) {
	warns := CheckFormLayout(projWithElement(&metadata.FormElement{
		Kind:            metadata.FormElementField,
		Name:            "Поле",
		HorizontalAlign: "centre",
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
	}
	if warns[0].Code != "form.layout-align" {
		t.Errorf("Code = %q, ожидался form.layout-align", warns[0].Code)
	}
	// Подсказка обязана перечислить рабочие значения — иначе предупреждение
	// сообщает о проблеме, но не о том, что писать вместо.
	for _, v := range metadata.FormHAlignValues {
		if !strings.Contains(warns[0].SuggestedFix, v) {
			t.Errorf("в подсказке нет значения %q: %s", v, warns[0].SuggestedFix)
		}
	}
}

func TestCheckFormLayout_KnownValuesSilent(t *testing.T) {
	for _, el := range []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Поле"},
		{Kind: metadata.FormElementField, Name: "Поле", Width: 200, Height: 100},
		{Kind: metadata.FormElementField, Name: "Поле", HorizontalAlign: "Center", VerticalAlign: "middle"},
		{Kind: metadata.FormElementField, Name: "Поле", HorizontalAlign: "stretch"},
	} {
		if warns := CheckFormLayout(projWithElement(el)); len(warns) != 0 {
			t.Errorf("рабочая раскладка помечена зря (%+v): %+v", el, warns)
		}
	}
}

func TestCheckFormLayout_BadSizeWarns(t *testing.T) {
	cases := []struct {
		name     string
		el       *metadata.FormElement
		contains string
	}{
		{"отрицательная ширина", &metadata.FormElement{Name: "Поле", Width: -10}, "отрицательный"},
		{"высота за потолком", &metadata.FormElement{Name: "Поле", Height: metadata.FormLayoutMaxSize + 1}, "потолка"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			warns := CheckFormLayout(projWithElement(c.el))
			if len(warns) != 1 {
				t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
			}
			if warns[0].Code != "form.layout-size" {
				t.Errorf("Code = %q, ожидался form.layout-size", warns[0].Code)
			}
			if !strings.Contains(warns[0].Message, c.contains) {
				t.Errorf("текст не называет причину (%q): %s", c.contains, warns[0].Message)
			}
		})
	}
}

// stretch съедает явную ширину — молчать об этом значит повторить ту же ошибку,
// из-за которой заведена заявка: ключ есть, а эффекта нет.
func TestCheckFormLayout_StretchWithWidthWarns(t *testing.T) {
	warns := CheckFormLayout(projWithElement(&metadata.FormElement{
		Name:            "Поле",
		Width:           200,
		HorizontalAlign: "stretch",
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
	}
	if !strings.Contains(warns[0].Message, "stretch") {
		t.Errorf("текст не называет причину: %s", warns[0].Message)
	}
}

// Проверка обязана доехать до самого `onebase check`: тест на функции, которую
// боевой путь не зовёт, зелёный и бесполезный (#611). Поэтому — RunFull по
// настоящему каталогу проекта.
func TestRunFull_FormLayoutWarnsThroughCheck(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "заказ.yaml"), `name: Заказ
fields:
  - name: Состояние
    type: string
`)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: объекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеСостояние
    data_path: Объект.Состояние
    halign: centre
    width: -10
`)

	res := RunFull(dir)
	var got []Issue
	for _, w := range res.Warnings {
		if strings.HasPrefix(w.Code, "form.layout-") {
			got = append(got, w)
		}
	}
	if len(got) != 2 {
		t.Fatalf("предупреждений о раскладке = %d, ожидалось 2: %+v", len(got), res.Warnings)
	}
	for _, w := range got {
		if w.File != "forms/заказ/объекта.form.yaml" {
			t.Errorf("предупреждение не привязано к файлу формы: %+v", w)
		}
	}
	// Раскладка — предупреждение, а не ошибка: ключи годами принимались молча,
	// и валить сборку задним числом было бы наказанием за прежнее молчание.
	if !res.OK {
		t.Errorf("раскладка не должна блокировать check: %+v", res.Issues)
	}
}

// Вид без собственного блока (колонка, командная панель) раскладку применить не
// может — и обязан об этом сказать, а не молчать так же, как молчал рантайм.
func TestCheckFormLayout_KindWithoutBlockWarns(t *testing.T) {
	for _, kind := range []metadata.FormElementType{
		metadata.FormElementColumn,
		metadata.FormElementCommandBar,
		metadata.FormElementCommandBarButton,
	} {
		t.Run(string(kind), func(t *testing.T) {
			warns := CheckFormLayout(projWithElement(&metadata.FormElement{
				Kind: kind, Name: "Элемент", Width: 100,
			}))
			if len(warns) != 1 {
				t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
			}
			if warns[0].Code != "form.layout-unsupported-kind" {
				t.Errorf("Code = %q", warns[0].Code)
			}
		})
	}
	// Без ключей раскладки такие элементы молчат.
	if warns := CheckFormLayout(projWithElement(&metadata.FormElement{
		Kind: metadata.FormElementColumn, Name: "Колонка",
	})); len(warns) != 0 {
		t.Errorf("колонка без раскладки помечена зря: %+v", warns)
	}
}
