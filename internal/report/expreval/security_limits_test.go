package expreval

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/compose"
)

type formulaLimitStringer struct{ touched *atomic.Bool }

func (v formulaLimitStringer) String() string {
	v.touched.Store(true)
	return "external"
}

type formulaLimitThis struct{ touched *atomic.Bool }

func (v *formulaLimitThis) Get(string) any {
	v.touched.Store(true)
	return "external"
}
func (v *formulaLimitThis) Set(string, any) { v.touched.Store(true) }

type formulaLimitStringers []formulaLimitStringer

func TestFormulaRowBindingsRejectHostValuesWithoutCallingThem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value func(*atomic.Bool) any
	}{
		{"stringer", func(hit *atomic.Bool) any { return formulaLimitStringer{touched: hit} }},
		{"this", func(hit *atomic.Bool) any { return &formulaLimitThis{touched: hit} }},
		{"typed slice", func(hit *atomic.Bool) any {
			return formulaLimitStringers{{touched: hit}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var touched atomic.Bool
			ev := New(interpreter.New(), DefaultProfile())
			if _, err := ev.EvalBool(`Value = Undefined`, compose.Row{"Value": tc.value(&touched)}); err == nil {
				t.Fatal("host value was accepted as passive report data")
			}
			if touched.Load() {
				t.Fatal("row validation invoked a host callback")
			}
		})
	}
}

func TestFormulaRowBindingsAcceptAndEvaluatePassiveScalars(t *testing.T) {
	row := compose.Row{
		"Nil": nil, "Bool": true, "Time": time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
		"Bytes": []byte("12"), "Decimal": decimal.RequireFromString("12.5"),
		"I": int(10), "I8": int8(10), "I16": int16(10), "I32": int32(10), "I64": int64(10),
		"U": uint(10), "U8": uint8(10), "U16": uint16(10), "U32": uint32(10), "U64": uint64(10),
		"F32": float32(10.5), "F64": float64(10.5), "Text": "ok",
	}
	if err := validateRowBindings(row); err != nil {
		t.Fatalf("passive row rejected: %v", err)
	}
	ev := New(interpreter.New(), DefaultProfile())
	for _, field := range []string{"I", "I8", "I16", "I32", "I64", "U", "U8", "U16", "U32", "U64", "F32", "F64"} {
		got, err := ev.EvalBool(field+` > 2`, row)
		if err != nil || !got {
			t.Errorf("%s did not retain numeric semantics: got=%v err=%v", field, got, err)
		}
	}
}

func TestFormulaDecimalOperatorsRejectUnsafeOperandsAndResults(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	row := compose.Row{"Text": "1e10001"}
	for _, expr := range []string{
		`Text + 0 > 0`, `Text - 0 > 0`, `Text * 1 > 0`,
		`Text / 1 > 0`, `Text % 2 > 0`, `Text > 0`, `-Text = 0`,
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := ev.EvalBool(expr, row); err == nil {
				t.Fatalf("unsafe numeric text reached operator %q", expr)
			}
		})
	}

	if _, err := ev.EvalBool(`A * B > 0`, compose.Row{
		"A": decimal.New(1, 4096),
		"B": decimal.New(1, 4096),
	}); err == nil {
		t.Fatal("operator result beyond the exponent bound was accepted")
	}

	got, err := ev.EvalBool(`Text + 0 > 0`, compose.Row{"Text": "1e4096"})
	if err != nil || !got {
		t.Fatalf("decimal boundary stopped working: got=%v err=%v", got, err)
	}
}

func TestFormulaDecimalBuiltinsRejectNumericTextBeforeExpansion(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	row := compose.Row{"Text": "1e10001"}
	for _, expr := range []string{
		`Round(Text, 0) = 0`,
		`Int(Text) = 0`,
		`Max(Text, 1) = 1`,
		`AmountInWords(Text, "rub") <> ""`,
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := ev.EvalBool(expr, row); err == nil {
				t.Fatalf("unsafe numeric text reached builtin %q", expr)
			}
		})
	}
}

func TestFormulaStringExpansionIsPreflighted(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	cases := []struct {
		name string
		expr string
		row  compose.Row
	}{
		{
			name: "binary concat",
			expr: `A + B <> ""`,
			row:  compose.Row{"A": strings.Repeat("a", 600<<10), "B": strings.Repeat("b", 600<<10)},
		},
		{
			name: "replace",
			expr: `StrReplace(Input, "a", Replacement) <> ""`,
			row:  compose.Row{"Input": strings.Repeat("a", 2048), "Replacement": strings.Repeat("b", 1024)},
		},
		{
			name: "cascading template",
			expr: `StrTemplate(Template, Arg1, Arg2) <> ""`,
			row: compose.Row{
				"Template": strings.Repeat("%2", 1024),
				"Arg1":     strings.Repeat("x", 2048),
				"Arg2":     "%1",
			},
		},
		{
			name: "split cardinality",
			expr: `StrSplit(Input, "a") <> Undefined`,
			row:  compose.Row{"Input": strings.Repeat("a", 200_000)},
		},
	}
	parts := make([]string, 120)
	for index := range parts {
		parts[index] = "Part"
	}
	cases = append(cases, struct {
		name string
		expr string
		row  compose.Row
	}{
		name: "join separators",
		expr: `StrJoin([` + strings.Join(parts, ",") + `], Separator) <> ""`,
		row:  compose.Row{"Part": "", "Separator": strings.Repeat("-", 10<<10)},
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ev.EvalBool(tc.expr, tc.row); err == nil {
				t.Fatalf("expanding formula was accepted: %s", tc.expr)
			}
		})
	}

	got, err := ev.EvalBool(`StrReplace("a-b", "-", "/") = "a/b"`, compose.Row{})
	if err != nil || !got {
		t.Fatalf("small string operation failed: got=%v err=%v", got, err)
	}
}

func TestFormulaSourcePrechecksRunBeforeRecursiveParserAndCacheFailures(t *testing.T) {
	ev := New(interpreter.New(), DefaultProfile())
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"bytes", strings.Repeat(" ", maxFormulaSourceBytes+1) + `1 = 1`, "byte"},
		{"tokens", strings.Repeat("- ", maxFormulaSourceTokens+1) + `1 = 1`, "token"},
		{"depth", strings.Repeat("(", maxFormulaSyntaxDepth+1) + `1 = 1` + strings.Repeat(")", maxFormulaSyntaxDepth+1), "depth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for attempt := 0; attempt < 2; attempt++ {
				_, err := ev.EvalBool(tc.expr, compose.Row{})
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
					t.Fatalf("attempt %d: expected %q precheck error, got %v", attempt, tc.want, err)
				}
			}
			if _, ok := ev.compileErrors[tc.expr]; !ok {
				t.Fatal("immutable compile failure was not cached")
			}
		})
	}

	boundary := strings.Repeat("(", maxFormulaSyntaxDepth) + `1 = 1` + strings.Repeat(")", maxFormulaSyntaxDepth)
	got, err := ev.EvalBool(boundary, compose.Row{})
	if err != nil || !got {
		t.Fatalf("syntax-depth boundary failed: got=%v err=%v", got, err)
	}
}

func TestEvalNumDoesNotExportUnsafeDecimalText(t *testing.T) {
	d, ok, err := New(interpreter.New(), DefaultProfile()).EvalNum(`Text`, compose.Row{"Text": "1e10001"})
	if err != nil {
		t.Fatalf("unsafe value should become undefined without expansion: %v", err)
	}
	if ok {
		t.Fatalf("unsafe decimal escaped evaluator: %s", d)
	}
}
