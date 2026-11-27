package compose_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/report/expreval"
)

func number(t *testing.T, value any, want string) {
	t.Helper()
	d, ok := compose.ExportToDecimal(value)
	if !ok || d.String() != want {
		t.Fatalf("value=%v, want %s", value, want)
	}
}

func TestRelativeMeasureGrand(t *testing.T) {
	spec := report.Composition{Groupings: []string{"G", "Team"}, Measures: []report.Measure{
		{Field: "Share", Expr: "Amount / Grand_Amount * 100"},
		{Field: "DoubleShare", Expr: "Twice / gRaNd_Twice * 100"},
		{Field: "Twice", Expr: "Amount * 2"},
		{Field: "Amount", Agg: "sum"},
	}}
	rows := []compose.Row{{"G": "a", "Team": "one", "amount": 20}, {"G": "a", "Team": "two", "amount": 30}, {"G": "b", "Team": "one", "amount": 50}}
	ev := expreval.New(nil, expreval.DefaultProfile())
	r, err := compose.Compose(rows, spec, ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 0 {
		t.Fatal(r.Warnings)
	}
	number(t, r.Grand["Share"], "100")
	number(t, r.Groups[0].Subtotals["Share"], "50")
	number(t, r.Groups[0].Children[0].Subtotals["Share"], "20")
	number(t, r.Groups[0].Subtotals["DoubleShare"], "50")
	_, chart := compose.GroupByField(rows, spec, "G", ev)
	number(t, chart["a"]["Share"], "50")
}

func TestRelativeMeasureColumns(t *testing.T) {
	spec := report.Composition{Groupings: []string{"G"}, Columns: []string{"Year"}, Measures: []report.Measure{
		{Field: "Amount", Agg: "sum"},
		{Field: "Change", Expr: "Amount - Previous_Amount"},
		{Field: "FirstRatio", Expr: "Amount / First_Amount * 100"},
		{Field: "Share", Expr: "Amount / Grand_Amount * 100"},
	}}
	rows := []compose.Row{{"G": "a", "Year": 3, "Amount": 30}, {"G": "a", "Year": 1, "Amount": 10}, {"G": "a", "Year": 2, "Amount": 20}, {"G": "b", "Year": 1, "Amount": 40}, {"G": "b", "Year": 3, "Amount": 0}}
	r, err := compose.ComposeCross(rows, spec, expreval.New(nil, expreval.DefaultProfile()))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 0 {
		t.Fatal(r.Warnings)
	}
	cell := func(g string, year string, field string) any {
		for _, row := range r.Rows {
			if row.Key != g {
				continue
			}
			for _, col := range r.Cols {
				if col.Path[0] == year && col.Measure == field {
					return row.Cells[col.Key()]
				}
			}
		}
		return nil
	}
	number(t, cell("a", "2", "Change"), "10")
	number(t, cell("a", "3", "FirstRatio"), "300")
	number(t, cell("a", "3", "Share"), "30")
	if cell("a", "1", "Change") != nil || cell("b", "3", "Change") != nil {
		t.Fatal("missing previous column must be blank")
	}
	number(t, r.Grand["Amount"], "100")
	for _, col := range r.Cols {
		if col.Path[0] == "2" && col.Measure == "Change" {
			number(t, r.RowTotal[col.Key()], "-30")
		}
	}
	// No first column for this group: never substitute the first nonempty cell.
	rows = append(rows, compose.Row{"G": "c", "Year": 2, "Amount": 7})
	r, _ = compose.ComposeCross(rows, spec, expreval.New(nil, expreval.DefaultProfile()))
	if cell("c", "2", "FirstRatio") != nil {
		t.Fatal("first missing column must be blank")
	}
}

func TestRelativeMeasureZeroAndLegacyName(t *testing.T) {
	spec := report.Composition{Groupings: []string{"G"}, Measures: []report.Measure{{Field: "Amount"}, {Field: "Share", Expr: "Amount / Grand_Amount"}}}
	r, _ := compose.Compose([]compose.Row{{"G": "a", "Amount": 0}}, spec, expreval.New(nil, expreval.DefaultProfile()))
	if r.Groups[0].Subtotals["Share"] != nil || len(r.Warnings) != 0 {
		t.Fatalf("zero total: %+v", r)
	}
	spec.Measures = []report.Measure{{Field: "Grand_Amount"}, {Field: "X", Expr: "Grand_Amount * 2"}}
	r, _ = compose.Compose([]compose.Row{{"G": "a", "Grand_Amount": 3}}, spec, expreval.New(nil, expreval.DefaultProfile()))
	number(t, r.Grand["X"], "6")
}
