package interpreter

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
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

func TestДатаАрифметика_СохраняетДолиСекунды(t *testing.T) {
	cases := map[string]time.Time{
		`Дата(2026, 8, 10, 12, 0, 0) + 0.5`: shiftBase.Add(500 * time.Millisecond),
		`0.5 + Дата(2026, 8, 10, 12, 0, 0)`: shiftBase.Add(500 * time.Millisecond),
		`Дата(2026, 8, 10, 12, 0, 0) - 0.5`: shiftBase.Add(-500 * time.Millisecond),
	}
	for expr, want := range cases {
		t.Run(expr, func(t *testing.T) {
			got := evalBreakFunc(t, "Функция Тест()\n  Возврат "+expr+";\nКонецФункции")
			date, ok := got.(time.Time)
			if !ok || !date.Equal(want) {
				t.Fatalf("%s = %T(%v), ожидалось %v", expr, got, got, want)
			}
		})
	}
}

// Число(Дата) даёт ГГГГММДДЧЧММСС и разбирается обратно конструктором по
// календарным компонентам с точностью до секунды.
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

func TestДата_ЦелыйDecimalПослеДеленияНеЗависитОтExponent(t *testing.T) {
	cases := map[string]time.Time{
		`Дата(20260511 / 1)`: time.Date(2026, 5, 11, 0, 0, 0, 0, time.Local),
		`Дата(Число(Дата(2026, 5, 11, 10, 30, 45)) / 1)`: time.Date(2026, 5, 11, 10, 30, 45, 0, time.Local),
	}
	for expr, want := range cases {
		t.Run(expr, func(t *testing.T) {
			got := evalBreakFunc(t, "Функция Тест()\n  Возврат "+expr+";\nКонецФункции")
			date, ok := got.(time.Time)
			if !ok || !date.Equal(want) {
				t.Fatalf("%s = %T(%v), ожидалось %v", expr, got, got, want)
			}
		})
	}
}

// Decimal удаляет начальные нули из ГГГГММДДЧЧММСС. Конструктор возвращает их
// перед разбором, иначе даты первых 999 лет не проходили заявленный round-trip.
func TestЧисло_ДатаRoundTripВосстанавливаетНулиГода(t *testing.T) {
	// Не используем 01.01.0001 00:00:00: в UTC это Go zero time, который DSL
	// намеренно считает своей пустой датой и преобразует в 0.
	for _, tc := range []struct {
		year int
		want string
	}{
		{year: 1, want: "10511103045"},
		{year: 10, want: "100511103045"},
		{year: 100, want: "1000511103045"},
	} {
		t.Run(fmt.Sprintf("year_%04d", tc.year), func(t *testing.T) {
			number := evalBreakFunc(t, fmt.Sprintf(`Функция Тест()
  Возврат Число(Дата(%d, 5, 11, 10, 30, 45));
КонецФункции`, tc.year))
			d, ok := number.(decimal.Decimal)
			if !ok || d.String() != tc.want {
				t.Fatalf("Число(Дата(%d,...)) = %T(%v), ожидалось %s", tc.year, number, number, tc.want)
			}
			back := evalBreakFunc(t, fmt.Sprintf(`Функция Тест()
  Возврат Дата(Число(Дата(%d, 5, 11, 10, 30, 45)));
КонецФункции`, tc.year))
			got, ok := back.(time.Time)
			if !ok || got.Year() != tc.year || got.Month() != time.May || got.Day() != 11 ||
				got.Hour() != 10 || got.Minute() != 30 || got.Second() != 45 {
				t.Fatalf("Дата(Число(Дата(%d,...))) = %T(%v)", tc.year, back, back)
			}
		})
	}
}

