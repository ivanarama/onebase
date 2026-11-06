package expreval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestFormulaFormatRejectsNumericTextBeforeDecimalExpansion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		profile interpreter.SandboxProfile
		wantErr string
	}{
		{"default exponent", "1e-5000", DefaultProfile(), "число вне безопасного диапазона"},
		{"decimal only", "1e-65", interpreter.SandboxProfile{MaxDecimalExpansion: 64}, "число вне безопасного диапазона"},
		{"string only", "1e-65", interpreter.SandboxProfile{MaxStringExpansion: 64}, "число вне безопасного диапазона"},
		{"stricter string bound", "1e-65", interpreter.SandboxProfile{MaxDecimalExpansion: 4096, MaxStringExpansion: 64}, "число вне безопасного диапазона"},
		{"coefficient", strings.Repeat("1", 5000) + "e-5000", interpreter.SandboxProfile{MaxDecimalExpansion: 10000}, "число вне безопасного диапазона"},
		{"numeric text length", strings.Repeat("0", 8193), interpreter.SandboxProfile{MaxDecimalExpansion: 4096}, "numeric text"},
	} {
		for _, name := range []string{"Формат", "Format"} {
			for _, format := range []string{"ЧГ=0", "ЧГ=0;ЧДЦ=2"} {
				t.Run(tc.name+"/"+name+"/"+format, func(t *testing.T) {
					ev := New(interpreter.New(), tc.profile)
					expr := fmt.Sprintf(`СтрДлина(%s(Text, %q))`, name, format)
					_, _, err := ev.EvalNum(expr, compose.Row{"Text": tc.text})
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("expected pre-expansion error %q, got %v", tc.wantErr, err)
					}
				})
			}
		}
	}
}

func TestFormulaFormatPreservesSafeNumericText(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	for _, expr := range []string{
		`Формат("21.50", "ЧГ=0") = "21.5"`,
		`Format("1.005", "ЧГ=0;ЧДЦ=2") = "1.01"`,
		`Формат("-1.005", "ЧГ=0;ЧДЦ=2") = "-1.01"`,
		`Формат("текст", "") = "текст"`,
		`СтрДлина(Формат("1e-4096", "ЧГ=0")) = 4098`,
		`Формат("1e-4096", "ЧГ=0;ЧДЦ=2") = "0.00"`,
	} {
		t.Run(expr, func(t *testing.T) {
			got, err := ev.EvalBool(expr, compose.Row{})
			if err != nil || !got {
				t.Fatalf("safe format changed: got=%v err=%v", got, err)
			}
		})
	}
}
