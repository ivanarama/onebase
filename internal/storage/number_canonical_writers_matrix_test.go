package storage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// These writers bypass the ordinary entity Upsert path. Keeping them in one
// matrix prevents a future writer from quietly restoring float/string-specific
// SQLite text while PostgreSQL hides the defect behind NUMERIC coercion.
func TestNumberCanonicalBypassWritersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")

		predefined := &metadata.Entity{
			Name: "CanonicalSeed" + suffix,
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Rate", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
			},
			Predefined: []*metadata.PredefinedItem{{
				Name:   "Base",
				Fields: map[string]any{"Rate": float64(12.5)},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{predefined}); err != nil {
			t.Fatalf("Migrate predefined: %v", err)
		}
		predefinedID, err := db.GetPredefinedID(ctx, predefined.Name, "Base")
		if err != nil {
			t.Fatalf("GetPredefinedID: %v", err)
		}
		if got := rawText(t, db, metadata.TableName(predefined.Name), "rate", predefinedID); got != "12.50" {
			t.Fatalf("predefined number=%q, want 12.50", got)
		}

		reg := &metadata.Register{
			Name: "CanonicalReg" + suffix,
			Dimensions: []metadata.Field{
				{Name: "Batch", Type: metadata.FieldTypeNumber, Length: 8, Scale: 2},
			},
			Resources: []metadata.Field{
				{Name: "Quantity", Type: metadata.FieldTypeNumber},
			},
			Attributes: []metadata.Field{
				{Name: "Box", Type: metadata.FieldTypeNumber, Length: 6, Scale: 0},
			},
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("MigrateRegisters: %v", err)
		}
		recorder := uuid.New()
		period := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Doc", recorder, []map[string]any{{
			"Batch": "7.500", "Quantity": float64(100), "Box": "10.6", "ВидДвижения": "Приход",
		}}, reg, &period); err != nil {
			t.Fatalf("WriteMovements: %v", err)
		}
		regTable := metadata.RegisterTableName(reg.Name)
		for column, want := range map[string]string{"batch": "7.50", "quantity": "100", "box": "11"} {
			if got := rawTextWhere(t, db, regTable, column, "recorder", recorder); got != want {
				t.Fatalf("register %s=%q, want %q", column, got, want)
			}
		}

		info := &metadata.InfoRegister{
			Name: "CanonicalInfo" + suffix,
			Dimensions: []metadata.Field{
				{Name: "Rate", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
			},
			Resources: []metadata.Field{
				{Name: "Whole", Type: metadata.FieldTypeNumber, Length: 6, Scale: 0},
			},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{info}); err != nil {
			t.Fatalf("MigrateInfoRegisters: %v", err)
		}
		if err := db.InfoRegSet(ctx, info, map[string]any{"Rate": "3.500"}, map[string]any{"Whole": 9.6}, nil); err != nil {
			t.Fatalf("InfoRegSet: %v", err)
		}
		infoTable := metadata.InfoRegTableName(info.Name)
		if got := rawFirstText(t, db, infoTable, "rate"); got != "3.50" {
			t.Fatalf("info dimension=%q, want 3.50", got)
		}
		if got := rawFirstText(t, db, infoTable, "whole"); got != "10" {
			t.Fatalf("info resource=%q, want 10", got)
		}
		if row, err := db.InfoRegGet(ctx, info, map[string]any{"Rate": float64(3.5)}); err != nil || row == nil {
			t.Fatalf("InfoRegGet through another input type: row=%#v err=%v", row, err)
		}
		if err := db.InfoRegDelete(ctx, info, map[string]any{"Rate": "3.5000"}, nil); err != nil {
			t.Fatalf("InfoRegDelete through non-canonical spelling: %v", err)
		}
		if got := rowCount(t, db, infoTable); got != 0 {
			t.Fatalf("InfoRegDelete left %d rows, want 0", got)
		}

		account := &metadata.AccountRegister{
			Name:     "CanonicalAccount" + suffix,
			Accounts: "Chart",
			Resources: []metadata.Field{
				{Name: "Amount", Type: metadata.FieldTypeNumber, Length: 12, Scale: 2},
			},
			Subconto: []metadata.Field{
				{Name: "Share", Type: metadata.FieldTypeNumber, Length: 8, Scale: 2},
			},
		}
		if err := db.MigrateAccountRegisters(ctx, []*metadata.AccountRegister{account}); err != nil {
			t.Fatalf("MigrateAccountRegisters: %v", err)
		}
		accountRecorder := uuid.New()
		if err := db.WriteAccountMovements(ctx, account.Name, "Doc", accountRecorder, []map[string]any{{
			"счётдт": "41", "счёткт": "60", "Amount": float64(15.5), "Субконто1": "2.5",
		}}, account, &period); err != nil {
			t.Fatalf("WriteAccountMovements: %v", err)
		}
		accountTable := metadata.AccountRegTableName(account.Name)
		if got := rawTextWhere(t, db, accountTable, "amount", "регистратор", accountRecorder); got != "15.50" {
			t.Fatalf("account resource=%q, want 15.50", got)
		}
		if got := rawTextWhere(t, db, accountTable, metadata.SubcontoColumn(1), "регистратор", accountRecorder); got != "2.50" {
			t.Fatalf("account subconto=%q, want 2.50", got)
		}
	})
}

