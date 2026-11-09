package metadata

import "testing"

// Маска ничем себя не выдаёт в пустом поле: подсказка показывает и формат,
// и то, какие литералы подставятся сами.
func TestInputMaskHint(t *testing.T) {
	cases := map[string]string{
		"(000)000-00-00": "(___)___-__-__",
		"Пл0000000":      "Пл_______",
		"00.00.00":       "__.__.__",
		"XX-***":         "__-___",
		"":               "",
	}
	for mask, want := range cases {
		if got := InputMaskHint(mask); got != want {
			t.Errorf("InputMaskHint(%q) = %q, ожидалось %q", mask, got, want)
		}
	}
}
