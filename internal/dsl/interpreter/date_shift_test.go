package interpreter

import (
	"testing"
	"time"
)

// Сдвиг даты по часам, минутам и секундам (issue #707).
//
// Их не было, а очевидные обходные пути молчали: ДобавитьДень(Д, 60/86400)
// отбрасывал дробную часть и давал нулевой сдвиг, конструктор
// Дата(Г,М,Д,ч,м,с+90) не нормализует переполнение и возвращал пустую дату,
// Число(Д) отдавал 0. Работало только `Д + N`, и находилось перебором.

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
