package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

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

func TestInfoRegNumberKeyAcceptedDriverTypesSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "driver-types.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:       "AuditInfoNumberDriverTypes",
		Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeNumber}},
		Resources:  []metadata.Field{{Name: "Marker", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	decimalPointer := decimal.RequireFromString("5.5")
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "string", value: "1.2500", want: "1.25"},
		{name: "bytes", value: []byte("2.500"), want: "2.5"},
		{name: "json number", value: json.Number("3.750"), want: "3.75"},
		{name: "decimal", value: decimal.RequireFromString("4.125"), want: "4.125"},
		{name: "decimal pointer", value: &decimalPointer, want: "5.5"},
		{name: "pg numeric", value: pgtype.Numeric{Int: big.NewInt(625), Exp: -2, Valid: true}, want: "6.25"},
		{name: "float64", value: float64(7.5), want: "7.5"},
		{name: "float32", value: float32(8.5), want: "8.5"},
		{name: "int", value: int(9), want: "9"},
		{name: "int8", value: int8(10), want: "10"},
		{name: "int16", value: int16(11), want: "11"},
		{name: "int32", value: int32(12), want: "12"},
		{name: "int64", value: int64(13), want: "13"},
		{name: "uint", value: uint(14), want: "14"},
		{name: "uint8", value: uint8(15), want: "15"},
		{name: "uint16", value: uint16(16), want: "16"},
		{name: "uint32", value: uint32(17), want: "17"},
		{name: "max uint64", value: uint64(math.MaxUint64), want: "18446744073709551615"},
	}
	table := metadata.InfoRegTableName(ir.Name)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": test.value}, map[string]any{"Marker": test.name}, nil); err != nil {
				t.Fatalf("set: %v", err)
			}
			var physical string
			if err := db.QueryRow(ctx, fmt.Sprintf("SELECT key FROM %s WHERE marker = ?", table), test.name).Scan(&physical); err != nil {
				t.Fatalf("read physical key: %v", err)
			}
			if physical != test.want {
				t.Fatalf("physical key=%q, want %q", physical, test.want)
			}
			row, err := db.InfoRegGet(ctx, ir, map[string]any{"Key": test.value})
			if err != nil || row == nil || row["Marker"] != test.name {
				t.Fatalf("get row=%#v err=%v", row, err)
			}
			if err := db.InfoRegDelete(ctx, ir, map[string]any{"Key": test.value}, nil); err != nil {
				t.Fatalf("delete: %v", err)
			}
			var count int
			if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("delete left %d rows", count)
			}
		})
	}

	// Byte-slice machine keys preserve literal legacy spelling rather than
	// being bound as a SQLite BLOB, which cannot equal a TEXT primary key.
	if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (key, marker) VALUES (?, ?)", table), "5,00", "legacy"); err != nil {
		t.Fatal(err)
	}
	row, err := db.InfoRegGet(ctx, ir, map[string]any{"Key": []byte("5,00")})
	if err != nil || row == nil || row["Marker"] != "legacy" {
		t.Fatalf("legacy byte key row=%#v err=%v", row, err)
	}
	if err := db.InfoRegDelete(ctx, ir, map[string]any{"Key": []byte("5,00")}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestInfoRegNumberKeyInvalidTypedValuesDoNotLeakDriverErrorsSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "invalid-driver-types.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:       "AuditInfoNumberInvalidTypes",
		Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeNumber}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	maxInt32 := int32(1<<31 - 1)
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "unsupported struct", value: struct{}{}},
		{name: "invalid json number", value: json.Number("not-a-number")},
		{name: "float NaN", value: math.NaN()},
		{name: "float infinity", value: math.Inf(1)},
		{name: "pg infinity", value: pgtype.Numeric{Valid: true, InfinityModifier: pgtype.Infinity}},
		{name: "decimal huge exponent", value: decimal.New(1, maxInt32)},
		{name: "string huge exponent", value: "1e2147483647"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := db.InfoRegSet(ctx, ir, map[string]any{"Key": test.value}, nil, nil)
			if err == nil {
				t.Fatal("invalid value unexpectedly succeeded")
			}
			message := strings.ToLower(err.Error())
			if strings.Contains(message, "converting argument") ||
				strings.Contains(message, "driver.value") ||
				strings.Contains(message, "unsupported type") {
				t.Fatalf("driver error leaked before numeric validation: %v", err)
			}
		})
	}
}

