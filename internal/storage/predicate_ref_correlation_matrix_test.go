package storage_test

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ревью #2172: условие по реквизиту связанной записи («Owner.Account = я»)
// в списке без квалификатора обязано сравнивать ссылку ВНЕШНЕЙ строки. Когда у
// связанной сущности есть одноимённая ссылка Owner, прежний EXISTS
// (... WHERE rls_ref.id = owner_id) связывал owner_id с внутренней таблицей и
// пропускал чужие записи.
func TestListReferencePredicateKeepsOuterCorrelationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		related := &metadata.Entity{Name: "ВладелецПробы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Account", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: "reference:ВладелецПробы", RefEntity: "ВладелецПробы"},
		}}
		source := &metadata.Entity{Name: "ЗаписьПробы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Owner", Type: "reference:ВладелецПробы", RefEntity: "ВладелецПробы"},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{related, source}); err != nil {
			t.Fatal(err)
		}
		mine, other := uuid.New(), uuid.New()
		// Обе связанные записи указывают Owner на «мою» — внутренняя owner_id
		// совпадает с моей id в каждой строке подзапроса.
		// Порядок важен: «чужая» ссылается на «мою», а «моя» — на саму себя
		// (сначала создаётся без ссылки).
		for _, it := range []struct {
			id  uuid.UUID
			row map[string]any
		}{
			{mine, map[string]any{"Account": "u-me"}},
			{mine, map[string]any{"Account": "u-me", "Owner": mine.String()}},
			{other, map[string]any{"Account": "u-other", "Owner": mine.String()}},
		} {
			id, row := it.id, it.row
			if err := db.Upsert(ctx, related.Name, id, row, related); err != nil {
				t.Fatal(err)
			}
		}
		for name, owner := range map[string]uuid.UUID{"своя": mine, "чужая": other} {
			if err := db.Upsert(ctx, source.Name, uuid.New(), map[string]any{"Наименование": name, "Owner": owner.String()}, source); err != nil {
				t.Fatal(err)
			}
		}
		list := func(pred storage.Predicate) []string {
			t.Helper()
			rows, err := db.List(ctx, source.Name, source, storage.ListParams{Limit: 100, RowFilter: &pred, RowFilterEvaluated: true})
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, row := range rows {
				names = append(names, row["Наименование"].(string))
			}
			sort.Strings(names)
			return names
		}
		ref := storage.Predicate{Field: "Owner", RefEntity: related,
			RefPredicate: &storage.Predicate{Field: "Account", Op: "eq", Value: "u-me"}}
		if got := list(ref); len(got) != 1 || got[0] != "своя" {
			t.Fatalf("Owner.Account = u-me: %v, want [своя]", got)
		}
		// Под NOT — дополнение, без потери строк с пустой ссылкой.
		if err := db.Upsert(ctx, source.Name, uuid.New(), map[string]any{"Наименование": "без владельца"}, source); err != nil {
			t.Fatal(err)
		}
		if got := list(storage.Predicate{Not: &ref}); len(got) != 2 || got[0] != "без владельца" || got[1] != "чужая" {
			t.Fatalf("NOT Owner.Account = u-me: %v, want [без владельца чужая]", got)
		}
	})
}
