package metadata

import "testing"

func TestNumeratorPrefixDistinguishesPeriod(t *testing.T) {
	tests := []struct {
		period string
		prefix string
		want   bool
	}{
		{period: "none", prefix: "R-", want: true},
		{period: "year", prefix: "R-{YYYY}-", want: true},
		{period: "year", prefix: "R-{BASE}-", want: false},
		{period: "month", prefix: "R-{YYYY}-", want: false},
		{period: "month", prefix: "R-{MM}-", want: false},
		{period: "month", prefix: "R-{YY}{MM}-", want: true},
		{period: "day", prefix: "R-{YYYY}{MM}-", want: false},
		{period: "day", prefix: "R-{YYYY}{MM}{DD}-", want: true},
	}
	for _, test := range tests {
		t.Run(test.period+"/"+test.prefix, func(t *testing.T) {
			if got := NumeratorPrefixDistinguishesPeriod(test.prefix, test.period); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestValidateNumeratorRejectsDailyCatalogReset(t *testing.T) {
	entity := &Entity{
		Name:      "Products",
		Kind:      KindCatalog,
		Fields:    []Field{{Name: StandardCodeField, Type: FieldTypeString}},
		Numerator: &Numerator{Period: "day"},
	}
	if err := Validate([]*Entity{entity}, nil); err == nil {
		t.Fatal("daily counter reset was accepted for a catalog")
	}
}
