package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func boolPtrGroup(v bool) *bool { return &v }

// Галка «Отображать заголовок» по умолчанию включена: форма с заголовком группы
// рисует <legend>, а show_title: false его убирает (#164, оформление группы).
func TestManagedGroup_ShowTitle(t *testing.T) {
	group := func(showTitle *bool) *metadata.FormElement {
		return &metadata.FormElement{
			Kind:      metadata.FormElementGroupBox,
			Name:      "Реквизиты",
			TitleMap:  map[string]string{"ru": "Реквизиты"},
			ShowTitle: showTitle,
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "ПолеНомер", DataPath: "Объект.Номер"},
			},
		}
	}

	withTitle := renderGroupHTML(t, group(nil))
	if !strings.Contains(withTitle, ">Реквизиты</legend>") {
		t.Errorf("по умолчанию заголовок группы обязан быть в разметке:\n%s", withTitle)
	}

	noTitle := renderGroupHTML(t, group(boolPtrGroup(false)))
	if strings.Contains(noTitle, "<legend") {
		t.Errorf("show_title: false обязан убрать <legend>:\n%s", noTitle)
	}
	// Дети при этом остаются на месте: галка убирает подпись, а не содержимое.
	if !strings.Contains(noTitle, `data-ob-el="ПолеНомер"`) {
		t.Errorf("поле группы пропало вместе с заголовком:\n%s", noTitle)
	}

	explicit := renderGroupHTML(t, group(boolPtrGroup(true)))
	if !strings.Contains(explicit, ">Реквизиты</legend>") {
		t.Errorf("show_title: true — то же, что отсутствие ключа:\n%s", explicit)
	}
}

// Галка «Выделять группу» по умолчанию включена: рамка и внутренние отступы
// приходят из правила .form-group-box, а bordered: false снимает их классом
// ob-group-plain — иначе ключ не смог бы перебить inline-стиль.
func TestManagedGroup_Bordered(t *testing.T) {
	group := func(bordered *bool) *metadata.FormElement {
		return &metadata.FormElement{
			Kind:     metadata.FormElementGroupBox,
			Name:     "Реквизиты",
			Bordered: bordered,
			Children: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "ПолеНомер", DataPath: "Объект.Номер"},
			},
		}
	}

	withFrame := renderGroupHTML(t, group(nil))
	if !strings.Contains(withFrame, `class="form-group-box managed-group-vertical" data-ob-el="Реквизиты"`) {
		t.Errorf("разметка группы без ключа bordered изменилась:\n%s", withFrame)
	}
	// Рамку и отступы задаёт правило, а не inline-стиль: иначе ключ не смог бы их
	// снять, а вид формы — переопределить оформление.
	if !strings.Contains(withFrame, "fieldset.form-group-box{border:1px solid #e2e8f0;border-radius:8px;padding:12px 14px") {
		t.Error("рамка группы не в правиле fieldset.form-group-box")
	}
	if groupTag(withFrame) == "" || strings.Contains(groupTag(withFrame), "border:1px solid #e2e8f0") {
		t.Errorf("рамка группы осталась в inline-стиле — ключ bordered: false её не снимет: %q", groupTag(withFrame))
	}

	noFrame := renderGroupHTML(t, group(boolPtrGroup(false)))
	if !strings.Contains(groupTag(noFrame), metadata.FormGroupPlainClass) {
		t.Errorf("bordered: false обязан дать класс %q в теге группы: %q", metadata.FormGroupPlainClass, groupTag(noFrame))
	}
	if !strings.Contains(noFrame, "fieldset.form-group-box.ob-group-plain{border:0;border-radius:0;padding:0}") {
		t.Error("в странице нет правила, снимающего рамку и отступы группы")
	}
	if !strings.Contains(noFrame, `data-ob-el="ПолеНомер"`) {
		t.Errorf("дети группы без рамки пропали:\n%s", noFrame)
	}
}

// groupTag вырезает открывающий тег группы «Реквизиты» из отрендеренной
// страницы: проверять наличие класса по всей странице нельзя — он встречается
// в правиле CSS, которое печатается на той же странице.
func groupTag(page string) string {
	at := strings.Index(page, `data-ob-el="Реквизиты"`)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(page[:at], "<fieldset")
	if start < 0 {
		return ""
	}
	end := strings.Index(page[at:], ">")
	if end < 0 {
		return ""
	}
	return page[start : at+end+1]
}

// renderGroupHTML рендерит managed-форму с единственной группой верхнего уровня.
func renderGroupHTML(t *testing.T, group *metadata.FormElement) string {
	t.Helper()
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заказ",
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   []*metadata.FormElement{group},
	}
	entity := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{form},
	}
	return renderManagedForm(t, entity, form, map[string]string{})
}
