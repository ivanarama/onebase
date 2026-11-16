package compose

import (
	"github.com/ivantit66/onebase/internal/report"
	"github.com/shopspring/decimal"
	"testing"
)

func scopeFixture() ([]Row, report.Composition) {
	return []Row{
		{"Бригада": "Б", "Мастер": "М", "Тип": "Т", "Период": "P", "Смен": decimal.NewFromInt(1), "Сумма": decimal.NewFromInt(10)},
		{"Бригада": "Б", "Мастер": "М", "Тип": "Т", "Период": "P", "Смен": decimal.Zero, "Сумма": decimal.NewFromInt(20)},
	}, report.Composition{
		Groupings: []string{"Бригада", "Мастер", "Тип"}, Detail: true,
		Measures: []report.Measure{{Field: "Смен", Agg: "sum", Scope: []string{report.MeasureScopeGrand}}, {Field: "Сумма", Agg: "sum"}},
		Totals:   report.Totals{Grand: true, Subtotals: true},
	}
}

func TestMeasureScopeGrandOnly(t *testing.T) {
	rows, spec := scopeFixture()
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, result.Grand["Смен"], "1")
	decEq(t, result.Grand["Сумма"], "30")
	if result.RowCount != 2 {
		t.Fatal(result.RowCount)
	}
	for groups := result.Groups; len(groups) > 0; groups = groups[0].Children {
		g := groups[0]
		if g.Count != 2 || g.Subtotals["Смен"] != nil {
			t.Fatalf("unexpected subtotal: %+v", g)
		}
		decEq(t, g.Subtotals["Сумма"], "30")
		for _, detail := range g.Details {
			if detail.Values["Смен"] != nil {
				t.Fatalf("detail exposed out-of-scope resource: %v", detail.Values)
			}
		}
	}
	// Blanking detail output must not corrupt the source or future exports.
	decEq(t, rows[0]["Смен"], "1")
	again, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, again.Grand["Смен"], "1")
}

func TestMeasureScopeNamedAndDetail(t *testing.T) {
	rows, spec := scopeFixture()
	spec.Measures[0].Scope = []string{"мАстер", report.MeasureScopeDetail}
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Grand["Смен"] != nil || result.Groups[0].Subtotals["Смен"] != nil {
		t.Fatal("named scope leaked upward")
	}
	master := result.Groups[0].Children[0]
	decEq(t, master.Subtotals["Смен"], "1")
	if master.Children[0].Subtotals["Смен"] != nil {
		t.Fatal("named scope leaked downward")
	}
	decEq(t, master.Children[0].Details[0].Values["Смен"], "1")
	// A chart aggregation is a named group, never an implicit grand total.
	_, chart := GroupByField(rows, spec, "Бригада", noEval{})
	if chart["Б"]["Смен"] != nil {
		t.Fatal("chart bypassed scope")
	}
	_, chart = GroupByField(rows, spec, "Мастер", noEval{})
	decEq(t, chart["М"]["Смен"], "1")
}

type scopeEval struct{ calls int }

func (*scopeEval) EvalBool(string, Row) (bool, error) { return false, nil }
func (e *scopeEval) EvalNum(string, Row) (decimal.Decimal, bool, error) {
	e.calls++
	return decimal.NewFromInt(17), true, nil
}

func TestMeasureScopeExcludedExpressionNeverRuns(t *testing.T) {
	rows, spec := scopeFixture()
	spec.Measures[0] = report.Measure{Field: "Вычисление", Expr: "17", Scope: []string{report.MeasureScopeGrand}}
	ev := &scopeEval{}
	result, err := Compose(rows, spec, ev)
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, result.Grand["Вычисление"], "17")
	if ev.calls != 1 {
		t.Fatalf("expression evaluated %d times, expected grand only", ev.calls)
	}
	for groups := result.Groups; len(groups) > 0; groups = groups[0].Children {
		if groups[0].Subtotals["Вычисление"] != nil {
			t.Fatal("computed subtotal outside scope")
		}
	}
}

func TestMeasureScopeCrossKeepsIndexes(t *testing.T) {
	rows, spec := scopeFixture()
	spec.Detail = false
	spec.Columns = []string{"Период"}
	// Same raw field, distinct resource indexes and scopes.
	spec.Measures = append(spec.Measures, report.Measure{Field: "Смен", Agg: "avg", Scope: []string{"Мастер"}})
	result, err := ComposeCross(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	sum, amount, avg := result.Cols[0].Key(), result.Cols[1].Key(), result.Cols[2].Key()
	decEq(t, result.RowTotal[sum], "1")
	decEq(t, result.RowTotal[amount], "30")
	if result.RowTotal[avg] != nil {
		t.Fatal("master-only average appeared in grand")
	}
	for groups := result.Rows; len(groups) > 0; groups = groups[0].Children {
		g := groups[0]
		if g.Cells[sum] != nil {
			t.Fatal("grand resource appeared in pivot subtotal")
		}
		decEq(t, g.Cells[amount], "30")
		if g.Field == "Мастер" {
			decEq(t, g.Cells[avg], "0.5")
		} else if g.Cells[avg] != nil {
			t.Fatal("named resource appeared at another pivot level")
		}
	}
}

func TestMeasureScopeUnspecifiedPreservesLegacy(t *testing.T) {
	rows, spec := scopeFixture()
	spec.Measures[0].Scope = nil
	result, err := Compose(rows, spec, noEval{})
	if err != nil {
		t.Fatal(err)
	}
	decEq(t, result.Grand["Смен"], "1")
	for groups := result.Groups; len(groups) > 0; groups = groups[0].Children {
		decEq(t, groups[0].Subtotals["Смен"], "1")
	}
}
