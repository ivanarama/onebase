package metadata

import "testing"

// Словарь событий формы обязан делиться на три непересекающиеся части без
// остатка: браузерные, серверные и невызываемые. Ради этого разбиения перечень
// невызываемых и вычисляется, а не пишется руками — тогда событие, добавленное
// в словарь и забытое в диспетчерах, само попадает в невызываемые и о нём
// скажет `onebase check`, а не пользователь через полгода (#1153).
func TestUncalledFormEvents_РазбиваютСловарьБезОстатка(t *testing.T) {
	classes := map[FormEventType]int{}
	for _, event := range BrowserFormEvents() {
		classes[event]++
	}
	for _, event := range ServerFormEvents() {
		classes[event]++
	}
	for _, event := range UncalledFormEvents() {
		classes[event]++
	}

	for event, n := range classes {
		if !IsKnownFormEventType(event) {
			t.Errorf("событие %q разнесено по классам, но словарю платформы неизвестно", event)
		}
		if n != 1 {
			t.Errorf("событие %q попало в %d класса, а классы обязаны не пересекаться", event, n)
		}
	}
	for event := range knownFormEventTypes {
		if classes[event] == 0 {
			t.Errorf("событие %q не отнесено ни к браузерным, ни к серверным, ни к невызываемым", event)
		}
	}
}

// Четыре события, с которых заявка началась: словарь их знает, конвертер 1С
// отображает, редактор подсказывает — а вызывающей стороны нет ни на клиенте,
// ни на сервере. Если какое-то из них реализуют, тест обязан упасть: тогда его
// надо убрать отсюда, а предупреждение `onebase check` исчезнет само.
func TestUncalledFormEvents_СодержатСобытияЗаявки(t *testing.T) {
	uncalled := map[FormEventType]bool{}
	for _, event := range UncalledFormEvents() {
		uncalled[event] = true
	}
	for _, event := range []FormEventType{
		FormEventBeforeClose, FormEventOnClose, FormEventOnActivate, FormEventOnCreate,
	} {
		if !uncalled[event] {
			t.Errorf("событие %q числится вызываемым, хотя диспетчера у него не было", event)
		}
		if !IsUncalledFormEventType(event) {
			t.Errorf("IsUncalledFormEventType(%q) = false", event)
		}
	}
}

// Вызываемое событие невызываемым не считается — иначе предупреждение придёт на
// рабочий обработчик, а это хуже молчания: конфигуратор пойдёт «чинить» живое.
func TestIsUncalledFormEventType_ЖивыеСобытияНеПомечены(t *testing.T) {
	for _, event := range append(BrowserFormEvents(), ServerFormEvents()...) {
		if IsUncalledFormEventType(event) {
			t.Errorf("IsUncalledFormEventType(%q) = true, а событие диспетчеризуется", event)
		}
		if !IsDispatchedFormEventType(event) {
			t.Errorf("IsDispatchedFormEventType(%q) = false", event)
		}
	}
	// Имя вне словаря не событие вовсе: ни вызываемое, ни невызываемое.
	if IsUncalledFormEventType(FormEventType("ПриПолнолунии")) {
		t.Error("выдуманное имя помечено как невызываемое событие — предупреждение уводит от опечатки")
	}
}

// Порядок перечня устойчив: его печатают и сообщения проверки, и руководство.
func TestUncalledFormEvents_ПорядокУстойчив(t *testing.T) {
	first := UncalledFormEvents()
	if len(first) == 0 {
		t.Fatal("перечень невызываемых пуст — сторожить нечего")
	}
	for i := 0; i < 5; i++ {
		next := UncalledFormEvents()
		if len(next) != len(first) {
			t.Fatalf("длина перечня плавает: %d против %d", len(next), len(first))
		}
		for j := range first {
			if next[j] != first[j] {
				t.Fatalf("порядок плавает: на месте %d то %q, то %q", j, first[j], next[j])
			}
		}
	}
}
