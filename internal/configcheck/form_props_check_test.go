package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
)

// Заявка #1492: автор потратил вечер на подбор props, которые платформа молча
// игнорирует. Проверка обязана называть именно это — и обязана молчать о
// ключах, которые кто-то действительно читает, иначе она превратится в шум на
// каждой импортированной из 1С форме.

func TestCheckFormProps_UnusedKeyWarns(t *testing.T) {
	warns := CheckFormProps(projWithElement(&metadata.FormElement{
		Kind:  metadata.FormElementField,
		Name:  "Контрагент",
		Props: map[string]any{"РастягиватьПоГоризонтали": true},
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
	}
	w := warns[0]
	if w.Code != "form.prop-unused" {
		t.Errorf("Code = %q, ожидался form.prop-unused", w.Code)
	}
	// Форма и элемент — обязательная часть сообщения: без них искать некуда.
	if w.File != "forms/входящееписьмо/объекта.form.yaml" {
		t.Errorf("File = %q — в предупреждении нет пути формы", w.File)
	}
	if !strings.Contains(w.Message, `"Контрагент"`) {
		t.Errorf("в сообщении нет имени элемента: %s", w.Message)
	}
	if !strings.Contains(w.Message, "РастягиватьПоГоризонтали") {
		t.Errorf("в сообщении нет самого ключа: %s", w.Message)
	}
	// Формулировка называет, КЕМ ключ не используется. «Неизвестный ключ» тут
	// было бы враньём: у 1С есть корректные имена, которых не знает рантайм.
	if !strings.Contains(w.Message, "не используется управляемой формой") {
		t.Errorf("формулировка не называет потребителя: %s", w.Message)
	}
	if !strings.Contains(w.SuggestedFix, "halign") {
		t.Errorf("подсказка не называет рабочий ключ раскладки: %s", w.SuggestedFix)
	}
}

func TestCheckFormProps_ConsumedKeysSilent(t *testing.T) {
	cases := []struct {
		name  string
		props map[string]any
	}{
		{"экспортный ключ 1С", map[string]any{"HorizontalStretch": true}},
		{"вид элемента 1С", map[string]any{"Type": "CommandBarButton"}},
		{"пометка конвертера decoration", map[string]any{metadata.FormPropKeyDecoration: true}},
		{"пометка конвертера in_command_bar", map[string]any{metadata.FormPropKeyInCommandBar: true}},
		{"носитель round-trip", map[string]any{"ContextMenu_name": "Меню", "ChoiceList_xml": "<ChoiceList/>"}},
		{"props вовсе нет", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			warns := CheckFormProps(projWithElement(&metadata.FormElement{
				Kind:  metadata.FormElementField,
				Name:  "Поле",
				Props: c.props,
			}))
			if len(warns) != 0 {
				t.Errorf("предупреждение о ключе, который читает потребитель: %+v", warns)
			}
		})
	}
}

// Регистр 1С-имён значим: writeKnownProps сравнивает ключ точно. Поэтому
// «horizontalstretch» — всё ещё мимо, но человеку надо сказать почему.
func TestCheckFormProps_WrongCaseWarnsWithSpelling(t *testing.T) {
	warns := CheckFormProps(projWithElement(&metadata.FormElement{
		Kind:  metadata.FormElementField,
		Name:  "Поле",
		Props: map[string]any{"horizontalstretch": true},
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получили %d: %+v", len(warns), warns)
	}
	if !strings.Contains(warns[0].SuggestedFix, `"HorizontalStretch"`) {
		t.Errorf("подсказка не называет правильное написание: %s", warns[0].SuggestedFix)
	}
}

// Вложенный элемент: props на реквизите внутри группы теряться не должны —
// именно там их обычно и пишут.
func TestCheckFormProps_NestedElementCovered(t *testing.T) {
	warns := CheckFormProps(projWithElement(&metadata.FormElement{
		Kind: metadata.FormElementGroupBox,
		Name: "Группа",
		Children: []*metadata.FormElement{{
			Kind:  metadata.FormElementField,
			Name:  "Сумма",
			Props: map[string]any{"АвтоМаксимальнаяШирина": true},
		}},
	}))
	if len(warns) != 1 {
		t.Fatalf("вложенный элемент не проверен: %+v", warns)
	}
	if !strings.Contains(warns[0].Message, `"Сумма"`) {
		t.Errorf("названо не то имя элемента: %s", warns[0].Message)
	}
}

// Формы обработок живут не в Entities, а в Processors — проверка обязана
// доходить и туда.
func TestCheckFormProps_ProcessorFormCovered(t *testing.T) {
	proj := &project.Project{
		Processors: []*processor.Processor{{
			Name: "ЗагрузкаПрайса",
			Forms: []*metadata.FormModule{{
				Name: "объекта",
				Elements: []*metadata.FormElement{{
					Kind:  metadata.FormElementField,
					Name:  "Файл",
					Props: map[string]any{"ТолькоПросмотр": true},
				}},
			}},
		}},
	}
	warns := CheckFormProps(proj)
	if len(warns) != 1 {
		t.Fatalf("форма обработки не проверена: %+v", warns)
	}
	if warns[0].File != "forms/загрузкапрайса/объекта.form.yaml" {
		t.Errorf("File = %q — путь формы обработки собран неверно", warns[0].File)
	}
	if warns[0].Object != "ЗагрузкаПрайса" {
		t.Errorf("Object = %q, ожидалась обработка", warns[0].Object)
	}
}

// Несколько неизвестных ключей на одном элементе выводятся в стабильном
// порядке: иначе вывод check прыгает от запуска к запуску.
func TestCheckFormProps_StableOrder(t *testing.T) {
	el := &metadata.FormElement{
		Kind:  metadata.FormElementField,
		Name:  "Поле",
		Props: map[string]any{"Яблоко": 1, "Арбуз": 2, "Банан": 3},
	}
	var first []string
	for i := 0; i < 5; i++ {
		warns := CheckFormProps(projWithElement(el))
		if len(warns) != 3 {
			t.Fatalf("ожидалось 3 предупреждения, получили %d", len(warns))
		}
		got := []string{warns[0].Message, warns[1].Message, warns[2].Message}
		if first == nil {
			first = got
			continue
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("порядок предупреждений не стабилен: %q vs %q", got[j], first[j])
			}
		}
	}
}

// Критерий заявки #1492 целиком, через публичный вход: форма с
// `props: {РастягиватьПоГоризонтали: true}` даёт предупреждение `onebase
// check`, а форма с ключом, который читает экспорт в 1С, — не даёт.
func TestRunFullWarnsAboutUnusedFormProp(t *testing.T) {
	write := func(t *testing.T, props string) Result {
		t.Helper()
		dir := t.TempDir()
		mkFile(t, filepath.Join(dir, "catalogs", "контрагент.yaml"), `name: Контрагент
fields:
  - name: Наименование
    type: string
`)
		mkFile(t, filepath.Join(dir, "forms", "контрагент", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагент
elements:
  - kind: Поле
    name: Наименование
    data_path: Объект.Наименование
    props:
`+props)
		return RunFull(dir)
	}

	t.Run("ключ, который не читает никто", func(t *testing.T) {
		res := write(t, "      РастягиватьПоГоризонтали: true\n")
		for _, w := range res.Warnings {
			if w.Code == "form.prop-unused" && strings.Contains(w.Message, "РастягиватьПоГоризонтали") {
				return
			}
		}
		t.Fatalf("onebase check промолчал о неиспользуемом props: %+v", res.Warnings)
	})

	t.Run("ключ, который читает экспорт в 1С", func(t *testing.T) {
		res := write(t, "      HorizontalStretch: true\n")
		for _, w := range res.Warnings {
			if w.Code == "form.prop-unused" {
				t.Fatalf("предупреждение о рабочем 1С-ключе: %+v", w)
			}
		}
	})
}
