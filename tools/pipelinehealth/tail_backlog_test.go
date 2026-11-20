package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailBacklogDistinguishesCurrentAndUnverifiedCounts(t *testing.T) {
	now := time.Date(2026, time.November, 21, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "tail.json")
	for _, tt := range []struct {
		name        string
		repository  string
		minutes     int
		pending     int
		recheck     int
		unavailable int
		want        string
	}{
		{"current", "ivanarama/onebase", 1, 0, 0, 0, "complete"},
		{"stale", "ivanarama/onebase", 10, 0, 0, 0, "stale"},
		{"pending proof", "ivanarama/onebase", 1, 2, 0, 0, "incomplete"},
		{"overdue proof", "ivanarama/onebase", 1, 0, 1, 0, "incomplete"},
		{"unavailable remote proof", "ivanarama/onebase", 1, 0, 0, 1, "unavailable"},
		{"foreign", "ivanarama/other", 1, 0, 0, 0, "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			timeText := now.Add(-time.Duration(tt.minutes) * time.Minute).Format(time.RFC3339)
			data := fmt.Sprintf(`{"repository":%q,"base":"main","discovery":{"state":"complete",
				"discovery_complete_through":%q,"last_complete_at":%q,"pending_intervals":0},
				"counts":{"ready":0,"ambiguous-create":1,"pending-proof":%d},"completions":{},"recheck_due":%d,
				"proof_unavailable_count":%d}`,
				tt.repository, timeText, timeText, tt.pending, tt.recheck, tt.unavailable)
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			got := readTailBacklog(path, "ivanarama/onebase", now)
			if got.State != tt.want {
				t.Fatalf("state = %q, want %q", got.State, tt.want)
			}
			_, hasZero := got.Counts["ready"]
			if hasZero != (tt.want == "complete") {
				t.Fatalf("unexpected zero count evidence: %v", got.Counts)
			}
			if tt.repository == "ivanarama/onebase" && got.Counts["ambiguous-create"] != 1 {
				t.Fatalf("positive count was lost: %v", got.Counts)
			}
		})
	}
}
