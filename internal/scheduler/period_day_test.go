package scheduler

import (
	"testing"
	"time"
)

func TestPeriodDayDefaultTemplate(t *testing.T) {
	for _, tc := range []struct{ op, want string }{{"start_of_day", "00:00:00"}, {"end_of_day", "23:59:59"}} {
		got, err := ResolveParamTemplateText("{{today | "+tc.op+"}}", "datetime", nil)
		if err != nil {
			t.Fatal(err)
		}
		value, err := time.ParseInLocation("2006-01-02T15:04:05", got, time.Local)
		if err != nil || value.Format("15:04:05") != tc.want {
			t.Fatalf("%s: %s %v", tc.op, got, err)
		}
	}
	const explicit = "2026-09-02T12:34:56"
	if got, err := ResolveParamTemplateText(explicit, "datetime", nil); err != nil || got != explicit {
		t.Fatalf("explicit time changed: %q %v", got, err)
	}
}
