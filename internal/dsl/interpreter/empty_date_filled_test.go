package interpreter_test

import (
	"testing"
	"time"
)

// Пустая дата не заполнена — как в 1С. Пока она считалась заполненной, типовая
// подстановка «нет даты — берём текущую» молча не срабатывала: срез версионного
// регистра уходил на 01.01.0001, правил там нет, и проверка, которая должна была
// заблокировать запись, просто не находилась. Ошибки при этом не было.
func TestEmptyDateIsNotFilled(t *testing.T) {
	localZone := time.FixedZone("UTC+3", 3*60*60)
	tests := []struct {
		name  string
		value time.Time
		want  string
	}{
		{name: "zero", value: time.Time{}, want: "false/true"},
		{name: "database UTC", value: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), want: "false/true"},
		{name: "local zone", value: time.Date(1, 1, 1, 0, 0, 0, 0, localZone), want: "false/true"},
		{name: "time on first day", value: time.Date(1, 1, 1, 12, 0, 0, 0, time.UTC), want: "true/false"},
		{name: "nanosecond on first day", value: time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC), want: "true/false"},
		{name: "real date", value: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), want: "true/false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evalWithVars(t, `Функция Тест()
  Возврат Строка(ЗначениеЗаполнено(ДатаПроверки)) + "/" + Строка(Пустая(ДатаПроверки));
КонецФункции`, map[string]any{"ДатаПроверки": tt.value})
			if got != tt.want {
				t.Errorf("ЗначениеЗаполнено/Пустая(%v) = %v, ожидалось %s", tt.value, got, tt.want)
			}
		})
	}
}
