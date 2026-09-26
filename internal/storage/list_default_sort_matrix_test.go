package storage_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// List — публичный путь списков и форм подбора. Фиксированные UUID отделяют
// пользовательский порядок от случайного порядка первичного ключа.
func TestListDefaultSortMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{
			Name: "СортДок" + uuid.NewString()[:8], Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Дата", Type: metadata.FieldTypeDate},
			},
		}
		catalog := &metadata.Entity{
			Name: "СортСправ" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Код", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{doc, catalog}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		lowID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		middleID := uuid.MustParse("80000000-0000-0000-0000-000000000002")
		emptyID := uuid.MustParse("e0000000-0000-0000-0000-000000000004")
		highID := uuid.MustParse("f0000000-0000-0000-0000-000000000003")
		oldDate := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		newDate := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
		for _, row := range []struct {
			id     uuid.UUID
			number string
			date   time.Time
		}{
			{middleID, "A-old", oldDate},
			{highID, "B-new-high-id", newDate},
			{lowID, "C-new-low-id", newDate},
		} {
			if err := db.Upsert(ctx, doc.Name, row.id, map[string]any{
				"Номер": row.number, "Дата": row.date,
			}, doc); err != nil {
				t.Fatalf("Upsert document %s: %v", row.number, err)
			}
		}
		if err := db.Upsert(ctx, doc.Name, emptyID, map[string]any{"Номер": "D-no-date"}, doc); err != nil {
			t.Fatalf("Upsert document without date: %v", err)
		}
		assertListFieldOrder(t, db, doc, storage.ListParams{}, "Номер",
			[]string{"C-new-low-id", "B-new-high-id", "A-old", "D-no-date"})
		// Явный выбор колонки и keyset по id имеют приоритет над новым дефолтом.
		assertListFieldOrder(t, db, doc, storage.ListParams{Sort: "Номер"}, "Номер",
			[]string{"A-old", "B-new-high-id", "C-new-low-id", "D-no-date"})
		assertListFieldOrder(t, db, doc, storage.ListParams{AfterID: &lowID}, "Номер",
			[]string{"A-old", "D-no-date", "B-new-high-id"})

		for _, row := range []struct {
			id   uuid.UUID
			name string
			code string
		}{
			{lowID, "B", "B-low-id"},
			{highID, "A", "A-high-id"},
			{middleID, "A", "A-middle-id"},
		} {
			if err := db.Upsert(ctx, catalog.Name, row.id, map[string]any{
				"Наименование": row.name, "Код": row.code,
			}, catalog); err != nil {
				t.Fatalf("Upsert catalog %s: %v", row.code, err)
			}
		}
		if err := db.Upsert(ctx, catalog.Name, emptyID, map[string]any{"Код": "Z-no-name"}, catalog); err != nil {
			t.Fatalf("Upsert catalog without name: %v", err)
		}
		assertListFieldOrder(t, db, catalog, storage.ListParams{}, "Код",
			[]string{"A-middle-id", "A-high-id", "B-low-id", "Z-no-name"})
	})
}

func assertListFieldOrder(t *testing.T, db *storage.DB, entity *metadata.Entity, params storage.ListParams, field string, want []string) {
	t.Helper()
	params.RowFilterEvaluated = true
	rows, err := db.List(context.Background(), entity.Name, entity, params)
	if err != nil {
		t.Fatalf("List %s: %v", entity.Name, err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row[field].(string))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List %s (%+v): порядок %v, want %v", entity.Name, params, got, want)
	}
}
