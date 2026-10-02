package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Ключи формы проверяются по РАЗМЕТКЕ, которую увидит браузер: «ключ принят
// загрузчиком» ничего не говорит о том, изменилось ли поведение, а именно это
// и обещает конфигурация.
func renderFormKeysHTML(t *testing.T, entity *metadata.Entity, form *metadata.FormModule, values map[string]string, refOptions map[string][]map[string]any, admin bool) string {
	t.Helper()
	data := map[string]any{
		"Entity": entity, "Form": form, "IsNew": true, "CanWrite": true,
		"Values":        values,
		"RefOptions":    refOptions,
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": loadChoiceOptions(form, "ru"),
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{},
		"IsAdmin":       admin,
		"User":          nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func TestFormGroupScrollXReplacesWrapping(t *testing.T) {
	button := func(name string) *metadata.FormElement {
		return &metadata.FormElement{Kind: metadata.FormElementButton, Name: name}
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заявка", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementGroupBox, Name: "ГруппаДействий", Orientation: "horizontal", ScrollX: true,
				Children: []*metadata.FormElement{button("Кн1"), button("Кн2")}},
			{Kind: metadata.FormElementGroupBox, Name: "ГруппаОбычная", Orientation: "horizontal",
				Children: []*metadata.FormElement{button("Кн3")}},
		},
	}
	entity := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Forms: []*metadata.FormModule{form}}

	html := renderFormKeysHTML(t, entity, form, map[string]string{}, nil, false)
	if !strings.Contains(html, `class="form-group-box managed-group-horizontal managed-group-scrollx"`) {
		t.Error("scroll_x не доехал до класса группы")
	}
	if strings.Contains(html, `data-ob-el="ГруппаОбычная"`) && strings.Count(html, "managed-group-scrollx\"") != 1 {
		t.Error("прокрутка включилась у группы, которая её не просила")
	}
	// Правило должно приехать вместе с разметкой: без него класс ничего не меняет.
	if !strings.Contains(html, ".managed-group-scrollx>.managed-group-body{flex-wrap:nowrap;overflow-x:auto}") {
		t.Error("CSS прокрутки не попал в страницу")
	}
}

func TestFormPrimaryButtonGetsAccentStyle(t *testing.T) {
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Задача", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementButton, Name: "КнВыполнить", Primary: true},
			{Kind: metadata.FormElementButton, Name: "КнОтмена"},
		},
	}
	entity := &metadata.Entity{Name: "Задача", Kind: metadata.KindDocument, Forms: []*metadata.FormModule{form}}

	html := renderFormKeysHTML(t, entity, form, map[string]string{}, nil, false)
	primary := strings.Index(html, `class="btn btn-primary managed-btn"`)
	secondary := strings.Index(html, `class="btn btn-secondary managed-btn"`)
	if primary < 0 {
		t.Fatal("основная кнопка не получила акцентный стиль")
	}
	if secondary < 0 {
		t.Fatal("вспомогательная кнопка потеряла обычный стиль")
	}
	if primary > secondary {
		t.Error("акцент достался не той кнопке")
	}
}

func TestChoiceDropdownFalseLeavesOnlyCurrentValue(t *testing.T) {
	current := "11111111-1111-1111-1111-111111111111"
	options := map[string][]map[string]any{"Филиал": {
		{"id": current, "_label": "Филиал А"},
		{"id": "22222222-2222-2222-2222-222222222222", "_label": "Филиал Б"},
		{"id": "33333333-3333-3333-3333-333333333333", "_label": "Филиал В"},
	}}
	entity := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Филиал", Type: metadata.FieldType("reference:Филиал"), RefEntity: "Филиал"}},
	}
	collapsed := false
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заявка", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementField, Name: "ПолеФилиал", DataPath: "Объект.Филиал",
			Choice: true, ChoiceDropdown: &collapsed,
		}},
	}
	entity.Forms = []*metadata.FormModule{form}
	values := map[string]string{"Филиал": current}

	html := renderFormKeysHTML(t, entity, form, values, options, false)
	if !strings.Contains(html, "Филиал А") {
		t.Error("текущее значение исчезло из списка — заполненное поле выглядит пустым")
	}
	if strings.Contains(html, "Филиал Б") || strings.Contains(html, "Филиал В") {
		t.Error("choice_dropdown: false не свернул предзагруженную страницу вариантов")
	}
	if !strings.Contains(html, `data-ref-choice-dropdown="false"`) {
		t.Error("список не передал JS признак choice_dropdown: false")
	}
	// Выбор уходит в форму подбора, значит кнопка подбора обязана остаться.
	if !strings.Contains(html, `data-ob-ref-picker="ref-Филиал"`) {
		t.Error("кнопка подбора пропала — выбирать значение стало нечем")
	}

	form.Elements[0].ChoiceDropdown = nil
	expanded := renderFormKeysHTML(t, entity, form, values, options, false)
	for _, label := range []string{"Филиал А", "Филиал Б", "Филиал В"} {
		if !strings.Contains(expanded, label) {
			t.Errorf("без ключа список сузился: нет %q", label)
		}
	}
}
