package configcheck

import (
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
	"testing"
)

func TestCheckReportTrustedContext(t *testing.T) {
	card := &metadata.Entity{Name: "Staff", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Account", Type: "reference:_users", RefEntity: "_users"}, {Name: "Department", Type: "reference:Unit", RefEntity: "Unit"},
	}}
	for _, tc := range []struct {
		name  string
		param report.Param
		valid bool
	}{
		{"login", report.Param{Name: "Actor", Type: "string", Context: &report.ParamContext{User: "login"}}, true},
		{"id", report.Param{Name: "Actor", Type: "reference:_users", Context: &report.ParamContext{User: "id"}}, true},
		{"card", report.Param{Name: "Unit", Type: "reference:Unit", Context: &report.ParamContext{UserCard: &auth.UserCardSource{Catalog: "Staff", AccountField: "Account", Field: "Department"}}}, true},
		{"missing-card", report.Param{Name: "Unit", Type: "string", Context: &report.ParamContext{UserCard: &auth.UserCardSource{Catalog: "Missing", AccountField: "Account", Field: "Department"}}}, false},
		{"wrong-card-type", report.Param{Name: "Unit", Type: "string", Context: &report.ParamContext{UserCard: &auth.UserCardSource{Catalog: "Staff", AccountField: "Account", Field: "Department"}}}, false},
		{"unknown-source", report.Param{Name: "Actor", Type: "string", Context: &report.ParamContext{User: "department"}}, false},
		{"default-fallback", report.Param{Name: "Actor", Type: "string", Default: "bob", Context: &report.ParamContext{User: "login"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &project.Project{Entities: []*metadata.Entity{card}, Reports: []*report.Report{{Name: "Context", Params: []report.Param{tc.param}}}}
			got := CheckReportContext(p)
			if (len(got) == 0) != tc.valid {
				t.Fatalf("valid=%t issues=%v", tc.valid, got)
			}
		})
	}
}
