package storage_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// These legacy Recorder registers have neither a PK nor an accessible SQLite
// rowid alias. Identical business rows must survive as separate physical rows.
func TestInfoRegEditRevisionShadowedKeylessMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, tc := range []struct {
			name  string
			n     int
			mixed bool
		}{{"Empty", 0, false}, {"Two", 2, false}, {"Large", 515, false}, {"Mixed", 6, true}} {
			t.Run(tc.name, func(t *testing.T) {
				ir, table := shadowedKeylessFixture(t, db, tc.name, tc.n, tc.mixed)
				before := shadowedKeylessSnapshot(t, db, table, tc.mixed)
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				first := shadowedKeylessSnapshot(t, db, table, true)
				checkShadowedKeylessRevisions(t, before, first, true)
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, first) {
					t.Fatalf("repeat migration changed rows: %v -> %v", first, got)
				}
				if _, err := db.InfoRegReadForEdit(ctx, ir, storage.InfoRegEditKey{}, nil); err == nil {
					t.Fatal("Recorder/keyless register became manually editable")
				}
				if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
					return db.InvalidateInfoRegEditRevisions(txCtx, ir)
				}); err != nil {
					t.Fatal(err)
				}
				current := shadowedKeylessSnapshot(t, db, table, true)
				checkShadowedKeylessRevisions(t, first, current, false)
				// A caller can roll back the maintenance write and invalidation
				// together. Cancel after actual writes, not before entering tx.
				for _, cancelScope := range []bool{false, true} {
					stop := errors.New("rollback maintenance")
					if cancelScope {
						stop = context.Canceled
					}
					var canceled bool
					if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
						child, cancel := context.WithCancel(txCtx)
						defer cancel()
						if _, err := db.Exec(child, "UPDATE "+table+" SET oid = 'provisional'"); err != nil {
							return err
						}
						if err := db.InvalidateInfoRegEditRevisions(child, ir); err != nil {
							return err
						}
						if cancelScope {
							cancel()
							canceled = child.Err() != nil
							return child.Err()
						}
						return stop
					}); !errors.Is(err, stop) || (cancelScope && !canceled) {
						t.Fatalf("rollback/cancellation: %v", err)
					}
					if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, current) {
						t.Fatal("rollback/cancellation changed business data or revisions")
					}
				}
			})
		}
		// Corruption is checked before generating any UUID or updating any row;
		// even DDL performed earlier by migrate must be rolled back.
		t.Run("Corrupt", func(t *testing.T) {
			ir, table := shadowedKeylessFixture(t, db, "Corrupt", 6, true)
			if _, err := db.Exec(ctx, "UPDATE "+table+" SET _edit_revision = 'corrupt' WHERE _edit_revision = ''"); err != nil {
				t.Fatal(err)
			}
			before := shadowedKeylessSnapshot(t, db, table, true)
			ir.Resources = append(ir.Resources, metadata.Field{Name: "Added", Type: "string"})
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err == nil {
				t.Fatal("corruption accepted")
			}
			if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, before) {
				t.Fatal("corruption changed rows")
			}
			for _, col := range []string{"added", "updated_at"} {
				if exists, err := db.Dialect().ColumnExists(ctx, db, table, col); err != nil || exists {
					t.Fatalf("DDL not rolled back: %s %t %v", col, exists, err)
				}
			}
			if err := db.WithTxScope(ctx, func(txCtx context.Context) error { return db.InvalidateInfoRegEditRevisions(txCtx, ir) }); err == nil {
				t.Fatal("invalidation accepted corruption")
			}
			if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, before) {
				t.Fatal("invalidation revived corruption")
			}
		})
		t.Run("SQLFailure", func(t *testing.T) {
			ir, table := shadowedKeylessFixture(t, db, "SQLFailure", 6, true)
			before := shadowedKeylessSnapshot(t, db, table, true)
			var statements []string
			if db.IsSQLite() {
				statements = []string{"CREATE TRIGGER revision_keyless_fail BEFORE UPDATE OF _edit_revision ON " + table + " BEGIN SELECT RAISE(ABORT, 'injected revision update failure'); END"}
			} else {
				statements = []string{
					"CREATE FUNCTION revision_keyless_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected revision update failure'; END $$",
					"CREATE TRIGGER revision_keyless_fail BEFORE UPDATE OF _edit_revision ON " + table + " FOR EACH ROW EXECUTE FUNCTION revision_keyless_fail()",
				}
			}
			for _, statement := range statements {
				if _, err := db.Exec(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err == nil || !strings.Contains(err.Error(), "injected revision update failure") {
				t.Fatalf("SQL fault not exercised: %v", err)
			}
			if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, before) {
				t.Fatal("SQL failure left partial backfill")
			}
			if exists, err := db.Dialect().ColumnExists(ctx, db, table, "updated_at"); err != nil || exists {
				t.Fatalf("SQL failure retained DDL: %t %v", exists, err)
			}
		})
		// Inject failure only during the public operation. No parallel subtests:
		// uuid.SetRand is process-global and always restored before leaving scope.
		for _, cancelDuringUUID := range []bool{false, true} {
			t.Run(fmt.Sprintf("RandomFailureCancel%t", cancelDuringUUID), func(t *testing.T) {
				ir, table := shadowedKeylessFixture(t, db, fmt.Sprintf("Failure%t", cancelDuringUUID), 6, true)
				before := shadowedKeylessSnapshot(t, db, table, true)
				child, cancel := context.WithCancel(ctx)
				defer cancel()
				fault := &revisionRandomFault{after: 1}
				if cancelDuringUUID {
					fault.cancel = cancel
				}
				err := func() error {
					uuid.SetRand(fault)
					defer uuid.SetRand(nil)
					return db.MigrateInfoRegisters(child, []*metadata.InfoRegister{ir})
				}()
				if err == nil || fault.reads < 2 {
					t.Fatalf("UUID failure not exercised: reads=%d err=%v", fault.reads, err)
				}
				if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, before) {
					t.Fatal("UUID failure/cancellation left partial backfill")
				}
				if exists, err := db.Dialect().ColumnExists(ctx, db, table, "updated_at"); err != nil || exists {
					t.Fatalf("failed backfill retained DDL: %t %v", exists, err)
				}
				// Recover and then exercise the same failure during invalidation.
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				current := shadowedKeylessSnapshot(t, db, table, true)
				fault = &revisionRandomFault{after: 1}
				err = func() error {
					uuid.SetRand(fault)
					defer uuid.SetRand(nil)
					return db.WithTxScope(ctx, func(txCtx context.Context) error {
						if _, err := db.Exec(txCtx, "UPDATE "+table+" SET oid = 'provisional'"); err != nil {
							return err
						}
						return db.InvalidateInfoRegEditRevisions(txCtx, ir)
					})
				}()
				if err == nil || fault.reads < 2 {
					t.Fatalf("invalidation failure not exercised: %v", err)
				}
				if got := shadowedKeylessSnapshot(t, db, table, true); !reflect.DeepEqual(got, current) {
					t.Fatal("failed invalidation left partial versions or business writes")
				}
			})
		}
	})
}

