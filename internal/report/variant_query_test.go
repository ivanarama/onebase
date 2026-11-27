package report

import (
	"strings"
	"testing"
)

func TestReportVariantQueryBindingsAreIsolated(t *testing.T) {
	r := &Report{Query: "SELECT 1 AS Flag", Variants: []ReportVariant{{Name: "v", Query: "SELECT 0 AS Flag", Filters: []VariantFilter{{Field: "Flag", Op: "eq", Value: false}, {Field: "Other", Op: "eq", Value: nil}}}}}
	params := map[string]any{"__REPORT_VARIANT_0": "client", "OtherParam": 0}
	text, values, err := r.QueryForVariant("v", params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "SELECT 0 AS Flag") || !strings.Contains(text, "ЕСТЬ NULL") || values["__report_variant_0_"] != false || values["__REPORT_VARIANT_0"] != "client" {
		t.Fatalf("query=%s values=%v", text, values)
	}
	if len(params) != 2 || r.Query != "SELECT 1 AS Flag" {
		t.Fatal("variant mutated shared data")
	}
	text, _, err = r.QueryForVariant("missing", params)
	if err != nil || text != r.Query {
		t.Fatalf("unknown variant fallback: %q %v", text, err)
	}
	for _, f := range []VariantFilter{{Field: "Flag; DROP", Op: "eq", Value: 1}, {Field: "Flag", Op: "bad", Value: 1}, {Field: "Flag", Op: "lt", Value: nil}, {Field: "Flag", Op: "eq", Value: []string{"x"}}} {
		r.Variants[0].Filters = []VariantFilter{f}
		if _, _, err = r.QueryForVariant("v", params); err == nil {
			t.Fatalf("invalid filter accepted: %+v", f)
		}
	}
}
