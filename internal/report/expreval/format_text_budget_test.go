package expreval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/shopspring/decimal"
)

func TestFormulaFormatSeparateTextBudget(t *testing.T) {
	for _, name := range []string{"Формат", "Format"} {
		for _, spec := range []string{"", "ЧГ=0", "ЧГ=0;ЧДЦ=2"} {
			for _, tc := range []struct {
				name, text, wantErr string
				profile             interpreter.SandboxProfile
				want                int
			}{
				{"text at old bound", strings.Repeat("x", 8192), "", DefaultProfile(), 8192},
				{"text beyond old bound", strings.Repeat("x", 8193), "", DefaultProfile(), 8193},
				{"numeric prefix text", "1" + strings.Repeat("0", 8192) + "x", "", DefaultProfile(), 8194},
				{"broken exponent text", "1e" + strings.Repeat("e", 8193), "", DefaultProfile(), 8195},
				{"decimal-only profile text", strings.Repeat("x", 9000), "", interpreter.SandboxProfile{MaxDecimalExpansion: 4096}, 9000},
				{"separate text budget", strings.Repeat("x", 9000), "", interpreter.SandboxProfile{MaxDecimalExpansion: 64, MaxStringExpansion: 10000}, 9000},
				{"above text budget", strings.Repeat("x", 10001), "string limit", interpreter.SandboxProfile{MaxDecimalExpansion: 64, MaxStringExpansion: 10000}, 0},
				{"long numeric coefficient", strings.Repeat("0", 8193), "numeric text", DefaultProfile(), 0},
				{"signed long number", "-." + strings.Repeat("0", 8193), "numeric text", DefaultProfile(), 0},
				{"long exponent", "1e-" + strings.Repeat("0", 8193), "numeric text", DefaultProfile(), 0},
				{"long hex float", "0x" + strings.Repeat("0", 8193) + "p-1", "numeric text", DefaultProfile(), 0},
				{"long underscored float", "0" + strings.Repeat("_0", 4097), "numeric text", DefaultProfile(), 0},
				{"coefficient expansion", strings.Repeat("1", 5000) + "e-5000", "число вне безопасного диапазона", DefaultProfile(), 0},
				{"exponent expansion", "1e-5000", "число вне безопасного диапазона", DefaultProfile(), 0},
			} {
				t.Run(name+"/"+spec+"/"+tc.name, func(t *testing.T) {
					ev := New(interpreter.New(), tc.profile)
					got, _, err := ev.EvalNum(fmt.Sprintf("СтрДлина(%s(Text, %q))", name, spec), compose.Row{"Text": tc.text})
					if tc.wantErr != "" {
						if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
							t.Fatalf("want error %q, got %v", tc.wantErr, err)
						}
						return
					}
					if err != nil || !got.Equal(decimal.NewFromInt(int64(tc.want))) {
						t.Fatalf("text length=%v err=%v, want %d", got, err, tc.want)
					}
				})
			}
		}
		// With an empty format, input and output at the exact configured string
		// boundary remain valid; one more byte must fail independently of Decimal.
		for _, size := range []int{128, 129} {
			t.Run(fmt.Sprintf("%s/string boundary/%d", name, size), func(t *testing.T) {
				ev := New(interpreter.New(), interpreter.SandboxProfile{MaxDecimalExpansion: 64, MaxStringExpansion: 128})
				got, _, err := ev.EvalNum(fmt.Sprintf(`СтрДлина(%s(Text, ""))`, name), compose.Row{"Text": strings.Repeat("x", size)})
				if size == 128 {
					if err != nil || !got.Equal(decimal.NewFromInt(128)) {
						t.Fatalf("boundary: %v %v", got, err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "string limit") {
					t.Fatalf("above boundary: %v %v", got, err)
				}
			})
		}
	}
}
