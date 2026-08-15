package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestMatchCatalogByPresentationUsesEffectiveFallback(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "presentation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entity := &metadata.Entity{
		Name:         "Товары",
		Kind:         metadata.KindCatalog,
		Presentation: []string{"Артикул", "Наименование"},
		Fields: []metadata.Field{
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	write := func(article, name string) string {
		t.Helper()
		id, err := db.WriteCatalogRecord(ctx, entity, "", map[string]any{
			"артикул": article, "наименование": name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	t.Run("fallback does not count a shadowed secondary value", func(t *testing.T) {
		write("A-1", "Стул")
		fallbackID := write("", "Стул")

		id, display, count, err := db.MatchCatalogByPresentation(ctx, entity, "Стул")
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 || id != fallbackID || display != "Стул" {
			t.Fatalf("match = (%q, %q, %d), want fallback row %q", id, display, count, fallbackID)
		}
	})

	t.Run("same effective label across different candidates is ambiguous", func(t *testing.T) {
		write("Одинаково", "Другое")
		write("", "Одинаково")

		id, _, count, err := db.MatchCatalogByPresentation(ctx, entity, "Одинаково")
		if err != nil {
			t.Fatal(err)
		}
		if count != 2 || id != "" {
			t.Fatalf("match = (id %q, count %d), want ambiguity across both candidates", id, count)
		}
	})

	t.Run("unicode whitespace in primary field activates fallback", func(t *testing.T) {
		fallbackID := write("\u00a0\t", "Пробельный fallback")
		id, display, count, err := db.MatchCatalogByPresentation(ctx, entity, "Пробельный fallback")
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 || id != fallbackID || display != "Пробельный fallback" {
			t.Fatalf("match = (%q, %q, %d), want unicode-whitespace fallback %q", id, display, count, fallbackID)
		}
	})
}
