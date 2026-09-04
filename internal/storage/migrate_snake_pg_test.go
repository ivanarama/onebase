//go:build integration

package storage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestMigrateRegistersPostgresLegacyReferenceIsFailClosed(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	schema := NewEphemeralSchemaName()
	db, err := ConnectWithSchema(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	if err := db.CreateSchema(ctx, schema); err != nil {
		db.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.DropSchemaCascade(context.Background(), schema)
		db.Close()
	})

	cases := []struct {
		name      string
		legacy    string
		wantError bool
	}{
		{name: "valid", legacy: "  " + uuid.NewString() + "  "},
		{name: "empty", legacy: "", wantError: true},
		{name: "whitespace", legacy: "   ", wantError: true},
		{name: "invalid", legacy: "not-a-uuid", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &metadata.Register{
				Name: "LegacyRef" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				Dimensions: []metadata.Field{{
					Name:      "CashDesk",
					RefEntity: "CashDesk",
				}},
			}
			table := metadata.RegisterTableName(reg.Name)
			if _, err := db.Exec(ctx, CreateRegisterSQL(db.Dialect(), reg)); err != nil {
				t.Fatalf("create register table: %v", err)
			}
			if _, err := db.Exec(ctx, "ALTER TABLE "+pgQuoteIdent(table)+" ADD COLUMN cash_desk TEXT"); err != nil {
				t.Fatalf("add legacy column: %v", err)
			}
			if _, err := db.Exec(ctx, "INSERT INTO "+pgQuoteIdent(table)+" (id, recorder, recorder_type, cash_desk) VALUES ($1, $2, $3, $4)",
				uuid.New(), uuid.New(), "Document", tc.legacy); err != nil {
				t.Fatalf("insert legacy row: %v", err)
			}

			err := db.MigrateRegisters(ctx, []*metadata.Register{reg})
			if tc.wantError {
				if err == nil {
					t.Fatal("MigrateRegisters succeeded for a non-UUID legacy reference")
				}
				if !strings.Contains(err.Error(), "перенос данных") {
					t.Fatalf("MigrateRegisters error = %q, want data-transfer context", err)
				}
				assertLegacyReferencePreserved(t, ctx, db, table, tc.legacy)
				return
			}
			if err != nil {
				t.Fatalf("MigrateRegisters: %v", err)
			}

			var got string
			if err := db.QueryRow(ctx, "SELECT cashdesk_id::text FROM "+pgQuoteIdent(table)).Scan(&got); err != nil {
				t.Fatalf("read migrated reference: %v", err)
			}
			if got != strings.TrimSpace(tc.legacy) {
				t.Fatalf("migrated reference = %q, want %q", got, strings.TrimSpace(tc.legacy))
			}
			if legacyRefColumnExists(t, ctx, db, table, "cash_desk") {
				t.Fatal("legacy column still exists after a successful migration")
			}
		})
	}
}

func assertLegacyReferencePreserved(t *testing.T, ctx context.Context, db *DB, table, want string) {
	t.Helper()
	if !legacyRefColumnExists(t, ctx, db, table, "cash_desk") {
		t.Fatal("legacy column was dropped after a failed migration")
	}
	var got string
	if err := db.QueryRow(ctx, "SELECT cash_desk FROM "+pgQuoteIdent(table)).Scan(&got); err != nil {
		t.Fatalf("read preserved legacy value: %v", err)
	}
	if got != want {
		t.Fatalf("preserved legacy value = %q, want %q", got, want)
	}
	var newIsNull bool
	if err := db.QueryRow(ctx, "SELECT cashdesk_id IS NULL FROM "+pgQuoteIdent(table)).Scan(&newIsNull); err != nil {
		t.Fatalf("read target reference: %v", err)
	}
	if !newIsNull {
		t.Fatal("target reference changed after a failed migration")
	}
}

func legacyRefColumnExists(t *testing.T, ctx context.Context, db *DB, table, column string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2
	)`, table, column).Scan(&exists); err != nil {
		t.Fatalf("inspect column %s.%s: %v", table, column, err)
	}
	return exists
}
