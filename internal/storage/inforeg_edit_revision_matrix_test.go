package storage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func revisionRegister(name string) *metadata.InfoRegister {
	return &metadata.InfoRegister{Name: name, Dimensions: []metadata.Field{{ID: "key", Name: "Key", Type: "string"}}, Resources: []metadata.Field{{ID: "value", Name: "Value", Type: "string"}}}
}
func readRevision(t *testing.T, db *storage.DB, ir *metadata.InfoRegister, key storage.InfoRegEditKey) *storage.InfoRegEditRecord {
	t.Helper()
	record, err := db.InfoRegReadForEdit(context.Background(), ir, key, nil)
	if err != nil || record == nil {
		t.Fatalf("read for edit: %v, %v", record, err)
	}
	if id, err := uuid.Parse(record.Revision); err != nil || id == uuid.Nil {
		t.Fatalf("revision: %q", record.Revision)
	}
	if _, ok := record.Row[storage.InfoRegEditRevisionColumn]; ok {
		t.Fatal("revision in business row")
	}
	return record
}

func TestInfoRegEditRevisionWritersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionWriters")
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": ""}}
		resources := map[string]any{"Value": "A", storage.InfoRegEditRevisionColumn: uuid.Nil.String()}
		write := func() {
			t.Helper()
			if err := db.InfoRegSet(ctx, ir, key.Dimensions, resources, nil); err != nil {
				t.Fatal(err)
			}
		}
		write()
		first := readRevision(t, db, ir, key)
		write()
		second := readRevision(t, db, ir, key)
		if first.Revision == second.Revision {
			t.Fatal("no-op write retained revision")
		}
		changed, err := db.InfoRegSetIfExistingAllowed(ctx, ir, key.Dimensions, resources, nil, &storage.Predicate{Field: "Value", Op: "eq", Value: "denied"})
		if err != nil || changed {
			t.Fatalf("denied write: %v %v", changed, err)
		}
		if got := readRevision(t, db, ir, key); got.Revision != second.Revision {
			t.Fatal("denied write changed revision")
		}
		changed, err = db.InfoRegSetIfExistingAllowed(ctx, ir, key.Dimensions, resources, nil, &storage.Predicate{Field: "Value", Op: "eq", Value: "A"})
		if err != nil || !changed {
			t.Fatalf("allowed write: %v %v", changed, err)
		}
		third := readRevision(t, db, ir, key)
		if third.Revision == second.Revision {
			t.Fatal("conditional writer retained revision")
		}
		if err := db.InfoRegApplyExchange(ctx, ir, key.Dimensions, resources, nil, false); err != nil {
			t.Fatal(err)
		}
		fourth := readRevision(t, db, ir, key)
		if fourth.Revision == third.Revision {
			t.Fatal("exchange retained revision")
		}
		if err := db.InfoRegDelete(ctx, ir, key.Dimensions, nil); err != nil {
			t.Fatal(err)
		}
		write()
		fifth := readRevision(t, db, ir, key)
		if fifth.Revision == fourth.Revision {
			t.Fatal("delete/reinsert revived revision")
		}
		resources["Value"] = "B"
		write()
		resources["Value"] = "A"
		write()
		sixth := readRevision(t, db, ir, key)
		if sixth.Revision == fifth.Revision {
			t.Fatal("A-B-A revived revision")
		}
		sentinel := errors.New("rollback")
		err = db.WithTxScope(ctx, func(txCtx context.Context) error {
			if err := db.InfoRegSet(txCtx, ir, key.Dimensions, resources, nil); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		if got := readRevision(t, db, ir, key); got.Revision != sixth.Revision {
			t.Fatal("rollback changed revision")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := db.InfoRegSet(canceled, ir, key.Dimensions, resources, nil); err == nil {
			t.Fatal("canceled write succeeded")
		}
		if got := readRevision(t, db, ir, key); got.Revision != sixth.Revision {
			t.Fatal("canceled write changed revision")
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if got := readRevision(t, db, ir, key); got.Revision != sixth.Revision {
			t.Fatal("repeat migrate invalidated revision")
		}
		for _, read := range []func() (map[string]any, error){
			func() (map[string]any, error) { return db.InfoRegGet(ctx, ir, key.Dimensions) },
			func() (map[string]any, error) { return db.InfoRegGetExact(ctx, ir, key.Dimensions, nil) },
		} {
			row, err := read()
			if err != nil || len(row) != 2 {
				t.Fatalf("public projection %v %v", row, err)
			}
		}
		page, err := db.InfoRegPage(ctx, ir, storage.RegFilter{}, 10, 0)
		if err != nil || len(page) != 1 || len(page[0]) != 2 {
			t.Fatalf("public page: %v %v", page, err)
		}
	})
}

func TestInfoRegEditRevisionMigrateBackfillAndRebuildMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionMigration")
		table := metadata.InfoRegTableName(ir.Name)
		// A legacy table has no revision or period. Public migration must backfill
		// before the SQLite PK rebuild and keep both empty and nonempty keys intact.
		if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"", "second"} {
			if _, err := db.Exec(ctx, "INSERT INTO "+table+" (key,value) VALUES ("+db.Dialect().Placeholder(1)+", 'A')", key); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		first := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": ""}})
		second := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "second"}})
		if first.Revision == second.Revision {
			t.Fatal("backfill reused UUID")
		}
		// Adding a key field forces PK rebuild on SQLite, ALTER PK on PostgreSQL.
		ir.Dimensions = append(ir.Dimensions, metadata.Field{ID: "extra", Name: "Extra", Type: "string"})
		if _, err := db.Exec(ctx, "ALTER TABLE "+table+" ADD COLUMN extra TEXT NOT NULL DEFAULT 'E'"); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		got := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "", "Extra": "E"}})
		if got.Revision != first.Revision || got.Row["Value"] != "A" {
			t.Fatal("rebuild lost row/revision")
		}
		// A failed public migration must not partially backfill or add the column.
		bad := revisionRegister("RevisionRollback")
		badTable := metadata.InfoRegTableName(bad.Name)
		if _, err := db.Exec(ctx, "CREATE TABLE "+badTable+" (key TEXT, value TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO "+badTable+" (key,value) VALUES ('same','A'),('same','B')"); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{bad}); err == nil {
			t.Fatal("duplicate PK migration succeeded")
		}
		exists, err := db.Dialect().ColumnExists(ctx, db, badTable, storage.InfoRegEditRevisionColumn)
		if err != nil || exists {
			t.Fatalf("failed migration left revision column: %v %v", exists, err)
		}
		var count int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+badTable).Scan(&count); err != nil || count != 2 {
			t.Fatalf("rollback data %d %v", count, err)
		}
	})
}

func TestInfoRegEditExactReadGateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionExact")
		ir.Periodic = true
		ir.Dimensions = append(ir.Dimensions, metadata.Field{Name: "Flag", Type: metadata.FieldTypeBool}, metadata.Field{Name: "Amount", Type: "number", Length: 15, Scale: 2}, metadata.Field{Name: "Ref", Type: "reference", RefEntity: "Catalog"}, metadata.Field{Name: "Date", Type: "date"})
		p := time.Date(2026, 10, 19, 13, 22, 33, 123000000, time.UTC)
		// Native SQLite writers store seconds; use the physical stored key.
		// Fractional restored periods are covered by the universal-backup matrix.
		if db.IsSQLite() {
			p = p.Truncate(time.Second)
		}
		ref := uuid.New()
		key := storage.InfoRegEditKey{Period: &p, Dimensions: map[string]any{"Key": "", "Flag": true, "Amount": "1.00", "Ref": ref, "Date": p}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "hidden"}, key.Period); err != nil {
			t.Fatal(err)
		}
		original := readRevision(t, db, ir, key)
		// The returned physical locator round trips without float/date formatting.
		again := readRevision(t, db, ir, original.Key)
		if again.Revision != original.Revision {
			t.Fatal("physical key changed")
		}
		for _, filter := range []*storage.Predicate{{Field: "Value", Op: "eq", Value: "visible"}, {Field: "Key", Op: "eq", Value: "sibling"}} {
			row, err := db.InfoRegReadForEdit(ctx, ir, key, filter)
			if err != nil || row != nil {
				t.Fatalf("RLS leaked row %v %v", row, err)
			}
		}
		badKey := storage.InfoRegEditKey{Dimensions: key.Dimensions}
		if _, err := db.InfoRegReadForEdit(ctx, ir, badKey, nil); err == nil {
			t.Fatal("missing period allowed")
		}
		for _, dims := range []map[string]any{{"Key": ""}, {"Key": nil, "Flag": true, "Amount": "1", "Ref": ref, "Date": p}} {
			if _, err := db.InfoRegReadForEdit(ctx, ir, storage.InfoRegEditKey{Dimensions: dims, Period: &p}, nil); err == nil {
				t.Fatal("partial/null key allowed")
			}
		}
		q := p.Add(time.Second)
		missing := key
		missing.Period = &q
		if row, err := db.InfoRegReadForEdit(ctx, ir, missing, nil); err != nil || row != nil {
			t.Fatalf("wrong period matched: %v %v", row, err)
		}
		owner := uuid.New()
		table := metadata.InfoRegTableName(ir.Name)
		if _, err := db.Exec(ctx, "UPDATE "+table+" SET recorder = "+db.Dialect().Placeholder(1), owner.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.InfoRegReadForEdit(ctx, ir, key, nil); err == nil {
			t.Fatal("physical recorder row editable")
		}
		ir.Recorder = true
		if _, err := db.InfoRegReadForEdit(ctx, ir, key, nil); err == nil {
			t.Fatal("recorder metadata editable")
		}
	})
}

func TestInfoRegEditRevisionReservedFieldMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionCollision")
		ir.Resources = append(ir.Resources, metadata.Field{Name: "_EDIT_REVISION", Type: "string"})
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err == nil {
			t.Fatal("reserved field accepted")
		}
		ir.Resources = ir.Resources[:1]
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "X"}}
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "A"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "UPDATE "+metadata.InfoRegTableName(ir.Name)+" SET _edit_revision = 'corrupt'"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.InfoRegReadForEdit(ctx, ir, key, nil); err == nil {
			t.Fatal("corrupt version editable")
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err == nil {
			t.Fatal("corruption silently backfilled")
		}
	})
}

func TestInfoRegEditRevisionPostingMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionPosting")
		ir.Recorder = true
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		owner := uuid.New()
		row := map[string]any{"Key": "X", "Value": "A"}
		write := func() {
			t.Helper()
			if err := db.WriteInfoMovements(ctx, ir.Name, "Doc", owner, []map[string]any{row}, ir, nil); err != nil {
				t.Fatal(err)
			}
		}
		revision := func() string {
			t.Helper()
			var s string
			if err := db.QueryRow(ctx, fmt.Sprintf("SELECT _edit_revision FROM %s", metadata.InfoRegTableName(ir.Name))).Scan(&s); err != nil {
				t.Fatal(err)
			}
			return s
		}
		write()
		first := revision()
		write()
		if revision() == first {
			t.Fatal("reposting reused revision")
		}
		old := revision()
		if err := db.WriteInfoMovements(ctx, ir.Name, "Doc", uuid.New(), []map[string]any{row}, ir, nil); err == nil {
			t.Fatal("foreign owner accepted")
		}
		if revision() != old {
			t.Fatal("failed posting changed revision")
		}
	})
}

func TestInfoRegEditRevisionLegacyNumberMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionLegacyNumber")
		ir.Dimensions[0].Type = "number"
		ir.Dimensions[0].Length = 15
		ir.Dimensions[0].Scale = 2
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		table := metadata.InfoRegTableName(ir.Name)
		// Legacy SQLite keeps both lexical keys. PostgreSQL folds their numeric
		// identity, so the single scale-preserving physical row is the counterpart.
		if _, err := db.Exec(ctx, "INSERT INTO "+table+" (key,value) VALUES ('1.00','first')"); err != nil {
			t.Fatal(err)
		}
		if db.IsSQLite() {
			if _, err := db.Exec(ctx, "INSERT INTO "+table+" (key,value) VALUES ('1','second')"); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		r := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "1.00"}})
		if r.Row["Value"] != "first" || r.Key.Dimensions["Key"] != "1.00" {
			t.Fatalf("physical spelling lost: %+v", r)
		}
		if db.IsSQLite() {
			other := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "1"}})
			if other.Row["Value"] != "second" || other.Revision == r.Revision {
				t.Fatalf("wrong lexical row: %+v", other)
			}
		}
	})
}

func TestInfoRegEditRevisionBackfillKeylessMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{Name: "RevisionKeyless", Resources: []metadata.Field{{Name: "Value", Type: "string"}}}
		table := metadata.InfoRegTableName(ir.Name)
		if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (value TEXT)"); err != nil {
			t.Fatal(err)
		}
		if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
			for i := 0; i < 513; i++ {
				if _, err := db.Exec(txCtx, "INSERT INTO "+table+" (value) VALUES ('same')"); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		var count, distinct int
		if err := db.QueryRow(ctx, "SELECT COUNT(_edit_revision), COUNT(DISTINCT _edit_revision) FROM "+table).Scan(&count, &distinct); err != nil || count != 513 || distinct != 513 {
			t.Fatalf("backfill %d %d %v", count, distinct, err)
		}
		if _, err := db.InfoRegReadForEdit(ctx, ir, storage.InfoRegEditKey{}, nil); err == nil {
			t.Fatal("keyless edit accepted")
		}
	})
}

