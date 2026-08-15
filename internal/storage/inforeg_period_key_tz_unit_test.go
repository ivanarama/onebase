package storage

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// The PostgreSQL integration test exercises pgx's time.Local scan behavior.
// Keep a fast, database-independent guard on the two serialization boundaries
// as well, so the UTC call cannot disappear unnoticed on machines without PG.
func TestInfoRegMachineDateKeysUseUTC(t *testing.T) {
	zone := time.FixedZone("UTC+03", 3*60*60)
	local := time.Date(2026, 8, 15, 12, 30, 45, 123000000, zone)
	want := "2026-08-15T09:30:45.123Z"
	field := metadata.Field{Name: "Момент", Type: metadata.FieldTypeDate}

	for _, tc := range []struct {
		name       string
		normalized any
	}{
		{name: "value", normalized: local},
		{name: "pointer", normalized: &local},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := infoRegKeyText(field, tc.normalized, tc.normalized); got != want {
				t.Fatalf("machine dimension key = %q, want %q", got, want)
			}
		})
	}

	rows := infoRegListRows(&metadata.InfoRegister{Periodic: true}, []map[string]any{
		{"period": local},
	})
	if got := rows[0]["period_key"]; got != want {
		t.Fatalf("machine period key = %q, want %q", got, want)
	}

	legacy := local.Format(time.RFC3339Nano)
	parsed, ok := ParseRegPeriod(legacy)
	if !ok || !parsed.Equal(local) {
		t.Fatalf("legacy offset key %q parsed as %v, ok=%v", legacy, parsed, ok)
	}
}
