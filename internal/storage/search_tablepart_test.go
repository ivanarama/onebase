package storage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestListCountTablePartSearchMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		// Also guard against a catalog name colliding with the generated inner alias.
		ent := &metadata.Entity{Name: "ob_search_tp_0", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Регион", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{
				{Name: "Контакты", Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}, {Name: "Email", Type: metadata.FieldTypeString}, {Name: "Число", Type: metadata.FieldTypeNumber}}},
				{Name: "Другие", Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeString}}},
			}, SearchSet: true, Search: []string{"контакты.значение", "Наименование", "Другие.Значение", "Контакты.Email", "Контакты.Число"},
		}
		if err := metadata.Validate([]*metadata.Entity{ent}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.MustParse("00000000-0000-0000-0000-000000000002"), uuid.MustParse("00000000-0000-0000-0000-000000000003")}
		for i, id := range ids {
			if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": fmt.Sprintf("Owner %d", i), "Регион": []string{"A", "B", "A"}[i]}, ent); err != nil {
				t.Fatal(err)
			}
		}
		replace := func(id uuid.UUID, tp int, rows []map[string]any) {
			t.Helper()
			if err := db.UpsertTablePartRows(ctx, ent.Name, ent.TableParts[tp].Name, id, rows, ent.TableParts[tp]); err != nil {
				t.Fatal(err)
			}
		}
		replace(ids[0], 0, []map[string]any{{"Значение": "Общий телефон 555", "Email": "first@example.test", "Число": 12345}, {"Значение": "Общий телефон 555", "Email": "Общий второй"}})
		replace(ids[1], 0, []map[string]any{{"Значение": "Общий телефон 666"}})
		replace(ids[0], 1, []map[string]any{{"Значение": "Общий дополнительный"}})
		replace(ids[2], 1, []map[string]any{{"Значение": "другой контакт 777"}})
		check := func(params storage.ListParams, want []uuid.UUID, total int) {
			t.Helper()
			params.Sort = "id"
			got := listKeysetIDs(t, db, ent, params)
			if len(want) == 0 {
				if len(got) != 0 {
					t.Fatalf("unexpected owners %v", got)
				}
			} else {
				assertUUIDs(t, got, want)
			}
			count, err := db.CountList(ctx, ent.Name, ent, params)
			if err != nil || count != total {
				t.Fatalf("CountList = %d, %v; want %d", count, err, total)
			}
		}
		check(storage.ListParams{Search: "оБщИй"}, ids[:2], 2)
		check(storage.ListParams{Search: "example"}, ids[:1], 1)
		check(storage.ListParams{Search: "123"}, ids[:1], 1)
		check(storage.ListParams{Search: "777"}, ids[2:], 1)
		check(storage.ListParams{Search: "Owner 1"}, ids[1:2], 1)
		check(storage.ListParams{Search: "Общий", Limit: 1, Offset: 1}, ids[1:2], 2)
		check(storage.ListParams{Search: "Общий", AfterID: &ids[0], ThroughID: &ids[1], Limit: 1}, ids[1:2], 2)
		check(storage.ListParams{Search: "Общий", Filters: map[string]storage.FilterValue{"Регион": {Value: "A"}}, RowFilter: &storage.Predicate{Field: "Наименование", Op: "eq", Value: "Owner 0"}, RowFilterEvaluated: true}, ids[:1], 1)
		check(storage.ListParams{Search: "Общий", RowFilter: &storage.Predicate{Field: "Регион", Op: "eq", Value: "B"}, RowFilterEvaluated: true}, ids[1:2], 1)
		check(storage.ListParams{Search: "absent"}, nil, 0)
		ent.SearchSet = false
		check(storage.ListParams{Search: "555"}, nil, 0)
		check(storage.ListParams{Search: "Owner"}, ids, 3)
		ent.SearchSet = true
		ent.Search = nil
		check(storage.ListParams{Search: "Owner"}, nil, 0)
		check(storage.ListParams{}, ids, 3)
		ent.Search = []string{"Контакты.Значение"}
		replace(ids[0], 0, nil)
		check(storage.ListParams{Search: "555"}, nil, 0)
		check(storage.ListParams{Search: "Общий"}, ids[1:2], 1)
		ent.Search = []string{"Контакты.Нет"}
		if _, err := db.List(ctx, ent.Name, ent, storage.ListParams{Search: "x"}); err == nil {
			t.Fatal("invalid runtime path silently accepted by List")
		}
		if _, err := db.CountList(ctx, ent.Name, ent, storage.ListParams{Search: "x"}); err == nil {
			t.Fatal("invalid runtime path silently accepted by CountList")
		}
	})
}
