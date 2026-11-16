package access_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportOutputVirtualImplicitProtectedDependencies(t *testing.T) {
	for _, flag := range []string{"pii", "report_pii"} {
		t.Run(flag, func(t *testing.T) {
			field := metadata.Field{Name: "Phone", Type: "string", PII: flag == "pii", ReportPII: flag == "report_pii"}
			ir := &metadata.InfoRegister{Name: "ProtectedSlice", Periodic: true, Dimensions: []metadata.Field{field}, Resources: []metadata.Field{{Name: "Value", Type: "number"}}}
			lookup := func(kind, name string) *metadata.Entity {
				if kind == "inforeg" && name == ir.Name {
					return storage.InfoRegisterPredicateEntity(ir)
				}
				return nil
			}
			for _, method := range []string{"LastSlice", "FirstSlice"} {
				t.Run(method, func(t *testing.T) {
					res, err := query.Compile("SELECT SUM(S.Value) AS Total FROM InformationRegister.ProtectedSlice."+method+"() AS S", query.CompileOpts{InfoRegs: []*metadata.InfoRegister{ir}, Dialect: storage.SQLiteDialect{}})
					if err != nil {
						t.Fatal(err)
					}
					admin := &auth.User{IsAdmin: true}
					if plan := access.ReportOutputQueryMaskPlan(admin, res, lookup); plan.Denied == "" {
						t.Fatal("protected implicit VT grouping escaped report gate")
					}
					if plan := access.QueryMaskPlanFor(admin, res, lookup); !plan.Empty() {
						t.Fatal("ordinary admin query changed")
					}
				})
			}
		})
	}
}

func TestReportOutputVirtualPublicAndPhysicalControls(t *testing.T) {
	for _, mode := range []string{"public_virtual", "protected_physical"} {
		t.Run(mode, func(t *testing.T) {
			ir := &metadata.InfoRegister{Name: "ControlSlice", Periodic: true, Dimensions: []metadata.Field{{Name: "Key", Type: "string", PII: mode == "protected_physical"}}, Resources: []metadata.Field{{Name: "Value", Type: "number"}}}
			source := "InformationRegister.ControlSlice"
			if mode == "public_virtual" {
				source += ".LastSlice()"
			}
			res, err := query.Compile("SELECT SUM(S.Value) AS Total FROM "+source+" AS S", query.CompileOpts{InfoRegs: []*metadata.InfoRegister{ir}, Dialect: storage.SQLiteDialect{}})
			if err != nil {
				t.Fatal(err)
			}
			lookup := func(kind, name string) *metadata.Entity {
				if kind == "inforeg" && name == ir.Name {
					return storage.InfoRegisterPredicateEntity(ir)
				}
				return nil
			}
			if plan := access.ReportOutputQueryMaskPlan(nil, res, lookup); !plan.Empty() {
				t.Fatalf("untouched safe control denied: %s", plan.Denied)
			}
		})
	}
}
