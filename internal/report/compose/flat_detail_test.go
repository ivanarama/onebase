package compose

import (
	"testing"

	"github.com/ivantit66/onebase/internal/report"
)

func TestFlatDetailsWithoutGrouping(t *testing.T) {
	spec := report.Composition{Detail: true,
		Measures: []report.Measure{{Field: "Amount", Agg: "sum"}},
		Sort:     []report.SortKey{{Field: "Amount", Dir: "desc"}}}
	rows := []Row{{"amount": 2}, {"amount": 7}, {"amount": 99}}
	got, err := ComposeN(rows, spec, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 0 || len(got.Details) != 2 || got.Details[0].Values["Amount"] != 7 || !got.Capped || got.RowCount != 2 {
		t.Fatalf("flat result: %#v", got)
	}
	if d, _ := ExportToDecimal(got.Grand["Amount"]); d.String() != "9" {
		t.Fatalf("grand: %v", got.Grand)
	}
	spec.Detail = false
	got, _ = Compose(rows, spec, nil)
	if len(got.Details) != 0 {
		t.Fatal("detail:false exposed details")
	}
}