func TestInfoRegEditReadRejectsDeferredKeyMigrationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("RevisionDeferredKey")
		ir.Dimensions = append(ir.Dimensions, metadata.Field{ID: "oldkey", Name: "OldKey", Type: "string"})
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "X", "OldKey": "only-row"}, map[string]any{"Value": "A"}, nil); err != nil {
			t.Fatal(err)
		}
		narrowed := *ir
		narrowed.Dimensions = ir.Dimensions[:1]
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{&narrowed}); err != nil {
			t.Fatal(err)
		}
		// One current match is insufficient: the caller lacks part of its physical
		// identity, and another sibling could be inserted at any later time.
		if _, err := db.InfoRegReadForEdit(ctx, &narrowed, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "X"}}, nil); err == nil {
			t.Fatal("incomplete physical key accepted")
		}
	})
}

func TestInfoRegEditRevisionShadowedLocatorsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
				ir := revisionRegister(fmt.Sprintf("ShadowedLocators%t", legacy))
				ir.Resources = []metadata.Field{{Name: "rowid", Type: "string"}, {Name: "_rowid_", Type: "string"}, {Name: "oid", Type: "string"}}
				table := metadata.InfoRegTableName(ir.Name)
				if legacy {
					if _, err := db.Exec(ctx, "CREATE TABLE "+table+" (key TEXT PRIMARY KEY, rowid TEXT, _rowid_ TEXT, oid TEXT)"); err != nil {
						t.Fatal(err)
					}
					// More than one backfill batch; repeated resource values cannot locate rows.
					for i := 0; i < 515; i++ {
						if _, err := db.Exec(ctx, "INSERT INTO "+table+" (key,rowid,_rowid_,oid) VALUES ("+db.Dialect().Placeholder(1)+",'R','S','T')", fmt.Sprint(i)); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "0"}}
				if !legacy {
					if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"rowid": "R", "_rowid_": "S", "oid": "T"}, nil); err != nil {
						t.Fatal(err)
					}
				}
				first := readRevision(t, db, ir, key)
				if first.Row["rowid"] != "R" || first.Row["_rowid_"] != "S" || first.Row["oid"] != "T" {
					t.Fatalf("business fields changed: %v", first)
				}
				if legacy {
					seen := map[string]bool{}
					for i := 0; i < 515; i++ {
						row := readRevision(t, db, ir, storage.InfoRegEditKey{Dimensions: map[string]any{"Key": fmt.Sprint(i)}})
						if seen[row.Revision] {
							t.Fatal("backfill reused revision")
						}
						seen[row.Revision] = true
					}
				}
				if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
					t.Fatal(err)
				}
				if got := readRevision(t, db, ir, key); got.Revision != first.Revision {
					t.Fatal("repeat migration changed revision")
				}
				if err := db.WithTxScope(ctx, func(txCtx context.Context) error { return db.InvalidateInfoRegEditRevisions(txCtx, ir) }); err != nil {
					t.Fatal(err)
				}
				if got := readRevision(t, db, ir, key); got.Revision == first.Revision || got.Row["oid"] != "T" {
					t.Fatal("invalidation lost identity or business data")
				}
			})
		}
	})
}

func TestInfoRegEditPhysicalPeriodSpellingMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("PhysicalPeriodSpelling")
		ir.Periodic = true
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		p := time.Date(2026, 10, 19, 13, 22, 33, 0, time.UTC)
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "A"}, Period: &p}
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "native"}, &p); err != nil {
			t.Fatal(err)
		}
		native := readRevision(t, db, ir, key)
		if !db.IsSQLite() {
			return
		} // PostgreSQL has one physical instant, no TEXT aliases.
		revision := uuid.NewString()
		if _, err := db.Exec(ctx, "INSERT INTO "+metadata.InfoRegTableName(ir.Name)+" (period,key,value,_edit_revision) VALUES (?,'A','restored',?)", p.Format(time.RFC3339Nano), revision); err != nil {
			t.Fatal(err)
		}
		if row, err := db.InfoRegReadForEdit(ctx, ir, key, nil); err == nil || row != nil {
			t.Fatalf("ambiguous typed period selected a row: %v %v", row, err)
		}
		exact := readRevision(t, db, ir, native.Key)
		if exact.Revision != native.Revision || exact.Row["Value"] != "native" {
			t.Fatalf("physical period selected sibling: %v", exact)
		}
		inconsistent := native.Key
		changed := p.Add(time.Second)
		inconsistent.Period = &changed
		if row, err := db.InfoRegReadForEdit(ctx, ir, inconsistent, nil); err == nil || row != nil {
			t.Fatalf("inconsistent physical period accepted: %v %v", row, err)
		}
	})
}
