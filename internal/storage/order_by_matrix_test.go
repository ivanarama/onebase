package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestOrderByExactNumberMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name:    "ТочныйПорядок" + uuid.NewString()[:8],
			Kind:    metadata.KindCatalog,
			OrderBy: []string{"Порядок"},
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Порядок", Type: metadata.FieldTypeNumber},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		for _, row := range []struct {
			name  string
			order string
		}{
			{name: "большее", order: "9007199254740993"},
			{name: "меньшее", order: "9007199254740992"},
		} {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
				"Наименование": row.name,
				"Порядок":      row.order,
			}, entity); err != nil {
				t.Fatalf("Upsert %s: %v", row.name, err)
			}
		}

		rows, err := db.List(ctx, entity.Name, entity, storage.ListParams{RowFilterEvaluated: true})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := rows[0]["Наименование"]; got != "меньшее" {
			t.Fatalf("первым идёт %v, want меньшее", got)
		}
	})
}

func TestOrderByEmptyTextLastMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name:    "ПустойПорядок" + uuid.NewString()[:8],
			Kind:    metadata.KindCatalog,
			OrderBy: []string{"Ранг"},
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Ранг", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		for _, row := range []struct {
			name string
			rank string
		}{
			{name: "без ранга", rank: ""},
			{name: "с рангом", rank: "B"},
		} {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
				"Наименование": row.name,
				"Ранг":         row.rank,
			}, entity); err != nil {
				t.Fatalf("Upsert %s: %v", row.name, err)
			}
		}

		rows, err := db.List(ctx, entity.Name, entity, storage.ListParams{RowFilterEvaluated: true})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := rows[0]["Наименование"]; got != "с рангом" {
			t.Fatalf("первым идёт %v, want с рангом", got)
		}
	})
}
