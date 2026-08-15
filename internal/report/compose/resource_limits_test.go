package compose

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/report"
)

func TestReportDecimalBoundsApplyBeforeAggregation(t *testing.T) {
	measure := report.Measure{Field: "Amount", Agg: "sum"}
	if got := aggMeasure([]Row{{"Amount": "1e10001"}}, measure); !got.(decimal.Decimal).IsZero() {
		t.Fatalf("unsafe exponent entered aggregate: %v", got)
	}
	if got := aggMeasure([]Row{{"Amount": strings.Repeat("9", maxReportDecimalTextBytes+1)}}, measure); !got.(decimal.Decimal).IsZero() {
		t.Fatalf("oversized numeric text entered aggregate: %v", got)
	}

	got := aggMeasure([]Row{{"Amount": "1e4096"}}, measure).(decimal.Decimal)
	if !got.Equal(decimal.New(1, 4096)) {
		t.Fatalf("safe exponent boundary changed: exponent=%d", got.Exponent())
	}
}

func TestReportDecimalBoundsCoverGroupingSortingAndFilters(t *testing.T) {
	unsafe := decimal.New(1, 10001)
	key := normalizeGroupKey(unsafe)
	if text, ok := key.(string); !ok || !strings.Contains(text, "outside safe bounds") {
		t.Fatalf("unsafe group key was formatted as a decimal: %#v", key)
	}
	if got := compareVals(unsafe, "z"); got >= 0 {
		t.Fatalf("unsafe decimal did not use bounded lexical fallback: %d", got)
	}
	rows := ApplyFilters([]Row{{"Amount": unsafe}}, []report.Filter{{
		Field: "Amount", Op: "contains", Value: "never-present",
	}})
	if len(rows) != 0 {
		t.Fatalf("unexpected filter match for unsafe decimal: %v", rows)
	}
}

func TestReportDecimalConversionSupportsAllowedScalarTypes(t *testing.T) {
	for _, value := range []any{
		int8(10), int16(10), int32(10), uint(10), uint8(10), uint16(10), uint32(10), uint64(10), float32(10.5),
	} {
		d, ok := ExportToDecimal(value)
		if !ok || d.Cmp(decimal.NewFromInt(2)) <= 0 {
			t.Errorf("%T lost numeric semantics: value=%v ok=%v", value, d, ok)
		}
	}
}

func TestDependencyDiscoveryStopsBeforeOversizedFormulaScan(t *testing.T) {
	expr := strings.Repeat("X ", maxDependencyExprTokens+1)
	deps := exprIdentDeps(expr, map[string]bool{"x": true}, "self")
	if !deps["x"] {
		t.Fatal("bounded dependency scan lost early identifiers")
	}

	tooLarge := strings.Repeat(" ", maxDependencyExprBytes+1) + "X"
	if got := exprIdentDeps(tooLarge, map[string]bool{"x": true}, "self"); len(got) != 0 {
		t.Fatalf("oversized dependency expression was scanned: %v", got)
	}
}
