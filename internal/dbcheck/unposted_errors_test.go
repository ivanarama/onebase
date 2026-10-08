package dbcheck

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Use the same selection and runner as doctor: a failed schema probe must
// appear as a failed check, never as a clean bill of health.
func TestUnpostedMovementsClosedDatabase(t *testing.T) {
	for _, family := range []string{"accumulation", "accounting"} {
		t.Run(family, func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "doctor.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			env := &Env{DB: db}
			if family == "accumulation" {
				env.Registers = []*metadata.Register{{Name: "Stock"}}
				err = db.MigrateRegisters(ctx, env.Registers)
			} else {
				env.AccountRegisters = []*metadata.AccountRegister{{Name: "Ledger"}}
				err = db.MigrateAccountRegisters(ctx, env.AccountRegisters)
			}
			if err != nil {
				t.Fatal(err)
			}
			db.Close()
			checks, unknown := Select([]string{"unposted-movements"})
			if len(unknown) != 0 {
				t.Fatal(unknown)
			}
			res := findResult(t, Run(ctx, env, checks, nil), "unposted-movements")
			if res.Severity != SeverityError || !strings.Contains(res.Error, "database is closed") {
				t.Fatalf("closed database reported as a successful check: %+v", res)
			}
		})
	}
}

func TestUnpostedMovementsSchemaProbeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, family := range []string{"accumulation", "accounting"} {
			t.Run(family, func(t *testing.T) {
				env := &Env{DB: db}
				if family == "accumulation" {
					env.Registers = []*metadata.Register{{Name: "NotMigratedStock"}}
				} else {
					env.AccountRegisters = []*metadata.AccountRegister{{Name: "NotMigratedLedger"}}
				}
				checks, unknown := Select([]string{"unposted-movements"})
				if len(unknown) != 0 {
					t.Fatal(unknown)
				}
				res := findResult(t, Run(context.Background(), env, checks, nil), "unposted-movements")
				if res.Severity != SeverityOK || res.Error != "" {
					t.Fatalf("genuinely absent table should be skipped: %+v", res)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				res = findResult(t, Run(ctx, env, checks, nil), "unposted-movements")
				if res.Severity != SeverityError || res.Error == "" {
					t.Fatalf("failed schema probe reported as a successful check: %+v", res)
				}
			})
		}
	})
}
