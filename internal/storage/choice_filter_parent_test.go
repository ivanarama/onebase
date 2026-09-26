package storage_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// parent_id отбирает по месту САМОЙ записи в дереве справочника, а
// not_in_hierarchy позволяет исключить ветку целиком. Без этого архивную папку
// было нечем убрать из подбора: is_folder скрывает саму группу, но не её
// содержимое, а обойти иерархию через ссылочные реквизиты невозможно —
// родитель реквизитом не является.
func TestChoiceFilterParentHierarchy(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		catalog := &metadata.Entity{
			Name: "ChoiceBranch", Kind: metadata.KindCatalog, Hierarchical: true,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{catalog}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		архив := uuid.New()
		строки := []struct {
			id     uuid.UUID
			имя    string
			папка  bool
			родитель *uuid.UUID
		}{
			{архив, "Неиспользуемые", true, nil},
			{uuid.New(), "старое одно", false, &архив},
			{uuid.New(), "старое два", false, &архив},
			{uuid.New(), "рабочее одно", false, nil},
			{uuid.New(), "рабочее два", false, nil},
		}
		for _, с := range строки {
			поля := map[string]any{"Наименование": с.имя, "ЭтоГруппа": с.папка}
			if с.родитель != nil {
				поля["Родитель"] = с.родитель.String()
			}
			if err := db.Upsert(ctx, catalog.Name, с.id, поля, catalog); err != nil {
				t.Fatalf("посев %s: %v", с.имя, err)
			}
		}

		имена := func(rows []map[string]any) string {
			out := make([]string, 0, len(rows))
			for _, r := range rows {
				out = append(out, strings.TrimSpace(r["Наименование"].(string)))
			}
			sort.Strings(out)
			return strings.Join(out, ", ")
		}

		t.Run("not_in_hierarchy исключает ветку целиком", func(t *testing.T) {
			rows, err := db.List(ctx, catalog.Name, catalog, storage.ListParams{
				ChoicePredicates: []storage.ChoicePredicate{
					{Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Value: архив},
				},
			})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := имена(rows); got != "рабочее два, рабочее одно" {
				t.Fatalf("осталось %q, ожидалось только рабочее", got)
			}
		})

		t.Run("in_hierarchy оставляет ветку и её корень", func(t *testing.T) {
			rows, err := db.List(ctx, catalog.Name, catalog, storage.ListParams{
				ChoicePredicates: []storage.ChoicePredicate{
					{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Value: архив},
				},
			})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := имена(rows); got != "Неиспользуемые, старое два, старое одно" {
				t.Fatalf("ветка = %q", got)
			}
		})

		t.Run("вместе с is_folder убирает и саму папку", func(t *testing.T) {
			rows, err := db.List(ctx, catalog.Name, catalog, storage.ListParams{
				ChoicePredicates: []storage.ChoicePredicate{
					{Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Value: архив},
					{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false},
				},
			})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := имена(rows); got != "рабочее два, рабочее одно" {
				t.Fatalf("осталось %q", got)
			}
		})
	})
}
