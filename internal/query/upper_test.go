package query

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Побайтовая арифметика заменяет стандартную функцию, поэтому проверяется не
// выборка случаев, а совпадение с strings.ToUpper/ToLower на всём диапазоне,
// который заявлен поддержанным, и корректный откат на всём остальном.

func TestUpperLowerFast_СовпадаетСоСтандартной(t *testing.T) {
	cases := []string{
		"", "a", "Z", "ВЫБРАТЬ", "выбрать", "ВыБрАтЬ",
		"СтатистикаОбменов.КоличествоОбъектов",
		"ёжик", "ЁЖИК", "Ёлка", "разъезд", "ЙОД", "щи",
		"Товар_2", "table-name", "СУММА(Количество)",
		// За пределами быстрого пути — обязан сработать откат.
		"Straße", "ÄÖÜ", "ĉapelo", "ΑΒΓ", "İstanbul", "ѪѫѬ",
		// Смешанное и битый UTF-8.
		"Заказ №7", "цена $5", "\xd0", "\xd1", "a\xd0\x00b", "\xff\xfe",
	}
	for _, s := range cases {
		if got, want := upperFast(s), strings.ToUpper(s); got != want {
			t.Errorf("upperFast(%q) = %q, ожидалось %q", s, got, want)
		}
		if got, want := lowerFast(s), strings.ToLower(s); got != want {
			t.Errorf("lowerFast(%q) = %q, ожидалось %q", s, got, want)
		}
	}
}

// Кириллический блок целиком: U+0400..U+04FF. Часть его (архаичные буквы за
// U+045F) быстрый путь не покрывает и обязан отдавать строку в стандартную
// функцию — результат всё равно должен совпасть.
func TestUpperLowerFast_ВесьКириллическийБлок(t *testing.T) {
	for r := rune(0x0400); r <= 0x04FF; r++ {
		s := string(r)
		if got, want := upperFast(s), strings.ToUpper(s); got != want {
			t.Fatalf("upperFast(%q U+%04X) = %q, ожидалось %q", s, r, got, want)
		}
		if got, want := lowerFast(s), strings.ToLower(s); got != want {
			t.Fatalf("lowerFast(%q U+%04X) = %q, ожидалось %q", s, r, got, want)
		}
	}
}

func TestUpperLowerFast_ВесьASCII(t *testing.T) {
	for b := 0; b < 0x80; b++ {
		s := string(rune(b))
		if got, want := upperFast(s), strings.ToUpper(s); got != want {
			t.Fatalf("upperFast(%q) = %q, ожидалось %q", s, got, want)
		}
		if got, want := lowerFast(s), strings.ToLower(s); got != want {
			t.Fatalf("lowerFast(%q) = %q, ожидалось %q", s, got, want)
		}
	}
}

// Длина в байтах в быстром пути не меняется — на этом держится отсечение по
// длине в upperLookup. Если инвариант сломается, ключевые слова начнут
// теряться, причём молча.
func TestUpperFast_ДлинаНеМеняется(t *testing.T) {
	for r := rune(0x0400); r <= 0x045F; r++ {
		s := string(r)
		if got, ok := appendCase(nil, s, true); ok && len(got) != len(s) {
			t.Fatalf("U+%04X: длина %d → %d", r, len(s), len(got))
		}
		if got, ok := appendCase(nil, s, false); ok && len(got) != len(s) {
			t.Fatalf("U+%04X: длина %d → %d", r, len(s), len(got))
		}
	}
}

func FuzzUpperLowerFast(f *testing.F) {
	for _, s := range []string{"ВЫБРАТЬ", "ёж", "Straße", "\xd1\x8f", "Товар"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := upperFast(s), strings.ToUpper(s); got != want {
			t.Fatalf("upperFast(%q) = %q, ожидалось %q", s, got, want)
		}
		if got, want := lowerFast(s), strings.ToLower(s); got != want {
			t.Fatalf("lowerFast(%q) = %q, ожидалось %q", s, got, want)
		}
		if !utf8.ValidString(s) {
			return
		}
		if up, ok := appendCase(nil, s, true); ok && !utf8.Valid(up) {
			t.Fatalf("upperFast сломал UTF-8 на %q", s)
		}
	})
}

// upperLookup обязан отвечать так же, как прямое обращение к карте, включая
// строки длиннее любого ключа и строки вне быстрого пути.
func TestUpperLookup_СовпадаетСПрямымПоиском(t *testing.T) {
	for _, ident := range []string{
		"выбрать", "ВЫБРАТЬ", "ВыБрАтЬ", "сумма", "COUNT", "count",
		"Номенклатура", "", "не_ключевое_слово",
		strings.Repeat("я", 200), "Straße", "СГРУППИРОВАТЬ",
	} {
		for name, m := range map[string]map[string]string{"kwMap": kwMap, "aggFuncs": aggFuncs} {
			wantValue, wantOK := m[strings.ToUpper(ident)]
			gotValue, gotOK := upperLookup(m, ident)
			if gotValue != wantValue || gotOK != wantOK {
				t.Errorf("upperLookup(%s, %q) = (%q,%v), ожидалось (%q,%v)",
					name, ident, gotValue, gotOK, wantValue, wantOK)
			}
		}
	}
}

// Поиск ключевого слова не должен ничего аллоцировать: он выполняется на каждый
// идентификатор запроса, а запрос компилируется на каждое Выполнить().
func TestUpperLookup_БезАллокаций(t *testing.T) {
	got := testing.AllocsPerRun(200, func() {
		if _, ok := sqlKW("сгруппировать"); !ok {
			t.Fatal("проба некорректна: слово должно находиться")
		}
	})
	if got != 0 {
		t.Errorf("sqlKW аллоцирует %.1f раз(а) за вызов, ожидалось 0", got)
	}
}
