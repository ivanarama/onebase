package metadata

// Закрытая модель того, что вправе прислать браузер: пары «вид элемента формы →
// событие», события команды и события уровня формы. Словарь
// IsKnownFormEventType шире — он описывает всё, что конфигурация вправе
// объявить, включая серверные события, которые через браузерную точку входа не
// вызываются.
//
// Живёт в metadata, а не в internal/ui, потому что читателей у перечня двое:
// маршрутизатор событий (internal/ui/form_event_eligibility.go) и сторож
// руководства (internal/cli/aiguide_forms_test.go). Раньше таблица была
// неэкспортированной в internal/ui, и сторож держал её рукописную копию:
// одиннадцатое событие, добавленное в движок, он бы не заметил — руководство
// молча отстало бы, а тест остался зелёным (#1151). Соседние сторожа в том же
// файле ходят в IsKnownFormEventType и FormTablePartContextVars, этот теперь
// устроен так же.

// browserFormEventRule — строка таблицы: виды элементов с одинаковым набором
// событий. Список, а не карта, ради устойчивого порядка BrowserFormEvents.
type browserFormEventRule struct {
	kinds  []FormElementType
	events []FormEventType
}

var browserFormEventRules = []browserFormEventRule{
	{
		kinds:  []FormElementType{FormElementButton},
		events: []FormEventType{FormEventOnClick, FormEventOnChoice},
	},
	{
		kinds: []FormElementType{
			FormElementField, FormElementCodeField, FormElementCheckbox,
			FormElementDatePicker, FormElementSwitch,
		},
		events: []FormEventType{FormEventOnChange, FormEventOnChoice},
	},
	{
		kinds:  []FormElementType{FormElementInputList},
		events: []FormEventType{FormEventOnChange, FormEventStartChoice, FormEventOnChoice},
	},
	{
		kinds: []FormElementType{FormElementTablePart},
		events: []FormEventType{
			FormEventOnChange, FormEventOnRowAdded, FormEventOnRowDeleted,
			FormEventOnChoice, FormEventOnRowActivated, FormEventOnRowChanged,
			FormEventAfterRowAdd,
		},
	},
	{
		// Колонка табличной части шлёт только правку своей ячейки (план 154).
		// Остальные события строки принадлежат таблице целиком: строка
		// добавляется и удаляется не «в колонке».
		kinds:  []FormElementType{FormElementColumn},
		events: []FormEventType{FormEventOnChange},
	},
}

// browserFormCommandEvents — события команды формы, не размещённой на ней
// элементом: вида элемента у неё нет, поэтому в таблицу выше она не попадает.
var browserFormCommandEvents = []FormEventType{FormEventOnClick, FormEventOnChoice}

// browserFormLevelEvents — события, приходящие без имени элемента, на форму
// целиком. Остальные события уровня формы серверные: их запускает сервер на
// чтении и записи, из браузера они не вызываются.
var browserFormLevelEvents = []FormEventType{FormEventOnOpen}

// BrowserFormEventsFor возвращает копию перечня событий, которые браузер вправе
// отправить элементу формы данного вида. Вид, ничего не отправляющий (надпись,
// группа, страница), даёт пустой перечень.
func BrowserFormEventsFor(kind FormElementType) []FormEventType {
	for _, rule := range browserFormEventRules {
		for _, k := range rule.kinds {
			if k == kind {
				return append([]FormEventType(nil), rule.events...)
			}
		}
	}
	return nil
}

// BrowserFormEventAllowed — проверка пары «вид элемента → событие», fail-closed:
// незнакомый вид не отправляет ничего.
func BrowserFormEventAllowed(kind FormElementType, event FormEventType) bool {
	for _, rule := range browserFormEventRules {
		for _, k := range rule.kinds {
			if k != kind {
				continue
			}
			for _, e := range rule.events {
				if e == event {
					return true
				}
			}
			return false
		}
	}
	return false
}

// BrowserFormCommandEvents — события, которые браузер вправе отправить команде
// формы.
func BrowserFormCommandEvents() []FormEventType {
	return append([]FormEventType(nil), browserFormCommandEvents...)
}

// BrowserFormLevelEvents — события, которые браузер вправе отправить форме
// целиком, без имени элемента.
func BrowserFormLevelEvents() []FormEventType {
	return append([]FormEventType(nil), browserFormLevelEvents...)
}

// BrowserFormEvents — все события, которые браузер действительно отправляет:
// уровня формы, элементные и командные, каждое по одному разу. Порядок
// устойчив: перечни объявлены списками. Это тот перечень, который обязано
// называть руководство, — событие, выпавшее из него, читателю негде найти.
func BrowserFormEvents() []FormEventType {
	var out []FormEventType
	seen := make(map[FormEventType]bool)
	add := func(events []FormEventType) {
		for _, event := range events {
			if seen[event] {
				continue
			}
			seen[event] = true
			out = append(out, event)
		}
	}
	add(browserFormLevelEvents)
	for _, rule := range browserFormEventRules {
		add(rule.events)
	}
	add(browserFormCommandEvents)
	return out
}
