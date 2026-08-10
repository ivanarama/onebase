package interpreter

import (
	"fmt"
	"testing"
	"time"
)

// Сдвиг даты по часам, минутам и секундам (issue #707).
//
// Их не было, а очевидные обходные пути молчали: ДобавитьДень(Д, 60/86400)
// отбрасывал дробную часть и давал нулевой сдвиг, а Число(Д) возвращал 0.
// Работало только `Д + N`, и находилось перебором.

var shiftBase = time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)

func TestДобавить_ЧасМинутаСекунда(t *testing.T) {
	cases := []struct {
		name string
		fn   func([]any, string, int) (any, error)
		n    float64
		want time.Time
	}{
		{"секунды", addSecondsBuiltin, 90, shiftBase.Add(90 * time.Second)},
		{"минуты", addMinutesBuiltin, 5, shiftBase.Add(5 * time.Minute)},
		{"часы", addHoursBuiltin, 3, shiftBase.Add(3 * time.Hour)},
		{"назад", addHoursBuiltin, -3, shiftBase.Add(-3 * time.Hour)},
		// Доли принимаются: в DSL число одно и оно дробное, а молчаливое
		// отбрасывание дроби — ровно та беда, из-за которой issue и заведён.
		{"доли секунды", addSecondsBuiltin, 0.5, shiftBase.Add(500 * time.Millisecond)},
		{"полминуты", addMinutesBuiltin, 0.5, shiftBase.Add(30 * time.Second)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.fn([]any{shiftBase, c.n}, "t.os", 1)
			if err != nil {
				t.Fatalf("вызов: %v", err)
			}
			ts, ok := got.(time.Time)
			if !ok {
				t.Fatalf("вернулось %T, ждали дату", got)
			}
			if !ts.Equal(c.want) {
				t.Fatalf("получено %v, ждали %v", ts, c.want)
			}
		})
	}
}

// Переход через границу суток обязан переносить дату, а не упираться в 23:59.
func TestДобавить_ПереходЧерезСутки(t *testing.T) {
	late := time.Date(2026, 8, 10, 23, 30, 0, 0, time.Local)
	got, err := addHoursBuiltin([]any{late, 2.0}, "t.os", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 11, 1, 30, 0, 0, time.Local)
	if ts, _ := got.(time.Time); !ts.Equal(want) {
		t.Fatalf("получено %v, ждали %v", got, want)
	}
}

// Эквивалентность `Д + N` и ДобавитьСекунд(Д, N) — то, что обещает справка.
func TestДобавить_СекундыЭквивалентныСложению(t *testing.T) {
	got, err := addSecondsBuiltin([]any{shiftBase, 30.0}, "t.os", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ts, _ := got.(time.Time); !ts.Equal(shiftBase.Add(30 * time.Second)) {
		t.Fatalf("ДобавитьСекунд(Д,30) = %v, а Д+30 = %v", got, shiftBase.Add(30*time.Second))
	}
}

// Число(Дата) даёт ГГГГММДДЧЧММСС и разбирается обратно конструктором.
// Раньше дата уходила в разбор строки «2026-05-11 00:00:00 +0300 MSK», не
// парсилась и молча давала 0 — это читалось как «дата пустая», и арифметику
// времени начинали строить на нуле (issue #707).
func TestЧисло_ДатаRoundTrip(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Число(Дата(2026, 5, 11, 10, 30, 45));
КонецФункции`)
	if got := fmt.Sprintf("%v", r); got != "20260511103045" {
		t.Fatalf("Число(Дата) = %v, ожидалось 20260511103045", got)
	}
	back := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(Число(Дата(2026, 5, 11, 10, 30, 45)));
КонецФункции`)
	d, ok := back.(time.Time)
	if !ok {
		t.Fatalf("Дата(Число(Дата)) вернула %T", back)
	}
	if d.Year() != 2026 || d.Month() != 5 || d.Day() != 11 || d.Hour() != 10 || d.Minute() != 30 || d.Second() != 45 {
		t.Errorf("round-trip потерял значение: %v", d)
	}
}

// Пустая дата остаётся нулём — Число() не должно выдавать 00010101000000.
func TestЧисло_ПустаяДатаОстаётсяНулём(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Число(Дата("не дата"));
КонецФункции`)
	if got := fmt.Sprintf("%v", r); got != "0" {
		t.Errorf("Число(пустая дата) = %v, ожидался 0", got)
	}
}

// Формы единственного числа работают наравне с множественными: соседние
// календарные функции единственного числа, и промах по форме давал бы
// `unknown function` на ровном месте.
func TestДобавить_ФормыЕдинственногоЧисла(t *testing.T) {
	cases := map[string]string{
		"ДобавитьСекунду(Дата(2026, 5, 11, 10, 0, 0), 90)": "10:1:30",
		"ДобавитьМинуту(Дата(2026, 5, 11, 10, 0, 0), 90)":  "11:30:0",
		"ДобавитьЧасов(Дата(2026, 5, 11, 10, 0, 0), 1.5)":  "11:30:0",
	}
	for expr, want := range cases {
		r := evalBreakFunc(t, "Функция Тест()\n  Возврат "+expr+";\nКонецФункции")
		d, ok := r.(time.Time)
		if !ok {
			t.Fatalf("%s вернул %T", expr, r)
		}
		got := fmt.Sprintf("%d:%d:%d", d.Hour(), d.Minute(), d.Second())
		if got != want {
			t.Errorf("%s = %s, ожидалось %s", expr, got, want)
		}
	}
}

// Конструктор нормализует компоненты сверх диапазона — вопреки тому, что было
// написано в DEVELOPER.md. Пустую дату дают другие входы, и тест фиксирует
// границу, чтобы документация снова не разошлась с кодом.
func TestДата_ПереполнениеНормализуется(t *testing.T) {
	r := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(2026, 5, 11, 10, 30, 90);
КонецФункции`)
	d, ok := r.(time.Time)
	if !ok || d.IsZero() {
		t.Fatalf("Дата(...,90) = %v — ожидалась нормализация, а не пустая дата", r)
	}
	if d.Minute() != 31 || d.Second() != 30 {
		t.Errorf("Дата(2026,5,11,10,30,90) = %v, ожидалось 10:31:30", d)
	}
	empty := evalBreakFunc(t, `Функция Тест()
  Возврат Дата("вчера");
КонецФункции`)
	if e, ok := empty.(time.Time); !ok || !e.IsZero() {
		t.Errorf("Дата(\"вчера\") = %v, ожидалась пустая дата", empty)
	}
}
