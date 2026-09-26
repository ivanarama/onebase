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

// Отбор по табличной части: запись подходит, если в её ТЧ есть строка с нужным
// значением. Так выражается связь многие-ко-многим — бренд обслуживает
// несколько направлений, и одним реквизитом записи это не описать.
func TestChoiceFilterTablePart(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		направление := &metadata.Entity{
			Name: "ChoiceDir", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		бренд := &metadata.Entity{
			Name: "ChoiceBrand", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{{
				Name: "Направления",
				Fields: []metadata.Field{{
					Name: "Направление", Type: metadata.FieldType("reference:ChoiceDir"), RefEntity: "ChoiceDir",
				}},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{направление, бренд}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		хд, шм := uuid.New(), uuid.New()
		for id, имя := range map[uuid.UUID]string{хд: "ХД", шм: "ШМ"} {
			if err := db.Upsert(ctx, направление.Name, id, map[string]any{"Наименование": имя}, направление); err != nil {
				t.Fatalf("посев направления: %v", err)
			}
		}
		for _, б := range []struct {
			имя  string
			напр []uuid.UUID
		}{
			{"Холодильный", []uuid.UUID{хд}},
			{"Швейный", []uuid.UUID{шм}},
			{"Универсальный", []uuid.UUID{хд, шм}},
			{"Без направлений", nil},
		} {
			id := uuid.New()
			строки := make([]map[string]any, 0, len(б.напр))
			for _, н := range б.напр {
				строки = append(строки, map[string]any{"Направление": н.String()})
			}
			if err := db.Upsert(ctx, бренд.Name, id, map[string]any{"Наименование": б.имя}, бренд); err != nil {
				t.Fatalf("посев бренда %s: %v", б.имя, err)
			}
			if len(строки) > 0 {
				if err := db.UpsertTablePartRows(ctx, бренд.Name, "Направления", id, строки, бренд.TableParts[0]); err != nil {
					t.Fatalf("посев ТЧ бренда %s: %v", б.имя, err)
				}
			}
		}

		rows, err := db.List(ctx, бренд.Name, бренд, storage.ListParams{
			ChoicePredicates: []storage.ChoicePredicate{
				{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Value: хд},
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
		if got := strings.Join(имена, ", "); got != "Универсальный, Холодильный" {
			t.Fatalf("по направлению ХД = %q, ожидались только бренды с этим направлением", got)
		}
	})
}
