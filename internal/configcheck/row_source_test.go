package configcheck

import (
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
)

func TestReportRowSourceCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  *report.RowSourceDefinition
		bad  string
	}{
		{"unset", nil, ""},
		{"valid", &report.RowSourceDefinition{Procedure: "Обогатить", Columns: []report.RowSourceColumn{{Name: "Работы", Type: "string"}}}, ""},
		{"procedure", &report.RowSourceDefinition{Procedure: "../Build", Columns: []report.RowSourceColumn{{Name: "Works", Type: "string"}}}, "процедуры"},
		{"no_columns", &report.RowSourceDefinition{Procedure: "Build"}, "от 1 до 32"},
		{"duplicate", &report.RowSourceDefinition{Procedure: "Build", Columns: []report.RowSourceColumn{{Name: "Works", Type: "string"}, {Name: "wORKS", Type: "string"}}}, "повторное"},
		{"capability_type", &report.RowSourceDefinition{Procedure: "Build", Columns: []report.RowSourceColumn{{Name: "Works", Type: "reference:X"}}}, "тип"},
		{"internal_name", &report.RowSourceDefinition{Procedure: "Build", Columns: []report.RowSourceColumn{{Name: "__factory", Type: "string"}}}, "имя"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := projWith(nil)
			proj.Reports[0].RowSource = tc.def
			issues := CheckReportComposition(proj)
			if tc.bad == "" && len(issues) > 0 {
				t.Fatal(issues)
			}
			if tc.bad != "" && !issuesContain(issues, tc.bad) {
				t.Fatalf("wanted %s: %+v", tc.bad, issues)
			}
		})
	}
}
func TestReportRowSourceRunFullYAML(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "rows.yaml"), `name: Rows
query: SELECT 1 AS N
row_source:
  procedure: Обогатить
  columns:
  - {name: Работы, type: string}
`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if !result.OK || len(result.Warnings) > 0 {
		t.Fatalf("%+v", result)
	}
}
