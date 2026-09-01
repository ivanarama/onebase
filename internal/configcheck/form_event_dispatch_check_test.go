package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// projWithFormHandlers собирает минимальный проект с одной формой сущности
// «Контрагент»: обработчики уровня формы и один элемент со своими.
func projWithFormHandlers(formHandlers map[metadata.FormEventType]string, el *metadata.FormElement) *project.Project {
	elements := []*metadata.FormElement(nil)
	if el != nil {
		elements = []*metadata.FormElement{el}
	}
	return &project.Project{
		Entities: []*metadata.Entity{
			{
				Name: "Контрагент",
				Forms: []*metadata.FormModule{
					{Name: "объекта", Handlers: formHandlers, Elements: elements},
				},
			},
		},
	}
}

func TestCheckFormEventDispatch_НевызываемоеСобытиеФормыПредупреждает(t *testing.T) {
	warns := CheckFormEventDispatch(projWithFormHandlers(map[metadata.FormEventType]string{
		metadata.FormEventBeforeClose: "ПередЗакрытиемФормы",
	}, nil))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d: %+v", len(warns), warns)
	}
	w := warns[0]
	if w.Code != "form.event-not-dispatched" {
		t.Errorf("Code = %q", w.Code)
	}
	if w.File != "forms/контрагент/объекта.form.yaml" {
		t.Errorf("File = %q, ожидался путь формы в нижнем регистре", w.File)
	}
	// Сообщение обязано называть и событие, и процедуру: конфигуратор ищет в
	// .form.os именно процедуру, а событие — в .form.yaml.
	if !strings.Contains(w.Message, "ПередЗакрытием") || !strings.Contains(w.Message, "ПередЗакрытиемФормы") {
		t.Errorf("сообщение не называет событие и процедуру: %q", w.Message)
	}
}

// Живое событие молчит. Это половина ценности проверки: предупреждение на
// рабочем обработчике увело бы чинить то, что работает.
func TestCheckFormEventDispatch_ЖивыеСобытияМолчат(t *testing.T) {
	handlers := map[metadata.FormEventType]string{}
	for _, event := range append(metadata.BrowserFormEvents(), metadata.ServerFormEvents()...) {
		handlers[event] = "Обработчик" + string(event)
	}
	if warns := CheckFormEventDispatch(projWithFormHandlers(handlers, nil)); len(warns) != 0 {
		t.Fatalf("ложное срабатывание на вызываемых событиях: %+v", warns)
	}
}

// Пустое значение обработчиком не является: `ПриЗакрытии:` без процедуры ничего
// не объявляет, ругаться там не на что.
func TestCheckFormEventDispatch_ПустойОбработчикНеСчитается(t *testing.T) {
	if warns := CheckFormEventDispatch(projWithFormHandlers(map[metadata.FormEventType]string{
		metadata.FormEventOnClose: "   ",
	}, nil)); len(warns) != 0 {
		t.Fatalf("пустой обработчик не должен предупреждать: %+v", warns)
	}
}

// Обработчик на элементе ловится так же, как на форме, и предупреждение
// называет элемент — иначе в форме на полсотни элементов его не найти.
func TestCheckFormEventDispatch_ОбработчикНаЭлементе(t *testing.T) {
	warns := CheckFormEventDispatch(projWithFormHandlers(nil, &metadata.FormElement{
		Kind: metadata.FormElementField,
		Name: "ПолеКонтрагент",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventItemChoice: "ОбработкаВыбораКонтрагента",
		},
	}))
	if len(warns) != 1 {
		t.Fatalf("ожидалось 1 предупреждение, получено %d: %+v", len(warns), warns)
	}
	if !strings.Contains(warns[0].Message, "ПолеКонтрагент") {
		t.Errorf("сообщение не называет элемент: %q", warns[0].Message)
	}
	// Для ОбработкаВыбора замена есть — подсказка обязана её назвать.
	if !strings.Contains(warns[0].SuggestedFix, "Выбор") {
		t.Errorf("подсказка не называет живое событие Выбор: %q", warns[0].SuggestedFix)
	}
}

// Обработчик во вложенном элементе (страница → группа → поле) тоже ловится:
// managed-формы почти всегда дерево, плоских не бывает.
func TestCheckFormEventDispatch_ВложенныйЭлемент(t *testing.T) {
	warns := CheckFormEventDispatch(projWithFormHandlers(nil, &metadata.FormElement{
		Kind: metadata.FormElementGroupBox,
		Name: "Группа",
		Children: []*metadata.FormElement{{
			Kind: metadata.FormElementField,
			Name: "ПолеСумма",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnActivate: "ПриАктивацииПоля",
			},
		}},
	}))
	if len(warns) != 1 {
		t.Fatalf("вложенный обработчик пропущен: %+v", warns)
	}
}

// Порядок сообщений не зависит от обхода карты обработчиков: два прогона одной
// конфигурации обязаны печатать одно и то же, иначе diff отчёта проверки шумит.
func TestCheckFormEventDispatch_ПорядокУстойчив(t *testing.T) {
	handlers := map[metadata.FormEventType]string{}
	for _, event := range metadata.UncalledFormEvents() {
		handlers[event] = "Обработчик" + string(event)
	}
	first := CheckFormEventDispatch(projWithFormHandlers(handlers, nil))
	if len(first) != len(handlers) {
		t.Fatalf("предупреждений %d, обработчиков %d", len(first), len(handlers))
	}
	for i := 0; i < 5; i++ {
		next := CheckFormEventDispatch(projWithFormHandlers(handlers, nil))
		for j := range first {
			if next[j].Message != first[j].Message {
				t.Fatalf("порядок плавает на месте %d", j)
			}
		}
	}
}

// Точка входа пользователя — `onebase check`, то есть RunFull. Проверка обязана
// работать без --lint: совет линта необязателен, а мёртвый обработчик — не
// совет, это разрыв между тем, что конфигурация объявила, и тем, что движок
// исполнит. И обязана оставаться ПРЕДУПРЕЖДЕНИЕМ: конфигурации с такими
// обработчиками уже существуют (импорт из 1С), ронять на них check нельзя.
func TestRunFull_ПредупреждаетОНевызываемомСобытииФормы(t *testing.T) {
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
  - kind: ПолеВвода
    name: ПолеНаименование
    data_path: Объект.Наименование
events:
  ПриОткрытии: ПриОткрытииФормы
  ПередЗакрытием: ПередЗакрытиемФормы
`)
	mkFile(t, filepath.Join(dir, "forms", "контрагент", "объекта.form.os"), `Процедура ПриОткрытииФормы()
КонецПроцедуры

Процедура ПередЗакрытиемФормы()
	Сообщить("Прощай");
КонецПроцедуры
`)

	res := RunFull(dir)

	if !res.OK {
		t.Fatalf("RunFull отказал: мёртвый обработчик обязан быть предупреждением, а не ошибкой: %+v", res.Issues)
	}
	var found *Issue
	for i, w := range res.Warnings {
		if w.Code == "form.event-not-dispatched" {
			found = &res.Warnings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("нет предупреждения о невызываемом событии: %+v", res.Warnings)
	}
	if !strings.Contains(found.Message, "ПередЗакрытием") {
		t.Errorf("предупреждение не называет событие: %q", found.Message)
	}
	if strings.Contains(found.Message, "ПриОткрытии") {
		t.Errorf("предупреждение задело живое событие ПриОткрытии: %q", found.Message)
	}
}
