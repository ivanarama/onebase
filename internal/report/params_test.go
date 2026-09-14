package report

import (
	"testing"
	"time"
)

// Разбор значения параметра отчёта раньше лежал в трёх копиях, и копии
// расходились намеренно: экран и выгрузки прощают негодное значение, API его
// отвергает. Расхождение нигде не было записано и держалось случайно —
// таблица ниже его фиксирует, чтобы следующий тип параметра не завёл четвёртую
// копию с четвёртым поведением.
func TestParseParamValue(t *testing.T) {
	local := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	}
	moment := func(y int, m time.Month, d, hh, mm, ss int) time.Time {
		return time.Date(y, m, d, hh, mm, ss, 0, time.Local)
	}
	for _, c := range []struct {
		name    string
		typ     string
		raw     string
		mode    ParamParseMode
		want    any
		wantErr bool
	}{
		{"форма: дата разбирается", "date", "2026-08-29", ParamParseForm, local(2026, time.August, 29), false},
		{"форма: datetime с секундами", "datetime", "2026-08-29T12:30:41", ParamParseForm, moment(2026, time.August, 29, 12, 30, 41), false},
		{"форма: datetime без секунд", "datetime", "2026-08-29T12:30", ParamParseForm, moment(2026, time.August, 29, 12, 30, 0), false},
		{"форма: datetime принимает одну дату", "datetime", "2026-08-29", ParamParseForm, local(2026, time.August, 29), false},
		{"форма: негодный datetime — ошибка", "datetime", "мусор", ParamParseForm, nil, true},
		{"api: datetime с секундами", "datetime", "2026-08-29T12:30:41", ParamParseAPI, moment(2026, time.August, 29, 12, 30, 41), false},
		{"api: негодный datetime — ошибка", "datetime", "2026-08-29 12:30", ParamParseAPI, nil, true},
		{"api: имя datetime нормализуется", " DateTime ", "2026-08-29T12:30:41", ParamParseAPI, moment(2026, time.August, 29, 12, 30, 41), false},
		{"форма: пустой datetime — nil", "datetime", "", ParamParseForm, nil, false},
		{"форма: негодная дата — ошибка вызывающему", "date", "мусор", ParamParseForm, nil, true},
		{"форма: имя типа сравнивается строго", "DATE", "2026-08-29", ParamParseForm, "2026-08-29", false},
		{"форма: bool — узкий набор", "bool", "on", ParamParseForm, true, false},
		{"форма: bool — yes не считается", "bool", "yes", ParamParseForm, false, false},
		{"форма: boolean не является bool", "boolean", "true", ParamParseForm, "true", false},
		{"форма: число остаётся строкой", "number", "12.5", ParamParseForm, "12.5", false},
		{"форма: select — строка", "select", "Первый", ParamParseForm, "Первый", false},
		{"форма: ссылка — строка", "reference:Клиент", "u-1", ParamParseForm, "u-1", false},
		{"форма: неизвестный тип — строка", "звездолёт", "x", ParamParseForm, "x", false},

		{"api: дата разбирается", "date", "2026-08-29", ParamParseAPI, local(2026, time.August, 29), false},
		{"api: негодная дата — ошибка", "date", "мусор", ParamParseAPI, nil, true},
		{"api: имя типа нормализуется", " Date ", "2026-08-29", ParamParseAPI, local(2026, time.August, 29), false},
		{"api: bool — широкий набор", "bool", "yes", ParamParseAPI, true, false},
		{"api: boolean — тот же набор", "boolean", "истина", ParamParseAPI, true, false},
		{"api: bool — прочее ложно", "bool", "мусор", ParamParseAPI, false, false},
		{"api: число разбирается", "number", "12.5", ParamParseAPI, 12.5, false},
		{"api: негодное число — ошибка", "number", "12,5", ParamParseAPI, nil, true},
		{"api: select — строка", "select", "Первый", ParamParseAPI, "Первый", false},
		{"api: неизвестный тип — строка", "звездолёт", "x", ParamParseAPI, "x", false},

		// Пустое значение решается ДО нормализации имени типа — в обоих режимах.
		{"форма: пустой точный bool — Ложь", "bool", "", ParamParseForm, false, false},
		{"api: пустой точный bool — Ложь", "bool", "", ParamParseAPI, false, false},
		{"api: пустой boolean — nil", "boolean", "", ParamParseAPI, nil, false},
		{"api: пустой BOOL — nil", "BOOL", "", ParamParseAPI, nil, false},
		{"форма: пустая дата — nil", "date", "", ParamParseForm, nil, false},
		{"api: пустая дата — nil", "date", "", ParamParseAPI, nil, false},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseParamValue(c.raw, Param{Name: "П", Type: c.typ}, c.mode)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ожидалась ошибка, получено %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if wt, ok := c.want.(time.Time); ok {
				gt, ok := got.(time.Time)
				if !ok || !gt.Equal(wt) {
					t.Fatalf("получено %#v, ожидалось %s", got, wt)
				}
				return
			}
			if got != c.want {
				t.Fatalf("получено %#v (%T), ожидалось %#v (%T)", got, got, c.want, c.want)
			}
		})
	}
}