func TestInfoRegLegacyNumericKeysRemainExactSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:       "LegacyNumericKey",
		Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeNumber}},
		Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	insertRawInfoKey := func(key, value string) {
		t.Helper()
		if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (key, value) VALUES (?, ?)", table), key, value); err != nil {
			t.Fatalf("insert legacy key %q: %v", key, err)
		}
	}

	insertRawInfoKey("1.00", "legacy")
	if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "1.00"}, map[string]any{"Value": "updated"}, nil); err != nil {
		t.Fatalf("exact legacy update: %v", err)
	}
	if got := rowCount(t, db, table); got != 1 {
		t.Fatalf("exact legacy update created a duplicate: rows=%d", got)
	}
	if got := rawFirstText(t, db, table, "key"); got != "1.00" {
		t.Fatalf("exact legacy update rewrote key as %q", got)
	}

	// Coexisting historical spellings are possible. Exact deletion must remove
	// only the machine key supplied by the list, never its canonical sibling.
	insertRawInfoKey("1", "canonical sibling")
	if err := db.InfoRegDelete(ctx, ir, map[string]any{"Key": "1.00"}, nil); err != nil {
		t.Fatalf("exact legacy delete: %v", err)
	}
	if got := rawFirstText(t, db, table, "key"); got != "1" {
		t.Fatalf("exact legacy delete removed wrong sibling; survivor=%q", got)
	}

	if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": "2.00"}, map[string]any{"Value": "new"}, nil); err != nil {
		t.Fatalf("canonical new key: %v", err)
	}
	if err := db.InfoRegDelete(ctx, ir, map[string]any{"Key": "2.00"}, nil); err != nil {
		t.Fatalf("delete by original non-canonical input: %v", err)
	}

	insertRawInfoKey("4.00", "legacy conflict")
	before := rowCount(t, db, table)
	err = db.InfoRegSet(ctx, ir, map[string]any{"Key": "4.0"}, map[string]any{"Value": "must not duplicate"}, nil)
	if err == nil || !strings.Contains(err.Error(), "прежнем написании") {
		t.Fatalf("numerically equivalent legacy key: err=%v, want explicit conflict", err)
	}
	if got := rowCount(t, db, table); got != before {
		t.Fatalf("legacy conflict changed row count: got %d, want %d", got, before)
	}

	insertRawInfoKey("5,00", "old comma")
	if err := db.InfoRegDelete(ctx, ir, map[string]any{"Key": "5,00"}, nil); err != nil {
		t.Fatalf("exact invalid legacy spelling is no longer addressable: %v", err)
	}
}

func rawFirstText(t *testing.T, db *storage.DB, table, column string) string {
	t.Helper()
	var got *string
	if err := db.QueryRow(context.Background(), fmt.Sprintf("SELECT CAST(%s AS TEXT) FROM %s ORDER BY 1 LIMIT 1", column, table)).Scan(&got); err != nil {
		t.Fatalf("read %s.%s: %v", table, column, err)
	}
	if got == nil {
		t.Fatalf("%s.%s is NULL", table, column)
	}
	return *got
}

func rowCount(t *testing.T, db *storage.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
