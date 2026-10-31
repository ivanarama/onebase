package storage_test

// Порядок иерархического справочника: папки впереди — только когда они
// перемешаны с элементами. Выбор значения ссылки (ExcludeFolders) и выбор
// папки (OnlyFolders) упорядочены одной колонкой — так «первые 50» берутся
// по индексу, а не сортировкой всего справочника.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestList_HierarchyOrderMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "Адреса" + uuid.NewString()[:8], Kind: metadata.KindCatalog, Hierarchical: true,
			Fields: []metadata.Field{{Name: "Код", Type: metadata.FieldTypeString}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, r := range []struct {
			код   string
			папка bool
		}{{"В", false}, {"Б", true}, {"А", false}, {"Г", true}} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Код": r.код, "is_folder": r.папка}, ent); err != nil {
				t.Fatalf("запись: %v", err)
			}
		}
		коды := func(p storage.ListParams) []string {
			t.Helper()
			rows, err := db.List(ctx, ent.Name, ent, p)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var out []string
			for _, r := range rows {
				out = append(out, r["Код"].(string))
			}
			return out
		}
		равно := func(имя string, got, want []string) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("%s: %v, ожидалось %v", имя, got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%s: %v, ожидалось %v", имя, got, want)
				}
			}
		}
		равно("только элементы", коды(storage.ListParams{ExcludeFolders: true}), []string{"А", "В"})
		равно("только папки", коды(storage.ListParams{OnlyFolders: true}), []string{"Б", "Г"})
		// Без отбора папок — как раньше: папки впереди, внутри — по коду.
		равно("вперемешку", коды(storage.ListParams{}), []string{"Б", "Г", "А", "В"})
	})
}
