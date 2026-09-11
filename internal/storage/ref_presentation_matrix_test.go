package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Поиск по реквизиту обязан строить ту же подпись, что списки и пикеры, на
// обоих диалектах. В частности, поле поиска не становится скрытым fallback, а
// синтетическая подпись документа получает нужные ей даты и числа.
func TestFieldLookupPresentationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		t.Run("explicit presentation does not fall back to search field", func(t *testing.T) {
			entity := &metadata.Entity{
				Name:         "LookupPresentation" + uuid.NewString()[:8],
				Kind:         metadata.KindCatalog,
				Presentation: []string{"Артикул"},
				Fields: []metadata.Field{
					{Name: "Артикул", Type: metadata.FieldTypeString},
					{Name: "Код", Type: metadata.FieldTypeString},
				},
			}
			if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			id, err := db.WriteCatalogRecord(ctx, entity, "", map[string]any{"Артикул": "", "Код": "K-1"})
			if err != nil {
				t.Fatalf("WriteCatalogRecord: %v", err)
			}

			assertFieldLookupLabels(t, db, entity, "Код", "K-1", id)
		})

		t.Run("document uses date and number fallback", func(t *testing.T) {
			owners := &metadata.Entity{
				Name: "LookupOwners" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			}
			doc := &metadata.Entity{
				Name: "LookupDocument" + uuid.NewString()[:8], Kind: metadata.KindDocument,
				Fields: []metadata.Field{
					{Name: "Владелец", Type: metadata.FieldType("reference:" + owners.Name), RefEntity: owners.Name},
					{Name: "Дата", Type: metadata.FieldTypeDate},
					{Name: "Сумма", Type: metadata.FieldTypeNumber},
				},
			}
			if err := db.Migrate(ctx, []*metadata.Entity{owners, doc}); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			ownerID := uuid.New()
			if err := db.Upsert(ctx, owners.Name, ownerID, map[string]any{"Наименование": "Владелец"}, owners); err != nil {
				t.Fatalf("Upsert owner: %v", err)
			}
			docID := uuid.New()
			if err := db.Upsert(ctx, doc.Name, docID, map[string]any{
				"Владелец": ownerID,
				"Дата":     time.Date(2026, time.September, 9, 12, 30, 0, 0, time.UTC),
				"Сумма":    125,
			}, doc); err != nil {
				t.Fatalf("Upsert document: %v", err)
			}

			assertFieldLookupLabels(t, db, doc, "Владелец", ownerID.String(), "09.09.2026 · 125")
		})
	})
}

func assertFieldLookupLabels(t *testing.T, db *storage.DB, entity *metadata.Entity, field, value, want string) {
	t.Helper()
	ctx := context.Background()

	id, display, found, err := db.FindCatalogByField(ctx, entity, field, value)
	if err != nil {
		t.Fatalf("FindCatalogByField: %v", err)
	}
	if !found || id == "" || display != want {
		t.Fatalf("FindCatalogByField = (%q, %q, %v), want nonempty id and %q", id, display, found, want)
	}

	ids, displays, err := db.ListCatalogMatchesByField(ctx, entity, field, value)
	if err != nil {
		t.Fatalf("ListCatalogMatchesByField: %v", err)
	}
	if len(ids) != 1 || len(displays) != 1 || ids[0] != id || displays[0] != want {
		t.Fatalf("ListCatalogMatchesByField = (%v, %v), want ([%s], [%s])", ids, displays, id, want)
	}
}
