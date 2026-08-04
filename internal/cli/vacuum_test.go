package cli

import "testing"

// Приставки берутся из списка, а не индексом по строке: кириллическая буква в
// UTF-8 занимает два байта, и индекс по строке давал «Ð» вместо «М».
func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                      "0 Б",
		512:                    "512 Б",
		1024:                   "1.0 КБ",
		1536:                   "1.5 КБ",
		1024 * 1024:            "1.0 МБ",
		3 * 1024 * 1024:        "3.0 МБ",
		2 * 1024 * 1024 * 1024: "2.0 ГБ",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, ожидалось %q", in, got, want)
		}
	}
}
