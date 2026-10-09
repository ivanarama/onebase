package interpreter_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

func TestLevenshteinDistanceDSL(t *testing.T) {
	tests := []struct {
		name, a, b string
		want       float64
	}{
		{"empty", "", "", 0},
		{"equal", "кот", "кот", 0},
		{"empty Unicode", "", "ёж🙂", 3},
		{"insert", "кот", "крот", 1},
		{"delete", "крот", "кот", 1},
		{"replace", "кот", "кит", 1},
		{"mixed edits", "kitten", "sitting", 3},
		{"case sensitive", "А", "а", 1},
		{"no normalization", "ё", "е\u0308", 2},
		{"combining code points", "", "е\u0308", 2},
		{"emoji code points", "🙂", "🙃", 1},
		{"transposition costs two", "ab", "ba", 2},
		{"unequal lengths", "а", "абвгдеж", 6},
		{"work boundary Unicode", strings.Repeat("я", 1000), strings.Repeat("ё", 1000), 1000},
		{"input boundary empty", "", strings.Repeat("a", 4<<20), 4 << 20},
	}
	for _, name := range []string{"РасстояниеЛевенштейна", "LevenshteinDistance", "LEVENSHTEINDISTANCE"} {
		proc := parseProc(t, fmt.Sprintf("Функция Тест()\nВозврат %s(A, B);\nКонецФункции", name))
		for _, tc := range tests {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				// Check symmetry through the same public DSL entry, including the
				// branch that puts the shorter input in the DP columns.
				for _, inputs := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
					var got any
					err := interpreter.New().RunWithResult(proc, nil, &got, map[string]any{"A": inputs[0], "B": inputs[1]})
					if err != nil || got != tc.want {
						t.Fatalf("result=%v, err=%v; want %v", got, err, tc.want)
					}
				}
			})
		}
	}
}

func TestLevenshteinDistanceDSLErrors(t *testing.T) {
	tests := []struct {
		name, args, message string
		vars                map[string]any
	}{
		{"missing", "", "ожидаются две строки", nil},
		{"one argument", `"a"`, "ожидаются две строки", nil},
		{"extra argument", `"a", "b", "c"`, "ожидаются две строки", nil},
		{"number", `1, "b"`, "должны быть строками", nil},
		{"undefined", `"a", Неопределено`, "должны быть строками", nil},
		{"work exceeded", "A, B", "предел вычислений 1000000", map[string]any{"A": strings.Repeat("a", 1001), "B": strings.Repeat("b", 1000)}},
		{"left input exceeded", `A, ""`, "4194304 байт", map[string]any{"A": strings.Repeat("a", (4<<20)+1)}},
		{"right input exceeded", `"", A`, "4194304 байт", map[string]any{"A": strings.Repeat("a", (4<<20)+1)}},
	}
	for _, name := range []string{"РасстояниеЛевенштейна", "LevenshteinDistance"} {
		for _, tc := range tests {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				// The budget/type error must be catchable by application code.
				proc := parseProc(t, fmt.Sprintf(`Функция Тест()
Попытка
  Возврат %s(%s);
Исключение
  Возврат ИнформацияОбОшибке().Описание;
КонецПопытки;
КонецФункции`, name, tc.args))
				var got any
				err := interpreter.New().RunWithResult(proc, nil, &got, tc.vars)
				message, ok := got.(string)
				if err != nil || !ok || !strings.Contains(message, tc.message) {
					t.Fatalf("result=%v, err=%v; want caught error containing %q", got, err, tc.message)
				}
			})
		}
	}
}

func TestLevenshteinDistanceSandboxStringLimit(t *testing.T) {
	for _, name := range []string{"РасстояниеЛевенштейна", "LevenshteinDistance"} {
		proc := parseProc(t, fmt.Sprintf("Функция Тест()\nВозврат %s(A, B);\nКонецФункции", name))
		profile := interpreter.SandboxProfile{MaxStringExpansion: 64}
		var got any
		err := interpreter.New().RunSandboxed(proc, nil, profile, &got, map[string]any{"A": "кот", "B": "кит"})
		if err != nil || got != float64(1) {
			t.Fatalf("%s: bounded sandbox call result=%v err=%v", name, got, err)
		}
		err = interpreter.New().RunSandboxed(proc, nil, profile, &got, map[string]any{"A": strings.Repeat("a", 65), "B": ""})
		if err == nil || !strings.Contains(err.Error(), "sandbox string limit") {
			t.Fatalf("%s: sandbox string budget bypassed: %v", name, err)
		}
	}
}
