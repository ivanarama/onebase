package interpreter_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

func TestSandboxResourceLimitsCoverOperatorsAndBuiltinFormatting(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		profile interpreter.SandboxProfile
		vars    map[string]any
	}{
		{
			name: "string compound assignment",
			source: `Function Test()
  X = A;
  X += B;
  Return X;
EndFunction`,
			profile: interpreter.SandboxProfile{MaxStringExpansion: 64},
			vars:    map[string]any{"A": strings.Repeat("a", 40), "B": strings.Repeat("b", 40)},
		},
		{
			name: "numeric compound assignment",
			source: `Function Test()
  X = Text;
  X -= 0;
  Return X;
EndFunction`,
			profile: interpreter.SandboxProfile{MaxDecimalExpansion: 4096},
			vars:    map[string]any{"Text": "1e10001"},
		},
		{
			name: "decimal-only profile checks lexical size before parsing",
			source: `Function Test()
  Return Text + 0;
EndFunction`,
			profile: interpreter.SandboxProfile{MaxDecimalExpansion: 4096},
			vars:    map[string]any{"Text": strings.Repeat("9", 9000)},
		},
		{
			name: "template validates decimal pointer before fmt",
			source: `Function Test()
  Return StrTemplate("%1", NumberValue);
EndFunction`,
			profile: interpreter.RestrictedProfile(),
			vars: func() map[string]any {
				value := decimal.New(1, 10001)
				return map[string]any{"NumberValue": &value}
			}(),
		},
		{
			name: "json validates nested decimal before marshal",
			source: `Function Test()
  Return WriteJSON(Value);
EndFunction`,
			profile: interpreter.RestrictedProfile(),
			vars: map[string]any{
				"Value": interpreter.NewStructFromMap(map[string]any{"Number": decimal.New(1, 10001)}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var result any
			err := interpreter.New().RunSandboxed(parseProc(t, tc.source), nil, tc.profile, &result, tc.vars)
			if err == nil {
				t.Fatalf("resource-expanding program succeeded: %T(%v)", result, result)
			}
		})
	}
}

func TestSandboxTemplateChecksMarkersCreatedAcrossReplacementBoundary(t *testing.T) {
	profile := interpreter.SandboxProfile{MaxStringExpansion: 1 << 20}
	proc := parseProc(t, `Function Test()
  Return StrTemplate(Template, Arg1, Arg2);
EndFunction`)
	_, err := interpreter.New().CallSandboxed(proc, nil, nil, profile, map[string]any{
		"Template": strings.Repeat("%21", 300_000),
		"Arg1":     strings.Repeat("x", 1024),
		"Arg2":     "%",
	})
	if err == nil {
		t.Fatal("template expansion formed at a replacement boundary bypassed the string limit")
	}

	result, err := interpreter.New().CallSandboxed(proc, nil, nil, profile, map[string]any{
		"Template": "%21",
		"Arg1":     "ok",
		"Arg2":     "%",
	})
	if err != nil || result != "ok" {
		t.Fatalf("bounded template changed normal largest-index-first semantics: result=%v err=%v", result, err)
	}
}

func TestSandboxLimitsDoNotChangeLexicalStringsOrNumericArithmetic(t *testing.T) {
	profile := interpreter.SandboxProfile{MaxDecimalExpansion: 4096, MaxStringExpansion: 100}

	result, err := interpreter.New().CallSandboxed(
		parseProc(t, `Function Test()
  Return StrLen(Text);
EndFunction`), nil, nil, profile, map[string]any{"Text": "1e10001"},
	)
	if err != nil || result == nil {
		t.Fatalf("lexical numeric-looking string was treated as decimal: result=%v err=%v", result, err)
	}

	result, err = interpreter.New().CallSandboxed(
		parseProc(t, `Function Test()
  Return NumberValue + 0;
EndFunction`), nil, nil, profile, map[string]any{"NumberValue": decimal.New(1, 101)},
	)
	if err != nil {
		t.Fatalf("MaxStringExpansion tightened pure decimal arithmetic: %v", err)
	}
	if got, ok := result.(decimal.Decimal); !ok || got.Cmp(decimal.Zero) <= 0 {
		t.Fatalf("unexpected arithmetic result: %T(%v)", result, result)
	}

	if _, err = interpreter.New().CallSandboxed(
		parseProc(t, `Function Test()
  Return Str(NumberValue);
EndFunction`), nil, nil, profile, map[string]any{"NumberValue": decimal.New(1, 101)},
	); err == nil {
		t.Fatal("formatting ignored the stricter string bound")
	}
}