type revisionRandomFault struct {
	after, reads int
	cancel       context.CancelFunc
}

func (r *revisionRandomFault) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > r.after {
		if r.cancel != nil {
			r.cancel()
			return 0, context.Canceled
		}
		return 0, errors.New("injected UUID entropy failure")
	}
	return rand.Read(p)
}

func shadowedKeylessFixture(t *testing.T, db *storage.DB, suffix string, n int, revisions bool) (*metadata.InfoRegister, string) {
	t.Helper()
	ctx := context.Background()
	ir := &metadata.InfoRegister{Name: "RevisionKeyless" + suffix, Recorder: true, Resources: []metadata.Field{{Name: "rowid", Type: "string"}, {Name: "_rowid_", Type: "string"}, {Name: "oid", Type: "string"}}}
	if err := metadata.ValidateIdentifiers(nil, nil, []*metadata.InfoRegister{ir}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	ddl := "CREATE TABLE " + table + " (rowid TEXT, _rowid_ TEXT, oid TEXT, recorder " + db.Dialect().TypeUUID() + ", recorder_type TEXT, period " + db.Dialect().TypeTimestamp()
	if revisions {
		ddl += ", _edit_revision TEXT"
	}
	if _, err := db.Exec(ctx, ddl+")"); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
		for i := 0; i < n; i++ {
			cols, vals := "rowid,_rowid_,oid,recorder,recorder_type,period", "'R','S','T','12345678-1234-4234-8234-123456789abc','Doc','2026-10-19 13:22:33+00:00'"
			if revisions {
				cols += ",_edit_revision"
				switch i % 3 {
				case 0:
					vals += ",NULL"
				case 1:
					vals += ",''"
				default:
					vals += ",'" + uuid.NewString() + "'"
				}
			}
			if _, err := db.Exec(txCtx, "INSERT INTO "+table+" ("+cols+") VALUES ("+vals+")"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ir, table
}

type shadowedKeylessRow struct {
	Business [6]string
	Revision string
}

func shadowedKeylessSnapshot(t *testing.T, db *storage.DB, table string, revisions bool) []shadowedKeylessRow {
	t.Helper()
	rev := "'<NULL>'"
	if revisions {
		rev = "COALESCE(_edit_revision, '<NULL>')"
	}
	rows, err := db.Query(context.Background(), "SELECT rowid,_rowid_,oid,CAST(recorder AS TEXT),recorder_type,CAST(period AS TEXT),"+rev+" FROM "+table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []shadowedKeylessRow{}
	for rows.Next() {
		var r shadowedKeylessRow
		if err := rows.Scan(&r.Business[0], &r.Business[1], &r.Business[2], &r.Business[3], &r.Business[4], &r.Business[5], &r.Revision); err != nil {
			t.Fatal(err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Revision < result[j].Revision })
	return result
}

func checkShadowedKeylessRevisions(t *testing.T, before, after []shadowedKeylessRow, preserve bool) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("row count changed: %d -> %d", len(before), len(after))
	}
	old, seen := map[string]bool{}, map[string]bool{}
	for _, r := range before {
		if r.Revision != "<NULL>" && r.Revision != "" {
			old[r.Revision] = true
		}
	}
	for _, r := range after {
		id, err := uuid.Parse(r.Revision)
		if err != nil || id == uuid.Nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || seen[r.Revision] {
			t.Fatalf("invalid/reused UUID: %q", r.Revision)
		}
		seen[r.Revision] = true
		if r.Business != before[0].Business {
			t.Fatalf("business row changed: %v", r.Business)
		}
		if !preserve && old[r.Revision] {
			t.Fatal("invalidation retained an old revision")
		}
	}
	if preserve {
		for rev := range old {
			if !seen[rev] {
				t.Fatal("backfill replaced a valid version")
			}
		}
	}
}
