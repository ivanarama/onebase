package configcheck

import (
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/shopspring/decimal"
)

// RunFull is the same entry point used by onebase check and the configurator.
func TestRunFullAcceptsSortOnResultDetailField(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: DetailSort\nversion: '1.0'\n")
	mkFile(t, filepath.Join(dir, "catalogs", "line.yaml"), `name: Line
fields:
  - name: GroupKey
    type: string
  - name: Amount
    type: number
  - name: Moment
    type: date
`)
	mkFile(t, filepath.Join(dir, "reports", "details.yaml"), `name: Details
query: ВЫБРАТЬ Л.GroupKey КАК g, Л.Amount КАК n, Л.Moment КАК d ИЗ Справочник.Line КАК Л
composition:
  groupings: [g]
  measures:
    - field: n
      agg: sum
  detail: true
  sort:
    - field: D
      dir: desc
variants:
  - name: Flat
    composition:
      groupings: []
      measures:
        - field: n
          agg: sum
      detail: true
      sort:
        - field: d
          dir: asc
`)
	got := RunFull(dir)
	if !got.OK {
		t.Fatalf("public check rejects a runtime-supported detail sort: %+v", got.Issues)
	}
}

func TestDetailSortValidationDoesNotAllowUnknownColumns(t *testing.T) {
	entity := &metadata.Entity{Name: "Line", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Label", Type: "string"}, {Name: "Amount", Type: "number"}}}
	for _, tc := range []struct {
		name, query, field string
		detail             bool
		columns            []string
		wantIssue          bool
	}{
		{name: "alias", query: "ВЫБРАТЬ Label КАК label, Amount КАК amount ИЗ Справочник.Line", field: "LABEL", detail: true},
		{name: "direct", query: "ВЫБРАТЬ Label, Amount ИЗ Справочник.Line", field: "label", detail: true},
		{name: "unknown", query: "ВЫБРАТЬ Label КАК label ИЗ Справочник.Line", field: "absent", detail: true, wantIssue: true},
		{name: "no details", query: "ВЫБРАТЬ Label КАК label ИЗ Справочник.Line", field: "label", detail: false, wantIssue: true},
		{name: "pivot", query: "ВЫБРАТЬ Label КАК label ИЗ Справочник.Line", field: "label", detail: true, columns: []string{"Month"}, wantIssue: true},
		{name: "duplicate", query: "ВЫБРАТЬ Label КАК label, Amount КАК label ИЗ Справочник.Line", field: "label", detail: true, wantIssue: true},
		{name: "wildcard", query: "ВЫБРАТЬ * ИЗ Справочник.Line", field: "label", detail: true, wantIssue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := &report.Report{Name: "Details", Query: tc.query, Composition: &report.Composition{Detail: tc.detail, Columns: tc.columns, Sort: []report.SortKey{{Field: tc.field, Dir: "asc"}}}}
			issues := CheckReportComposition(&project.Project{Entities: []*metadata.Entity{entity}, Reports: []*report.Report{rep}})
			if got := issuesContain(issues, "поле сортировки"); got != tc.wantIssue {
				t.Fatalf("sort issue=%v want=%v: %+v", got, tc.wantIssue, issues)
			}
		})
	}
}

func TestRuntimeDetailSortingKeepsGroupTotalsAndLinks(t *testing.T) {
	rows := []compose.Row{{"group": "G", "amount": 1, "stamp": "2026-11-01", "id": "first"}, {"group": "G", "amount": 2, "stamp": "2026-11-03", "id": "last"}, {"group": "G", "amount": 3, "stamp": "2026-11-02", "id": "middle"}}
	spec := report.Composition{Groupings: []string{"group"}, Measures: []report.Measure{{Field: "amount", Agg: "sum"}}, Detail: true, DetailLink: "id", Sort: []report.SortKey{{Field: "STAMP", Dir: "desc"}}}
	got, err := compose.Compose(rows, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].Details) != 3 {
		t.Fatalf("details missing %+v", got)
	}
	for i, want := range []string{"last", "middle", "first"} {
		if got.Groups[0].Details[i].Values["id"] != want {
			t.Fatalf("detail sort/link differs %+v", got.Groups[0].Details)
		}
	}
	if got.RowCount != 3 || got.Groups[0].Count != 3 {
		t.Fatal("row count changed")
	}
	for _, total := range []any{got.Grand["amount"], got.Groups[0].Subtotals["amount"]} {
		n, ok := total.(decimal.Decimal)
		if !ok || !n.Equal(decimal.NewFromInt(6)) {
			t.Fatalf("total changed: %v", total)
		}
	}
}
