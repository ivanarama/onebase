package interpreter

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// Приостановить(Секунды) — выдержка времени для backoff (issue #708).
//
// Без неё политика повторов декоративна: счётчик попыток растёт, а интервал
// между ними нулевой — все попытки уходят в перегруженный сервис мгновенно,
// ровно тогда, когда он просил подождать.

func TestПриостановить_ДействительноЖдёт(t *testing.T) {
	fn := newSleepBuiltin()
	start := time.Now()
	if _, err := fn([]any{0.15}, "t.os", 1); err != nil {
		t.Fatalf("вызов: %v", err)
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("прошло %v — выдержки не было", el)
	}
}

// Предел на один вызов обязателен: выдержка держит поток целиком, и опечатка в
// единицах (взяли миллисекунды) подвесила бы воркер регламентного задания.
func TestПриостановить_ПределИОтрицательные(t *testing.T) {
	fn := newSleepBuiltin()
	for _, c := range []struct {
		name string
		arg  any
	}{
		{"больше предела", float64(maxSleepSeconds + 1)},
		{"отрицательная", -1.0},
		{"NaN", math.NaN()},
		{"плюс бесконечность", math.Inf(1)},
		{"минус бесконечность", math.Inf(-1)},
		{"переполнение duration", math.MaxFloat64},
		{"числовая строка", "0.001"},
		{"булево", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			if !raisesUserError(func() { _, _ = fn([]any{c.arg}, "t.os", 1) }) {
				t.Fatal("принято без ошибки")
			}
			if el := time.Since(start); el > time.Second {
				t.Fatalf("отказ занял %v — значит всё-таки ждали", el)
			}
		})
	}
	if !raisesUserError(func() { _, _ = fn(nil, "t.os", 1) }) {
		t.Error("вызов без аргумента принят")
	}
}

func TestПриостановить_DecimalСХвостовымиНулямиСохраняетЗначение(t *testing.T) {
	d, err := decimal.NewFromString("0.100000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if got := sleepDuration([]any{d}); got != 100*time.Millisecond {
		t.Fatalf("длительность = %v, ожидалось %v", got, 100*time.Millisecond)
	}
}

// raisesUserError сообщает, подняла ли функция пользовательскую ошибку DSL.
// Отказ здесь идёт паникой (RaiseUserError), как у всех объектов DSL, — её
// ловит Попытка в модуле и обёртки Run/Call снаружи.
func raisesUserError(fn func()) (raised bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(userError); ok {
				raised = true
				return
			}
			panic(r)
		}
	}()
	fn()
	return false
}

// Под замороженными часами выдержка двигает время, а не тратит его: backoff
// проверяется headless и мгновенно.
func TestПриостановить_ПодЗамороженнымиЧасамиДвигаетВремя(t *testing.T) {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.Local)
	clock := &TestClock{frozen: &base}
	fn := newFrozenClockSleepBuiltin(clock, nil)

	start := time.Now()
	if _, err := fn([]any{30.0}, "t.os", 1); err != nil {
		t.Fatalf("вызов: %v", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("реально ждали %v — замороженные часы не учтены", el)
	}
	if got := clock.now(); !got.Equal(base.Add(30 * time.Second)) {
		t.Fatalf("время %v, ждали %v", got, base.Add(30*time.Second))
	}
}

// Незамороженные часы означают обычный прогон — там выдержка настоящая, иначе
// тест-профиль незаметно отключил бы backoff в бою.
func TestПриостановить_БезЗаморозкиЖдётПоНастоящему(t *testing.T) {
	fn := newFrozenClockSleepBuiltin(&TestClock{}, nil)
	start := time.Now()
	if _, err := fn([]any{0.15}, "t.os", 1); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("прошло %v — выдержки не было", el)
	}
}