// Компактное число не кодирует зону и дробную секунду: сохраняются именно
// видимые календарные компоненты до секунд, а восстановленная зона локальная.
func TestЧисло_ДатаRoundTripИмеетТочностьДоСекунды(t *testing.T) {
	source := time.Date(2026, time.May, 11, 10, 30, 45, 987654321,
		time.FixedZone("external", 7*60*60))
	back := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(Число(Вход));
КонецФункции`, map[string]any{"Вход": source})
	got := back.(time.Time)
	if got.Year() != source.Year() || got.Month() != source.Month() || got.Day() != source.Day() ||
		got.Hour() != source.Hour() || got.Minute() != source.Minute() || got.Second() != source.Second() {
		t.Fatalf("календарные компоненты изменились: %v -> %v", source, got)
	}
	if got.Nanosecond() != 0 || got.Location() != time.Local {
		t.Fatalf("Дата(число) должна вернуть локальную точность до секунды: %v", got)
	}
}

// Нечёткие и патологически большие числа не являются компактной датой. В
// частности, extreme exponent должен отклоняться до разворачивания в строку.
func TestДата_ЧисловойВводОтклоняетДробьИНебезопасныйExponent(t *testing.T) {
	huge, err := decimal.NewFromString("1e2147483647")
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]any{
		"fraction": decimal.RequireFromString("20260511.5"),
		"huge":     huge,
		"nan":      math.NaN(),
		"infinity": math.Inf(1),
	} {
		t.Run(name, func(t *testing.T) {
			result := evalBreakFunc(t, `Функция Тест()
  Возврат Дата(Вход);
КонецФункции`, map[string]any{"Вход": input})
			got, ok := result.(time.Time)
			if !ok || !got.IsZero() {
				t.Fatalf("Дата(%v) = %T(%v), ожидалась пустая дата", input, result, result)
			}
		})
	}
}

// Тот же Decimal не должен доходить до InexactFloat64 через арифметику даты
// либо новые функции сдвига: внутри shopspring это разворачивает 10^Exponent.
func TestДата_ОперацииСдвигаОтклоняютНебезопасныеЧислаДоExpansion(t *testing.T) {
	huge := decimal.New(1, math.MaxInt32)
	tiny := decimal.New(1, math.MinInt32)
	for name, input := range map[string]any{
		"huge_decimal": huge,
		"tiny_decimal": tiny,
		"max_float":    math.MaxFloat64,
		"nan":          math.NaN(),
		"infinity":     math.Inf(1),
	} {
		t.Run(name, func(t *testing.T) {
			for _, expr := range []string{
				`Дата(2026, 5, 11) + Вход`,
				`ДобавитьСекунду(Дата(2026, 5, 11), Вход)`,
				`ДобавитьМинуту(Дата(2026, 5, 11), Вход)`,
				`ДобавитьЧас(Дата(2026, 5, 11), Вход)`,
			} {
				result := evalBreakFunc(t, `Функция Тест()
  Попытка
    Возврат `+expr+`;
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецФункции`, map[string]any{"Вход": input})
				msg, ok := result.(string)
				if !ok || !strings.Contains(msg, "безопасн") {
					t.Fatalf("%s с %s = %T(%v), ожидалась безопасная ошибка", expr, name, result, result)
				}
			}
		})
	}
}

func TestДобавить_КалендарныеСдвигиОтклоняютПереполнение(t *testing.T) {
	for _, input := range []any{
		decimal.RequireFromString("9223372036854775807"),
		decimal.RequireFromString("-9223372036854775808"),
		math.MaxFloat64,
	} {
		for _, fn := range []func([]any, string, int) (any, error){
			addDayBuiltin,
			addMonthBuiltin,
			addYearBuiltin,
		} {
			if !raisesUserError(func() { _, _ = fn([]any{shiftBase, input}, "t.os", 1) }) {
				t.Fatalf("календарный сдвиг %v принят без безопасной ошибки", input)
			}
		}
	}
}

func TestДобавить_КалендарныеСдвигиНеВозвращаютНесериализуемыйГод(t *testing.T) {
	for _, input := range []float64{float64(maxCalendarShift), -float64(maxCalendarShift)} {
		for _, fn := range []func([]any, string, int) (any, error){
			addDayBuiltin,
			addMonthBuiltin,
			addYearBuiltin,
		} {
			if !raisesUserError(func() { _, _ = fn([]any{shiftBase, input}, "t.os", 1) }) {
				t.Fatalf("сдвиг %v вернул дату за JSON-диапазоном", input)
			}
		}
	}
}

func TestДобавить_КалендарныеСдвигиПоПрежнемуУсекаютДробьКНулю(t *testing.T) {
	cases := []struct {
		name string
		fn   func([]any, string, int) (any, error)
		arg  float64
		want time.Time
	}{
		{"день плюс", addDayBuiltin, 1.9, shiftBase.AddDate(0, 0, 1)},
		{"день минус", addDayBuiltin, -1.9, shiftBase.AddDate(0, 0, -1)},
		{"месяц плюс", addMonthBuiltin, 1.9, shiftBase.AddDate(0, 1, 0)},
		{"месяц минус", addMonthBuiltin, -1.9, shiftBase.AddDate(0, -1, 0)},
		{"год плюс", addYearBuiltin, 1.9, shiftBase.AddDate(1, 0, 0)},
		{"год минус", addYearBuiltin, -1.9, shiftBase.AddDate(-1, 0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn([]any{shiftBase, tc.arg}, "t.os", 1)
			if err != nil {
				t.Fatal(err)
			}
			date, ok := got.(time.Time)
			if !ok || !date.Equal(tc.want) {
				t.Fatalf("получено %T(%v), ожидалось %v", got, got, tc.want)
			}
		})
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

// Формат ГГГГММДДЧЧММСС имеет ровно четыре цифры года. Значение за этой
// границей не должно выглядеть успешно преобразованным: Дата(такое число) уже
// не смогла бы восстановить исходную дату.
func TestЧисло_ДатаВнеЧетырёхзначногоДиапазонаОстаётсяНулём(t *testing.T) {
	for _, year := range []int{0, -1, 10000} {
		t.Run(fmt.Sprintf("year_%d", year), func(t *testing.T) {
			source := time.Date(year, time.May, 11, 10, 30, 45, 0, time.Local)
			result := evalBreakFunc(t, `Функция Тест()
  Возврат Число(Вход);
КонецФункции`, map[string]any{"Вход": source})
			got, ok := result.(decimal.Decimal)
			if !ok || !got.IsZero() {
				t.Fatalf("Число(Дата с годом %d) = %T(%v), ожидался 0", year, result, result)
			}
		})
	}
}

// Для секунд и минут приняты формы единственного числа, а для часа — также
// привычная форма ДобавитьЧасов. Промах по форме раньше давал `unknown
// function` на ровном месте.
func TestДобавить_ФормыЕдинственногоЧисла(t *testing.T) {
	cases := map[string]string{
		"ДобавитьСекунду(Дата(2026, 5, 11, 10, 0, 0), 90)": "10:1:30",
		"ДобавитьМинуту(Дата(2026, 5, 11, 10, 0, 0), 90)":  "11:30:0",
		"ДобавитьЧасов(Дата(2026, 5, 11, 10, 0, 0), 1.5)":  "11:30:0",
		"AddSecond(Дата(2026, 5, 11, 10, 0, 0), 90)":       "10:1:30",
		"AddMinute(Дата(2026, 5, 11, 10, 0, 0), 90)":       "11:30:0",
		"AddHour(Дата(2026, 5, 11, 10, 0, 0), 1.5)":        "11:30:0",
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
