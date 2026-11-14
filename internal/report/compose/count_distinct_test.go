package compose

import (
	"github.com/ivantit66/onebase/internal/report"
	"github.com/shopspring/decimal"
	"testing"
)

func TestCountDistinctGroupsAxesAndGrand(t *testing.T) {
	rows := []Row{{"G": "a", "Period": 1, "ID": "x"}, {"G": "a", "Period": 1, "ID": "x"}, {"G": "a", "Period": 2, "ID": "y"}, {"G": "b", "Period": 1, "ID": "x"}, {"G": "b", "Period": 2, "ID": nil}}
	spec := report.Composition{Groupings: []string{"G"}, Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}}
	r, err := Compose(rows, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Groups[0].Subtotals["ID"] != int64(2) || r.Groups[1].Subtotals["ID"] != int64(1) || r.Grand["ID"] != int64(2) {
		t.Fatalf("distinct groups/grand: %+v", r)
	}
	spec.Columns = []string{"Period"}
	c, err := ComposeCross(rows, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.RowTotal[c.Cols[0].Key()] != int64(1) || c.RowTotal[c.Cols[1].Key()] != int64(1) || c.Rows[0].Cells[c.Cols[0].Key()] != int64(1) {
		t.Fatalf("distinct cross: %+v", c)
	}
	_, byGroup := GroupByField(rows, spec, "G", nil)
	if byGroup["a"]["ID"] != int64(2) {
		t.Fatalf("chart: %v", byGroup)
	}
}

func TestCountDistinctIdentityAndNull(t *testing.T) {
	rows := []Row{{"ID": nil}, {"ID": ""}, {"ID": []byte("")}, {"ID": int64(1)}, {"ID": float64(1)}, {"ID": decimal.RequireFromString("1.00")}, {"ID": "1"}, {"ID": false}, {"ID": false}}
	r, _ := Compose(rows, report.Composition{Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}}, nil)
	if r.Grand["ID"] != int64(4) {
		t.Fatalf("identity: %v", r.Grand)
	}
	r, _ = Compose(nil, report.Composition{Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}}, nil)
	if r.Grand["ID"] != int64(0) {
		t.Fatalf("empty: %v", r.Grand)
	}
}
