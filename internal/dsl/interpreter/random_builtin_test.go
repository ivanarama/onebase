package interpreter

import (
	"strings"
	"testing"
)

func TestRandomNumberInclusiveRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		got := callB(t, "случайноечисло", float64(1), float64(3)).(float64)
		if got < 1 || got > 3 || got != float64(int(got)) {
			t.Fatalf("СлучайноеЧисло(1, 3): получено %v", got)
		}
	}
}

func TestRandomNumberAlias(t *testing.T) {
	got := callB(t, "randomnumber", float64(-2), float64(-2))
	if got != float64(-2) {
		t.Fatalf("RandomNumber(-2, -2): получено %v", got)
	}
}

func TestRandomNumberRejectsInvalidRange(t *testing.T) {
	tests := []struct {
		name string
		args []any
	}{
		{name: "missing argument", args: []any{float64(1)}},
		{name: "fractional boundary", args: []any{float64(1.5), float64(3)}},
		{name: "reversed range", args: []any{float64(3), float64(1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := builtins["случайноечисло"](test.args, "", 0)
			if err == nil || !strings.Contains(err.Error(), "СлучайноеЧисло") {
				t.Fatalf("ожидалась ошибка СлучайноеЧисло, получено %v", err)
			}
		})
	}
}
