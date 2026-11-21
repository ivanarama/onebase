package report_test

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"testing"
)

func TestReportTypedDrilldownMetadata(t *testing.T) {
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{{Name: "Invoice", Kind: metadata.KindDocument}, {Name: "Task", Kind: metadata.KindCatalog}}})
	for _, tc := range []struct {
		name  string
		d     report.Drilldown
		valid bool
	}{
		{"entity", report.Drilldown{Field: "Name", IDField: "ID", Target: &report.DrilldownTarget{Kind: "entity", Entity: "Invoice"}}, true},
		{"image", report.Drilldown{Title: "Photo", IDField: "Image", Target: &report.DrilldownTarget{Kind: "image"}}, true},
		{"dynamic", report.Drilldown{Field: "Name", IDField: "ID", TypeField: "Type", Targets: map[string]report.DrilldownTarget{"invoice": {Kind: "entity", Entity: "Invoice"}, "task": {Kind: "entity", Entity: "Task"}}}, true},
		{"unknown-entity", report.Drilldown{Field: "Name", IDField: "ID", Target: &report.DrilldownTarget{Kind: "entity", Entity: "Unknown"}}, false},
		{"url", report.Drilldown{Field: "Name", IDField: "ID", Target: &report.DrilldownTarget{Kind: "url", Entity: "https://example.invalid"}}, false},
		{"binary-owner-override", report.Drilldown{Field: "Name", IDField: "ID", Target: &report.DrilldownTarget{Kind: "image", Entity: "Task"}}, false},
		{"unbound-dynamic", report.Drilldown{Field: "Name", IDField: "ID", TypeField: "Type"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &report.Report{Drilldowns: []report.Drilldown{tc.d}}
			if err := report.ValidateDrilldowns(r, reg); (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
		})
	}
	r, err := report.ParseBytes([]byte("name: Typed\ndrilldowns:\n - field: Name\n   id_field: ID\n   type_field: Type\n   targets:\n     invoice: {kind: entity, entity: Invoice}\n     photo: {kind: image}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateDrilldowns(r, reg); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Drilldowns[0].SelectTarget("Unknown"); ok {
		t.Fatal("unknown selector became a target")
	}
	if _, err := report.ParseBytes([]byte("name: Unsafe\ndrilldowns:\n - field: Name\n   id_field: ID\n   target: {kind: entity, entity: Invoice, url: 'javascript:evil'}\n")); err == nil {
		t.Fatal("arbitrary URL accepted")
	}
}
