package metadata

import "testing"

// Таблица браузерных событий — источник правды сразу для трёх читателей:
// маршрутизатора событий, сторожа руководства и (через них) проверки
// конфигураций. Опечатка в ней тише всего: пара просто перестаёт разрешаться,
// а имя, которого нет в словаре платформы, не разрешится никогда.
func TestBrowserFormEvents_ИменаИзСловаряПлатформы(t *testing.T) {
	check := func(where string, events []FormEventType) {
		for _, event := range events {
			if !IsKnownFormEventType(event) {
				t.Errorf("%s: событие %q отсутствует в словаре платформы", where, event)
			}
		}
	}
	for _, rule := range browserFormEventRules {
		for _, kind := range rule.kinds {
			if !IsKnownFormElementType(kind) || kind == "" {
				t.Errorf("таблица разрешает события неизвестному виду элемента %q", kind)
			}
		}
		check("таблица элементов", rule.events)
	}
	check("события команды", browserFormCommandEvents)
	check("события уровня формы", browserFormLevelEvents)
}

// Вид элемента обязан встречаться в таблице один раз: вторая строка про тот же
// вид молча проигрывает первой — BrowserFormEventsFor возвращает найденную
// раньше, и половина перечня перестаёт действовать, не сказав ни слова.
func TestBrowserFormEvents_ВидЭлементаНеПовторяется(t *testing.T) {
	seen := make(map[FormElementType]bool)
	for _, rule := range browserFormEventRules {
		for _, kind := range rule.kinds {
			if seen[kind] {
				t.Errorf("вид элемента %q описан в таблице дважды", kind)
			}
			seen[kind] = true
		}
	}
}

// BrowserFormEventAllowed и BrowserFormEventsFor обязаны отвечать одно и то же:
// первым пользуется рантайм, вторым — руководство и редактор форм. Разойдясь,
// они дадут документированное событие, которое сервер отклоняет.
func TestBrowserFormEvents_ПроверкаПарыСходитсяСПеречнем(t *testing.T) {
	kinds := append(KnownFormElementTypes(), FormElementType("ВыдуманныйВид"), "")
	for _, kind := range kinds {
		listed := make(map[FormEventType]bool)
		for _, event := range BrowserFormEventsFor(kind) {
			listed[event] = true
		}
		for event := range knownFormEventTypes {
			if got, want := BrowserFormEventAllowed(kind, event), listed[event]; got != want {
				t.Errorf("%q + %q: BrowserFormEventAllowed=%v, в перечне=%v", kind, event, got, want)
			}
		}
	}
}

// Fail-closed: вид, который ничего не отправляет, не отправляет и незнакомое.
func TestBrowserFormEvents_ПассивныеВидыМолчат(t *testing.T) {
	for _, kind := range []FormElementType{
		FormElementLabel, FormElementGroupBox, FormElementPage, FormElementPages,
		FormElementPicture, FormElementType("ВыдуманныйВид"), "",
	} {
		if events := BrowserFormEventsFor(kind); len(events) != 0 {
			t.Errorf("вид %q не отправляет событий, а перечень выдал %v", kind, events)
		}
		if BrowserFormEventAllowed(kind, FormEventOnChange) {
			t.Errorf("вид %q разрешил ПриИзменении", kind)
		}
	}
}

// Колонка табличной части шлёт только правку своей ячейки (план 154):
// остальные события строки принадлежат таблице целиком. Решение
// продуктовое — меняя его, тест правят осознанно.
func TestBrowserFormEvents_КолонкаТолькоПриИзменении(t *testing.T) {
	got := BrowserFormEventsFor(FormElementColumn)
	if len(got) != 1 || got[0] != FormEventOnChange {
		t.Errorf("колонка ТЧ отправляет %v, ожидалось только %q", got, FormEventOnChange)
	}
	for _, event := range []FormEventType{
		FormEventOnRowAdded, FormEventOnRowDeleted, FormEventOnRowActivated,
	} {
		if BrowserFormEventAllowed(FormElementColumn, event) {
			t.Errorf("колонка ТЧ разрешила событие строки %q", event)
		}
	}
}

// BrowserFormEvents — объединение всех трёх источников без повторов. Событие
// уровня формы и событие команды в нём обязаны быть: элементной таблицей они
// не покрываются, а руководство сверяется именно с объединением.
func TestBrowserFormEvents_ОбъединениеБезПовторов(t *testing.T) {
	events := BrowserFormEvents()
	seen := make(map[FormEventType]bool, len(events))
	for _, event := range events {
		if seen[event] {
			t.Errorf("BrowserFormEvents повторяет %q", event)
		}
		seen[event] = true
	}
	for _, want := range append(BrowserFormLevelEvents(), BrowserFormCommandEvents()...) {
		if !seen[want] {
			t.Errorf("BrowserFormEvents не содержит %q", want)
		}
	}
	for _, rule := range browserFormEventRules {
		for _, want := range rule.events {
			if !seen[want] {
				t.Errorf("BrowserFormEvents не содержит %q из таблицы элементов", want)
			}
		}
	}
}

// Перечни отдаются копиями: таблица — общая на процесс, и правка у одного
// читателя молча меняла бы поведение маршрутизатора событий у всех.
func TestBrowserFormEvents_ОтдаютсяКопии(t *testing.T) {
	got := BrowserFormEventsFor(FormElementInputList)
	if len(got) == 0 {
		t.Fatal("ПолеСписка не отправляет событий")
	}
	got[0] = "ИспорченноеИмя"
	if again := BrowserFormEventsFor(FormElementInputList); again[0] == "ИспорченноеИмя" {
		t.Error("BrowserFormEventsFor отдал ссылку на таблицу, а не копию")
	}

	level := BrowserFormLevelEvents()
	level[0] = "ИспорченноеИмя"
	if BrowserFormLevelEvents()[0] == "ИспорченноеИмя" {
		t.Error("BrowserFormLevelEvents отдал ссылку на таблицу, а не копию")
	}

	commands := BrowserFormCommandEvents()
	commands[0] = "ИспорченноеИмя"
	if BrowserFormCommandEvents()[0] == "ИспорченноеИмя" {
		t.Error("BrowserFormCommandEvents отдал ссылку на таблицу, а не копию")
	}
}
