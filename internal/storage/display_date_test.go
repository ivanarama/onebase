package storage

import (
	"testing"
	"time"
)

func TestFormatDisplayDateShowsSecondsUnlessMidnight(t *testing.T) {
	cases := map[time.Time]string{
		time.Date(2024, 8, 12, 15, 33, 22, 0, time.Local): "12.08.2024 15:33:22",
		time.Date(2024, 8, 12, 0, 0, 1, 0, time.Local):    "12.08.2024 00:00:01",
		time.Date(2024, 8, 12, 0, 0, 0, 0, time.Local):    "12.08.2024",
	}
	for in, want := range cases {
		if got := FormatDisplayDate(in); got != want {
			t.Errorf("FormatDisplayDate(%v) = %q, ждали %q", in, got, want)
		}
	}
}
