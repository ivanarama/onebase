package compose

import (
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/shopspring/decimal"
)

func levelCalculationFixture(t *testing.T) ([]Row, report.Composition) {
	t.Helper()
	r, err := report.ParseBytes([]byte(`name: level-calculations
composition:
  groupings: [Direction, Brigade, Master]
  measures:
  - field: Percentage
    agg: avg
    calculations:
    - {level: Direction, agg: avg, group_by: Brigade, inner_agg: avg}
    - {level: $grand, agg: avg, group_by: Direction, inner_agg: avg}
  totals: {grand: true, subtotals: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	rows := []Row{
		{"Direction": "D1", "Brigade": "B1", "Master": "M1", "Period": "P1", "Percentage": 100},
		{"Direction": "D1", "Brigade": "B1", "Master": "M1", "Period": "P2", "Percentage": 0},
		{"Direction": "D1", "Brigade": "B1", "Master": "M2", "Period": "P1", "Percentage": 0},
		{"Direction": "D1", "Brigade": "B2", "Master": "M3", "Period": "P1", "Percentage": 100},
		{"Direction": "D1", "Brigade": "B2", "Master": "M4", "Period": "P1", "Percentage": 100},
		{"Direction": "D1", "Brigade": "B2", "Master": "M5", "Period": "P1", "Percentage": 100},
		{"Direction": "D1", "Brigade": "B2", "Master": "M5", "Period": "P2", "Percentage": 100},
		{"Direction": "D2", "Brigade": "B3", "Master": "M6", "Period": "P1", "Percentage": 0},
	}
	return rows, *r.Composition
}
func nearCalculation(t *testing.T, value any, want decimal.Decimal) {
	t.Helper()
	got, ok := toDecimal(value)
	if !ok || got.Sub(want).Abs().GreaterThan(decimal.RequireFromString("0.000000000001")) {
		t.Fatalf("got %v, want %s", value, want)
	}
}
func TestMeasureCalculationUnequalGroups(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	nearCalculation(t, result.Groups[0].Subtotals["Percentage"], decimal.NewFromInt(200).Div(decimal.NewFromInt(3)))
	nearCalculation(t, result.Grand["Percentage"], decimal.NewFromInt(250).Div(decimal.NewFromInt(7)))
	// Grand is avg(raw direction means), not avg(displayed direction resources).
	nearCalculation(t, result.Groups[0].Children[0].Subtotals["Percentage"], decimal.NewFromInt(100).Div(decimal.NewFromInt(3)))
	if result.RowCount != 8 || rows[0]["Percentage"] != 100 {
		t.Fatal("input changed")
	}
}
func TestMeasureCalculationCrossPeriods(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	spec.Columns = []string{"Period"}
	result, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, result.Rows[0].Cells[colKey([]any{"P1"}, 0)], "75")
	decEq(t, result.Rows[0].Cells[colKey([]any{"P2"}, 0)], "50")
	decEq(t, result.RowTotal[colKey([]any{"P1"}, 0)], "40")
	decEq(t, result.RowTotal[colKey([]any{"P2"}, 0)], "50")
	if result.Rows[1].Cells[colKey([]any{"P2"}, 0)] != nil {
		t.Fatal("missing period was synthesized")
	}
}

func TestMeasureCalculationNullGroupsAndScope(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	rows = append(rows, Row{"Direction": "D3", "Brigade": nil, "Master": "M7", "Period": "P1", "Percentage": nil})
	spec.Measures[0].Scope = []string{report.MeasureScopeGrand}
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	// Null group mean is absent and does not turn into a zero-valued direction.
	nearCalculation(t, result.Grand["Percentage"], decimal.NewFromInt(250).Div(decimal.NewFromInt(7)))
	if result.Groups[0].Subtotals["Percentage"] != nil {
		t.Fatal("scope ignored")
	}
	empty, err := Compose(nil, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Grand["Percentage"] != nil {
		t.Fatal("empty average synthesized")
	}
}
func TestMeasureCalculationCaseAndRemovedGrouping(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	spec.Measures[0].Calculations[0].Level = "dIrEcTiOn"
	spec.Measures[0].Calculations[0].GroupBy = "bRiGaDe"
	spec.Groupings = []string{"Direction"} // A runtime presentation may omit Brigade/Master.
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	nearCalculation(t, result.Groups[0].Subtotals["Percentage"], decimal.NewFromInt(200).Div(decimal.NewFromInt(3)))
}
func TestMeasureCalculationMissingSourceAndInvalidRules(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	delete(rows[0], "Brigade")
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Groups[0].Subtotals["Percentage"] != nil || len(result.Warnings) == 0 {
		t.Fatal("missing group_by silently computed")
	}
	spec.Measures[0].Calculations = append(spec.Measures[0].Calculations, spec.Measures[0].Calculations[0])
	if _, err := Compose(rows, spec, noEval{}); err == nil {
		t.Fatal("duplicate rule accepted")
	}
	if _, err := ComposeCross(rows, spec, noEval{}); err == nil {
		t.Fatal("duplicate cross rule accepted")
	}
}
func TestMeasureCalculationCrossResourceIndex(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	spec.Columns = []string{"Period"}
	flat := spec.Measures[0]
	flat.Calculations = nil
	spec.Measures = append(spec.Measures, flat)
	result, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, result.Rows[0].Cells[colKey([]any{"P1"}, 0)], "75")
	decEq(t, result.Rows[0].Cells[colKey([]any{"P1"}, 1)], "80")
}
func TestMeasureCalculationLevelExpressionDependency(t *testing.T) {
	rows, spec := levelCalculationFixture(t)
	spec.Measures = []report.Measure{
		{Field: "Double", Expr: "Сумма*2", Calculations: []report.MeasureCalculation{{Level: "Direction", Expr: "Сумма*4"}}},
		{Field: "Сумма", Agg: "sum"},
	}
	for _, r := range rows {
		r["Сумма"] = r["Percentage"]
	}
	ev := calculationExprEval{}
	result, err := Compose(rows, spec, ev)
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, result.Grand["Double"], "1000")
	decEq(t, result.Groups[0].Subtotals["Double"], "2000")
	spec.Columns = []string{"Period"}
	cross, err := ComposeCross(rows, spec, ev)
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, cross.Rows[0].Cells[colKey([]any{"P1"}, 0)], "1600")
}

type calculationExprEval struct{ exprEvalMul }

func (calculationExprEval) EvalNum(expr string, row Row) (decimal.Decimal, bool, error) {
	if expr == "Сумма*4" {
		d, ok := toDecimal(row["Сумма"])
		return d.Mul(decimal.NewFromInt(4)), ok, nil
	}
	return (exprEvalMul{}).EvalNum(expr, row)
}