func TestInfoRegNumberKeyTypedInputsPreferCanonicalSiblingSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "typed-canonical-sibling.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name: "AuditInfoNumberTypedCanonicalSibling",
		Dimensions: []metadata.Field{{
			Name: "Key", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2,
		}},
		Resources: []metadata.Field{{Name: "Marker", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (key, marker) VALUES (?, ?), (?, ?)", table),
		"1", "legacy", "1.00", "canonical"); err != nil {
		t.Fatal(err)
	}

	decimalPointer := decimal.NewFromInt(1)
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "json number", value: json.Number("1")},
		{name: "decimal", value: decimal.NewFromInt(1)},
		{name: "decimal pointer", value: &decimalPointer},
		{name: "pg numeric", value: pgtype.Numeric{Int: big.NewInt(1), Valid: true}},
		{name: "float", value: float64(1)},
		{name: "int", value: int(1)},
		{name: "uint", value: uint64(1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			row, err := db.InfoRegGet(ctx, ir, map[string]any{"Key": test.value})
			if err != nil || row == nil || row["Marker"] != "canonical" {
				t.Fatalf("typed get selected wrong sibling: row=%#v err=%v", row, err)
			}
		})
	}

	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "string", value: "1"},
		{name: "bytes", value: []byte("1")},
	} {
		t.Run("lexical "+test.name, func(t *testing.T) {
			row, err := db.InfoRegGet(ctx, ir, map[string]any{"Key": test.value})
			if err != nil || row == nil || row["Marker"] != "legacy" {
				t.Fatalf("lexical get lost exact sibling: row=%#v err=%v", row, err)
			}
		})
	}

	if err := db.InfoRegSet(ctx, ir, map[string]any{"Key": int(1)}, map[string]any{"Marker": "typed-update"}, nil); err != nil {
		t.Fatal(err)
	}
	var legacyMarker, canonicalMarker string
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT marker FROM %s WHERE key = ?", table), "1").Scan(&legacyMarker); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT marker FROM %s WHERE key = ?", table), "1.00").Scan(&canonicalMarker); err != nil {
		t.Fatal(err)
	}
	if legacyMarker != "legacy" || canonicalMarker != "typed-update" {
		t.Fatalf("typed update touched wrong sibling: legacy=%q canonical=%q", legacyMarker, canonicalMarker)
	}

	if err := db.InfoRegDelete(ctx, ir, map[string]any{"Key": uint64(1)}, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE key = ?", table), "1.00").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("typed delete left canonical sibling")
	}
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE key = ?", table), "1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("typed delete removed legacy sibling")
	}
}

func TestInfoRegNumberKeyCompositeHybridLookupSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "composite-hybrid-key.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name: "AuditInfoNumberCompositeHybrid",
		Dimensions: []metadata.Field{
			{Name: "Lexical", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
			{Name: "Typed", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
		},
		Resources: []metadata.Field{{Name: "Marker", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	table := metadata.InfoRegTableName(ir.Name)
	if _, err := db.Exec(ctx, fmt.Sprintf("INSERT INTO %s (lexical, typed, marker) VALUES (?, ?, ?)", table),
		"2.0", "3.00", "hybrid"); err != nil {
		t.Fatal(err)
	}

	key := map[string]any{"Lexical": "2.0", "Typed": int(3)}
	row, err := db.InfoRegGet(ctx, ir, key)
	if err != nil || row == nil || row["Marker"] != "hybrid" {
		t.Fatalf("hybrid exact key row=%#v err=%v", row, err)
	}
	if err := db.InfoRegSet(ctx, ir, key, map[string]any{"Marker": "updated"}, nil); err != nil {
		t.Fatal(err)
	}
	var marker string
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT marker FROM %s WHERE lexical = ? AND typed = ?", table),
		"2.0", "3.00").Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != "updated" {
		t.Fatalf("hybrid update marker=%q", marker)
	}
	if err := db.InfoRegDelete(ctx, ir, key, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("hybrid delete left %d rows", count)
	}
}

func TestInfoRegNumberKeyGetLastSeparatesTypedAndLexicalSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "last-key-kind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:     "AuditInfoNumberLastKeyKind",
		Periodic: true,
		Dimensions: []metadata.Field{{
			Name: "Key", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2,
		}},
		Resources: []metadata.Field{{Name: "Marker", Type: metadata.FieldTypeString}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	table := metadata.InfoRegTableName(ir.Name)
	if _, err := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO %s (period, key, marker) VALUES (?, ?, ?), (?, ?, ?)", table),
		period, "1", "legacy", period, "1.00", "canonical"); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		value      any
		wantMarker string
	}{
		{name: "typed", value: int(1), wantMarker: "canonical"},
		{name: "lexical", value: "1", wantMarker: "legacy"},
		{name: "lexical bytes", value: []byte("1"), wantMarker: "legacy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			row, err := db.InfoRegGetLast(ctx, ir, map[string]any{"Key": test.value}, period.Add(time.Hour))
			if err != nil || row == nil || row["Marker"] != test.wantMarker {
				t.Fatalf("get last row=%#v err=%v; want marker %q", row, err, test.wantMarker)
			}
			storedPeriod, ok := row["period"].(time.Time)
			if !ok || !storedPeriod.Equal(period) {
				t.Fatalf("get last period=%T(%v); want %v", row["period"], row["period"], period)
			}
		})
	}
}
