package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestNumberPredicateUsesDeclaredScaleSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	entity := &metadata.Entity{
		Name: "AuditNumberPredicate",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{
			Name: "Amount", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2,
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Amount": "100"}, entity); err != nil {
		t.Fatal(err)
	}

	for _, value := range []any{float64(100), "100", "100.0", "100.00"} {
		condition, args, _, err := PredicateSQL(db.Dialect(), entity,
			&Predicate{Field: "Amount", Op: "eq", Value: value}, 1)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", metadata.TableName(entity.Name), condition)
		if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("numeric predicate value %T(%v) matched %d rows; want 1", value, value, count)
		}
	}

	for _, test := range []struct {
		name      string
		predicate Predicate
		want      int
	}{
		{name: "ne", predicate: Predicate{Field: "Amount", Op: "ne", Value: float64(100)}, want: 0},
		{name: "in", predicate: Predicate{Field: "Amount", Op: "in", Values: []any{"100", "200"}}, want: 1},
		{name: "not in", predicate: Predicate{Field: "Amount", Op: "not_in", Values: []any{"100", "200"}}, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			condition, args, _, err := PredicateSQL(db.Dialect(), entity, &test.predicate, 1)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", metadata.TableName(entity.Name), condition)
			if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.want {
				t.Fatalf("matched %d rows; want %d", count, test.want)
			}
		})
	}

	if _, _, _, err := PredicateSQL(db.Dialect(), entity,
		&Predicate{Field: "Amount", Op: "eq", Value: "not-a-number"}, 1); err == nil {
		t.Fatal("invalid numeric predicate was accepted")
	}
}

func TestWriteInfoMovementsRollsBackInvalidNumberSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:       "AuditInfoMovementAtomic",
		Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeNumber}},
		Resources:  []metadata.Field{{Name: "Amount", Type: metadata.FieldTypeNumber}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	recorder := uuid.New()
	if err := db.WriteInfoMovements(ctx, ir.Name, "Doc", recorder,
		[]map[string]any{{"Key": 1, "Amount": 10}}, ir, nil); err != nil {
		t.Fatal(err)
	}
	err = db.WriteInfoMovements(ctx, ir.Name, "Doc", recorder, []map[string]any{
		{"Key": 2, "Amount": 20},
		{"Key": 3, "Amount": "not-a-number"},
	}, ir, nil)
	if err == nil {
		t.Fatal("invalid number unexpectedly succeeded")
	}

	assertInfoMovement := func(wantKey, wantAmount string) {
		t.Helper()
		table := metadata.InfoRegTableName(ir.Name)
		var key, amount string
		if err := db.QueryRow(ctx, fmt.Sprintf("SELECT key, amount FROM %s WHERE recorder = ?", table), recorder.String()).Scan(&key, &amount); err != nil {
			t.Fatalf("read preserved movement: %v", err)
		}
		if key != wantKey || amount != wantAmount {
			t.Fatalf("movement key=%q amount=%q; want %s/%s", key, amount, wantKey, wantAmount)
		}
	}
	assertInfoMovement("1", "10")

	// The public replacement owns a savepoint even inside a caller transaction.
	// A caller that handles the error and commits unrelated work must not commit
	// the DELETE and the prefix of the replacement batch.
	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WriteInfoMovements(txCtx, ir.Name, "Doc", recorder, []map[string]any{
		{"Key": 2, "Amount": 20},
		{"Key": 3, "Amount": "not-a-number"},
	}, ir, nil)
	if err == nil {
		_ = tx.Rollback(txCtx)
		t.Fatal("invalid number in borrowed transaction unexpectedly succeeded")
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	assertInfoMovement("1", "10")

	// A driver error after a valid prefix has the same all-or-nothing contract.
	table := metadata.InfoRegTableName(ir.Name)
	triggerSQL := fmt.Sprintf(`CREATE TRIGGER reject_audit_info_movement
		BEFORE INSERT ON %s WHEN NEW.key = '3'
		BEGIN SELECT RAISE(ABORT, 'forced insert failure'); END`, table)
	if _, err := db.Exec(ctx, triggerSQL); err != nil {
		t.Fatal(err)
	}
	err = db.WriteInfoMovements(ctx, ir.Name, "Doc", recorder, []map[string]any{
		{"Key": 2, "Amount": 20},
		{"Key": 3, "Amount": 30},
	}, ir, nil)
	if err == nil {
		t.Fatal("forced database error unexpectedly succeeded")
	}
	assertInfoMovement("1", "10")
}

func TestWriteInfoMovementsRejectsEquivalentLegacyNumberKeySQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "legacy-movement.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:       "AuditInfoMovementLegacy",
		Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeNumber}},
		Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (key, value, updated_at) VALUES (?, ?, ?)", table),
		"4.00", "legacy", time.Now()); err != nil {
		t.Fatal(err)
	}

	err = db.WriteInfoMovements(ctx, ir.Name, "Doc", uuid.New(),
		[]map[string]any{{"Key": "4.0", "Value": "new"}}, ir, nil)
	if err == nil {
		t.Fatal("equivalent legacy sibling unexpectedly allowed")
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count=%d, want untouched legacy row", count)
	}
}

func TestWriteInfoMovementsConvergesEquivalentNumberKeysSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "batch-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name: "AuditInfoMovementBatchKeys",
		Dimensions: []metadata.Field{{
			Name: "Key", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2,
		}},
		Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	recorder := uuid.New()
	if err := db.WriteInfoMovements(ctx, ir.Name, "Doc", recorder, []map[string]any{
		{"Key": "7", "Value": "first"},
		{"Key": "7.0", "Value": "second"},
		{"Key": float64(7), "Value": "last"},
	}, ir, nil); err != nil {
		t.Fatal(err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	var count int
	var key, value string
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*), MIN(key), MIN(value) FROM %s", table)).Scan(&count, &key, &value); err != nil {
		t.Fatal(err)
	}
	if count != 1 || key != "7.00" || value != "last" {
		t.Fatalf("batch converged to count=%d key=%q value=%q; want 1/7.00/last", count, key, value)
	}
}
