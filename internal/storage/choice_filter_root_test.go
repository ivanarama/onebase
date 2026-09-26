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

// is_root оставляет только записи верхнего уровня, а булев реквизит
// сравнивается с литералом. Вместе это «регионы, кроме муниципальных» —
// выразить такое ссылочными условиями было нечем.
func TestChoiceFilterRootAndBool(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		catalog := &metadata.Entity{
			Name: "ChoiceRegion", Kind: metadata.KindCatalog, Hierarchical: true,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Муниципальный", Type: metadata.FieldTypeBool},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{catalog}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		корень := uuid.New()
		строки := []struct {
			id       uuid.UUID
			имя      string
			муницип  bool
			родитель *uuid.UUID
		}{
			{корень, "Москва", false, nil},
			{uuid.New(), "Москва муниципальная", true, nil},
			{uuid.New(), "Московская обл", false, nil},
			{uuid.New(), "улица внутри", false, &корень},
		}
		for _, с := range строки {
			поля := map[string]any{"Наименование": с.имя, "Муниципальный": с.муницип}
			if с.родитель != nil {
				поля["Родитель"] = с.родитель.String()
			}
			if err := db.Upsert(ctx, catalog.Name, с.id, поля, catalog); err != nil {
				t.Fatalf("посев %s: %v", с.имя, err)
			}
		}

		rows, err := db.List(ctx, catalog.Name, catalog, storage.ListParams{
			ChoicePredicates: []storage.ChoicePredicate{
				{Field: "is_root", Op: metadata.FormChoiceOpEqual, Value: true},
				{Field: "Муниципальный", Op: metadata.FormChoiceOpEqual, Value: false},
			},
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		имена := make([]string, 0, len(rows))
		for _, r := range rows {
			имена = append(имена, strings.TrimSpace(r["Наименование"].(string)))
		}
		sort.Strings(имена)
		if got := strings.Join(имена, ", "); got != "Москва, Московская обл" {
			t.Fatalf("осталось %q, ожидались только немуниципальные записи верхнего уровня", got)
		}
	})
}
