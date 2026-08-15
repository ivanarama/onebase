package storage_test

// Префикс базы (план 117D). Живёт в ДАННЫХ базы, а не в конфигурации:
// конфигурация одинакова во всех базах, поэтому «понять, откуда приехал
// объект» через неё невозможно by design.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func prefixCatalog(basePrefix bool) *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
		Fields:    []metadata.Field{{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString}},
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6, Period: "none", BasePrefix: basePrefix},
	}
}

// Префикс базы подставляется перед префиксом объекта — и только при явном
// base_prefix: true.
func TestBasePrefixMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.EnsureNumeratorSchema(ctx); err != nil {
			t.Fatalf("схема: %v", err)
		}
		if err := db.SaveBasePrefix(ctx, "Ф-"); err != nil {
			t.Fatalf("SaveBasePrefix: %v", err)
		}
		if got := db.GetBasePrefix(ctx); got != "Ф-" {
			t.Fatalf("GetBasePrefix = %q", got)
		}

		withPrefix, err := db.GenerateNumber(ctx, prefixCatalog(true), map[string]any{})
		if err != nil {
			t.Fatalf("GenerateNumber: %v", err)
		}
		if withPrefix != "Ф-К-000001" {
			t.Errorf("код = %q, ожидался Ф-К-000001 (префикс базы перед префиксом объекта)", withPrefix)
		}

		// Без base_prefix: true формат номеров не меняется — иначе включение
		// префикса на базе молча переформатировало бы всё сразу.
		plain, err := db.GenerateNumber(ctx, prefixCatalog(false), map[string]any{})
		if err != nil {
			t.Fatalf("GenerateNumber: %v", err)
		}
		if strings.HasPrefix(plain, "Ф-") {
			t.Errorf("код = %q: префикс базы подставлен без base_prefix: true", plain)
		}
	})
}

// Снятие префикса возвращает прежний формат: это путь восстановления копии в
// другую базу, где клон обязан перестать выдавать коды оригинала.
func TestBasePrefix_ClearRestoresPlainFormat(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.EnsureNumeratorSchema(ctx); err != nil {
			t.Fatalf("схема: %v", err)
		}
		ent := prefixCatalog(true)
		if err := db.SaveBasePrefix(ctx, "Ф-"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.GenerateNumber(ctx, ent, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveBasePrefix(ctx, ""); err != nil {
			t.Fatal(err)
		}
		after, err := db.GenerateNumber(ctx, ent, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(after, "Ф-") {
			t.Errorf("после снятия префикса код = %q", after)
		}
		if db.GetBasePrefix(ctx) != "" {
			t.Error("префикс не снят")
		}
	})
}

// Restore identity changes are safety-critical: a failed read must not be
// mistaken for an absent prefix and reported as a successful reset.
func TestResetBasePrefixAfterRestore_ReadFailureIsReported(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if err := db.SaveBasePrefix(context.Background(), "Ф-"); err != nil {
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if previous, err := db.ResetBasePrefixAfterRestore(canceled); err == nil {
			t.Fatalf("ResetBasePrefixAfterRestore = (%q, nil) on canceled read", previous)
		}
		if got := db.GetBasePrefix(context.Background()); got != "Ф-" {
			t.Fatalf("failed reset changed prefix to %q", got)
		}
	})
}
