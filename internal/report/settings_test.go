package report

import "testing"

func TestUserSettingsRoundTrip(t *testing.T) {
	s := &UserReportSettings{
		Variant: "По складам",
		Composition: &Composition{
			Groupings: []string{"Товар"},
			Measures:  []Measure{{Field: "Сумма", Agg: "sum"}},
		},
		Filters: []Filter{{Field: "Сумма", Op: "gt", Value: "100"}},
	}
	raw, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseUserSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("got nil")
		return
	}
	if got.Variant != "По складам" {
		t.Fatalf("variant: %q", got.Variant)
	}
	if got.Composition == nil || len(got.Composition.Groupings) != 1 || got.Composition.Groupings[0] != "Товар" {
		t.Fatalf("composition groupings: %+v", got.Composition)
	}
	m := got.Composition.Measures
	if len(m) != 1 || m[0].Field != "Сумма" || m[0].Agg != "sum" {
		t.Fatalf("composition measures: %+v", m)
	}
	if len(got.Filters) != 1 || got.Filters[0].Field != "Сумма" || got.Filters[0].Op != "gt" || got.Filters[0].Value != "100" {
		t.Fatalf("filters: %+v", got.Filters)
	}
}

func TestParseUserSettingsEmpty(t *testing.T) {
	got, err := ParseUserSettings("")
	if err != nil {
		t.Fatalf("empty: err=%v", err)
	}
	if got != nil {
		t.Fatalf("empty: ожидали nil, получили %+v", got)
	}
}
