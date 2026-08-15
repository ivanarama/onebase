package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// Число с точностью округляется при записи — единое поведение PG и SQLite
// (SQLite хранит TEXT и сам не округляет).
func TestNumberSpec_RoundOnWriteSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entity := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Цена", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}

	id := uuid.New()
	if err := db.Upsert(ctx, "Товар", id, map[string]any{
		"Цена": decimal.RequireFromString("10.999"),
	}, entity); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	row, err := db.GetByID(ctx, "Товар", id, entity)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	got := row["Цена"].(decimal.Decimal)
	if got.String() != "11" {
		t.Errorf("округление до scale=2: 10.999 → %q, ожидалось 11", got.String())
	}
}

func TestCanonicalNumberArg_UsesDeclaredScale(t *testing.T) {
	tests := []struct {
		name  string
		field metadata.Field
		value any
		want  string
	}{
		{"plain trims zeros", metadata.Field{Name: "N", Type: metadata.FieldTypeNumber}, "100.00", "100"},
		{"fixed pads zeros", metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2}, 100, "100.00"},
		{"fixed rounds", metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2}, "1.005", "1.01"},
		{"explicit scale zero", metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 6, Scale: 0}, "10.6", "11"},
		{"decimal comma", metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 6, Scale: 2}, "1,5", "1.50"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := canonicalNumberArg(test.field, test.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("canonicalNumberArg=%T(%v), want %q", got, got, test.want)
			}
		})
	}
}

func TestCanonicalNumberArg_HugeExponentIsBounded(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := canonicalNumberArg(
			metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
			"1e2147483647",
		)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "разрядность") {
			t.Fatalf("huge positive exponent err=%v, want bounded precision error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("huge exponent canonicalization did not finish in bounded time")
	}

	got, err := canonicalNumberArg(
		metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2},
		"1e-2147483647",
	)
	if err != nil || got != "0.00" {
		t.Fatalf("huge negative exponent=%T(%v), err=%v, want 0.00", got, got, err)
	}
	for _, value := range []string{"1e2147483647", "1e-2147483647"} {
		if _, err := canonicalNumberArg(metadata.Field{Name: "N", Type: metadata.FieldTypeNumber}, value); err == nil {
			t.Fatalf("plain number %q: expected PostgreSQL-compatible size error", value)
		}
	}
}

func TestCanonicalNumberArg_RejectsInvalidNumberAndSpec(t *testing.T) {
	if _, err := canonicalNumberArg(metadata.Field{Name: "N", Type: metadata.FieldTypeNumber}, "not-a-number"); err == nil {
		t.Fatal("invalid number was silently passed to the driver")
	}
	if _, err := canonicalNumberArg(metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 2, Scale: 3}, "1"); err == nil {
		t.Fatal("invalid number precision was silently accepted")
	}
}

func TestCanonicalNumberArg_PGNumericSpecialValues(t *testing.T) {
	fixed := metadata.Field{Name: "N", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2}
	got, err := canonicalNumberArg(fixed, pgtype.Numeric{Valid: true, Exp: -7})
	if err != nil || got != "0.00" {
		t.Fatalf("finite nil-Int zero=%T(%v), err=%v, want 0.00", got, got, err)
	}

	for _, value := range []pgtype.Numeric{
		{Valid: true, InfinityModifier: pgtype.Infinity},
		{Valid: true, InfinityModifier: pgtype.NegativeInfinity},
		{Valid: true, NaN: true},
	} {
		if got, err := canonicalNumberArg(fixed, value); err == nil || got != nil {
			t.Fatalf("special PG numeric %#v => %T(%v), err=%v; want rejection", value, got, got, err)
		}
	}

	got, err = canonicalNumberArg(fixed, pgtype.Numeric{})
	if err != nil || got != nil {
		t.Fatalf("invalid PG numeric=%T(%v), err=%v, want empty nil", got, got, err)
	}
}

// Переполнение по числу целых разрядов даёт понятную ошибку, а не молчаливую
// запись (SQLite) / numeric overflow (PG).
func TestNumberSpec_OverflowError(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entity := &metadata.Entity{
		Name: "Товар",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Цена", Type: metadata.FieldTypeNumber, Length: 5, Scale: 2}, // макс 3 целых разряда
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}

	err = db.Upsert(ctx, "Товар", uuid.New(), map[string]any{
		"Цена": decimal.RequireFromString("1234.5"), // 4 целых разряда > 3
	}, entity)
	if err == nil {
		t.Fatal("ожидалась ошибка переполнения разрядности, получено nil")
	}
	if !strings.Contains(err.Error(), "разрядность") {
		t.Errorf("ошибка не про разрядность: %v", err)
	}
}

func TestFieldType_NumberSpecDDL(t *testing.T) {
	f := metadata.Field{Name: "Цена", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2}
	if got := fieldType(PgDialect{}, f); got != "NUMERIC(10,2)" {
		t.Errorf("PG fieldType = %q, want NUMERIC(10,2)", got)
	}
	if got := fieldType(SQLiteDialect{}, f); got != "TEXT" {
		t.Errorf("SQLite fieldType = %q, want TEXT", got)
	}
	// Без разрядности — NUMERIC без параметров.
	plain := metadata.Field{Name: "Кол", Type: metadata.FieldTypeNumber}
	if got := fieldType(PgDialect{}, plain); got != "NUMERIC" {
		t.Errorf("PG plain number = %q, want NUMERIC", got)
	}
}
