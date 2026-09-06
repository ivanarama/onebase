package backup

import (
	"context"
	"errors"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// The writable SQLite preflight is only a way to make a hot WAL readable.
// The authoritative opened-database guard must remain fail-closed on both
// dialects after that preflight (issue #1198).
func TestCheckNoPendingRestoreRejectsMarkerMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		query := `INSERT INTO _settings(key,value) VALUES (` +
			db.Dialect().Placeholder(1) + `,` + db.Dialect().Placeholder(2) + `)`
		if _, err := db.Exec(ctx, query, restoreIntentKey, `{}`); err != nil {
			t.Fatalf("insert restore marker: %v", err)
		}

		if err := CheckNoPendingRestore(ctx, db); !errors.Is(err, ErrRestoreRecoveryRequired) {
			t.Fatalf("CheckNoPendingRestore error = %v, want ErrRestoreRecoveryRequired", err)
		}
	})
}