func TestSandboxLimitsDoNotMakeBuiltinNamesImmutable(t *testing.T) {
	shadow := interpreter.BuiltinFunc(func([]any, string, int) (any, error) {
		return "shadow", nil
	})
	result, err := interpreter.New().CallSandboxed(
		parseProc(t, `Function Test()
  Return Format(1);
EndFunction`), nil, nil,
		interpreter.SandboxProfile{MaxDecimalExpansion: 4096, MaxStringExpansion: 1024},
		map[string]any{"Format": shadow},
	)
	if err != nil || result != "shadow" {
		t.Fatalf("trusted shadow function stopped resolving: result=%v err=%v", result, err)
	}
}

func TestDebuggerConditionsApplyResourceLimits(t *testing.T) {
	tests := []struct {
		name string
		expr string
		vars map[string]any
	}{
		{"round", `Round(1, 10001) = 1`, nil},
		{"numeric operator", `"1e10001" + 0 > 0`, nil},
		{
			"replace",
			`StrReplace(Input, "a", Replacement) <> ""`,
			map[string]any{"Input": strings.Repeat("a", 2048), "Replacement": strings.Repeat("b", 1024)},
		},
		{
			"concat",
			`A + B <> ""`,
			map[string]any{"A": strings.Repeat("a", 600<<10), "B": strings.Repeat("b", 600<<10)},
		},
		{
			"decimal pointer formatting",
			`StrTemplate("%1", NumberValue) <> ""`,
			func() map[string]any {
				value := decimal.New(1, 10001)
				return map[string]any{"NumberValue": &value}
			}(),
		},
		{
			"source nesting before parser",
			strings.Repeat("(", interpreter.MaxUntrustedExpressionSyntaxDepth+1) + "1 = 1" +
				strings.Repeat(")", interpreter.MaxUntrustedExpressionSyntaxDepth+1),
			nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hook := runWithCondition(t, tc.expr, tc.vars)
			if hook.err == nil {
				t.Fatalf("condition bypassed resource limit: %s", tc.expr)
			}
		})
	}
}

func TestSandboxEvalCannotBypassResourceLimits(t *testing.T) {
	profile := interpreter.SandboxProfile{MaxDecimalExpansion: 4096, MaxStringExpansion: 1 << 20}
	tests := []struct {
		name   string
		source string
		vars   map[string]any
	}{
		{
			name: "decimal result",
			source: `Function Test()
  Return Eval("1e10001");
EndFunction`,
		},
		{
			name: "nested parser source",
			source: `Function Test()
  Return Eval(Source);
EndFunction`,
			vars: map[string]any{
				"Source": strings.Repeat("(", interpreter.MaxUntrustedExpressionSyntaxDepth+1) + "1" +
					strings.Repeat(")", interpreter.MaxUntrustedExpressionSyntaxDepth+1),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := interpreter.New().CallSandboxed(parseProc(t, tc.source), nil, nil, profile, tc.vars); err == nil {
				t.Fatal("Eval bypassed sandbox resource limits")
			}
		})
	}
}

func TestDebuggerConditionDoesNotWeakenOuterSandboxLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expr    string
		profile interpreter.SandboxProfile
		vars    map[string]any
	}{
		{
			name:    "string",
			expr:    `StrReplace(Input, "a", Replacement) <> ""`,
			profile: interpreter.SandboxProfile{MaxStringExpansion: 64},
			vars:    map[string]any{"Input": strings.Repeat("a", 10), "Replacement": strings.Repeat("b", 10)},
		},
		{
			name:    "decimal",
			expr:    `Round(1, 5) = 1`,
			profile: interpreter.SandboxProfile{MaxDecimalExpansion: 4},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := &condHook{expr: tc.expr}
			in := interpreter.New()
			in.DebugSource = func() interpreter.DebugHook { return hook }
			proc := parseProc(t, `Procedure Work()
  X = 1;
EndProcedure`)
			var result any
			if err := in.RunSandboxed(proc, nil, tc.profile, &result, tc.vars); err != nil {
				t.Fatalf("debugged sandbox run failed: %v", err)
			}
			if hook.err == nil {
				t.Fatal("condition replaced a stricter outer resource limit")
			}
		})
	}
}

func TestDebuggerConditionRestoresOrdinaryRunLimits(t *testing.T) {
	hook := &condHook{expr: `1 = 1`}
	in := interpreter.New()
	in.DebugSource = func() interpreter.DebugHook { return hook }
	proc := parseProc(t, `Function Work()
  Return StrReplace(Input, "a", Replacement);
EndFunction`)
	var result any
	if err := in.RunWithResult(proc, nil, &result, map[string]any{
		"Input":       strings.Repeat("a", 2048),
		"Replacement": strings.Repeat("b", 1024),
	}); err != nil {
		t.Fatalf("condition limits leaked into ordinary execution: %v", err)
	}
	if output, ok := result.(string); !ok || len(output) != 2048*1024 {
		t.Fatalf("unexpected post-condition output: %T len=%d", result, len(output))
	}
}
