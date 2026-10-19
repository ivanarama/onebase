package report

import (
	"testing"
	"time"
)

// period_end: дата «По» — конец дня, как КонецПериода(…, День) в 1С; в форме
// и в API одинаково. Без ключа — полночь, как раньше.
func TestParseParamValue_PeriodEnd(t *testing.T) {
	for _, mode := range []ParamParseMode{ParamParseForm, ParamParseAPI} {
		got, err := ParseParamValue("2026-10-12", Param{Name: "По", Type: "date", PeriodEnd: true}, mode)
		if err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, time.October, 12, 23, 59, 59, 0, time.Local); !got.(time.Time).Equal(want) {
			t.Fatalf("mode %v: %v, want %v", mode, got, want)
		}
	}
	got, err := ParseParamValue("2026-10-12", Param{Name: "По", Type: "date"}, ParamParseForm)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.Local); !got.(time.Time).Equal(want) {
		t.Fatalf("без period_end — полночь: %v", got)
	}
}
