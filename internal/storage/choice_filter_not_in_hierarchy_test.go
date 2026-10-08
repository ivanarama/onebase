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

// not_in_hierarchy (#1821, вариант 1) — точное дополнение in_hierarchy для
// того же поля и значения: запись, чей реквизит пуст, проходит (она ни в
// каком поддереве не лежит). Голый SQL NOT IN такую запись молча теряет —
// поэтому матричный тест: одно тело на SQLite и PostgreSQL.
func TestChoiceFilterNotInHierarchyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		fixture := seedChoiceFilterFixture(t, db)
		// Общая запись: направление не задано вовсе.
		if err := db.Upsert(ctx, fixture.target.Name, choiceTestUUID("000000000201"), map[string]any{
			"Наименование": "common alpha",
			"Owner":        "alice",
		}, fixture.target); err != nil {
			t.Fatalf("seed common: %v", err)
		}
		notFolder := storage.ChoicePredicate{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false}
		names := func(predicates ...storage.ChoicePredicate) []string {
			t.Helper()
			params := storage.ListParams{ChoicePredicates: predicates}
			rows, err := db.List(ctx, fixture.target.Name, fixture.target, params)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			total, err := db.CountList(ctx, fixture.target.Name, fixture.target, params)
			if err != nil {
				t.Fatalf("CountList: %v", err)
			}
			got := choiceRowNames(rows)
			sort.Strings(got)
			if total != len(got) {
				t.Fatalf("CountList=%d, а строк %d: %v", total, len(got), got)
			}
			return got
		}
		want := func(list ...string) string {
			sort.Strings(list)
			return strings.Join(list, ",")
		}

		t.Run("исключает поддерево, пустой реквизит проходит", func(t *testing.T) {
			got := names(storage.ChoicePredicate{Field: "Направление", Op: metadata.FormChoiceOpNotInHierarchy, Value: fixture.child}, notFolder)
			if strings.Join(got, ",") != want("root alpha", "sibling alpha", "common alpha") {
				t.Fatalf("not_in_hierarchy child = %v", got)
			}
		})

		t.Run("точное дополнение in_hierarchy", func(t *testing.T) {
			all := names()
			for _, group := range []uuid.UUID{fixture.root, fixture.child, fixture.grand, fixture.sibling} {
				in := names(storage.ChoicePredicate{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, Value: group})
				out := names(storage.ChoicePredicate{Field: "Направление", Op: metadata.FormChoiceOpNotInHierarchy, Value: group})
				union := append(append([]string{}, in...), out...)
				sort.Strings(union)
				if strings.Join(union, ",") != strings.Join(all, ",") {
					t.Fatalf("группа %s: in %v + not_in %v ≠ всё %v", group, in, out, all)
				}
			}
		})

		t.Run("несуществующая группа ничего не исключает", func(t *testing.T) {
			// На уровне SQL поддерево пусто. Пустую выдачу для невидимой группы
			// даёт слой подбора (choiceRefVisible) — см. ui-тест.
			got := names(storage.ChoicePredicate{Field: "Направление", Op: metadata.FormChoiceOpNotInHierarchy, Value: choiceTestUUID("000000009999")})
			if strings.Join(got, ",") != strings.Join(names(), ",") {
				t.Fatalf("несуществующая группа исключила записи: %v", got)
			}
		})
	})
}

// parent_id: not_in_hierarchy X — всё, что не строго внутри X: сама X,
// корневые записи и чужие ветки проходят. Цикл в иерархии не зацикливает обход.
func TestChoiceFilterParentIDNotInHierarchyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := seedChoiceParentFixture(t, db)
		want := func(names ...string) string {
			sort.Strings(names)
			return strings.Join(names, ",")
		}
		everything := choiceParentNames(t, db, f)

		t.Run("всё, кроме содержимого «Техники»", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Value: f.tech})
			if strings.Join(got, ",") != want("Техника", "Прочее", "лампа", "корневой элемент", "цикл А", "цикл Б") {
				t.Fatalf("не внутри «Техники» = %v", got)
			}
		})

		t.Run("с is_folder=false — элементы вне группы", func(t *testing.T) {
			got := choiceParentNames(t, db, f,
				storage.ChoicePredicate{Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Value: f.kitchen},
				storage.ChoicePredicate{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false})
			if strings.Join(got, ",") != want("утюг", "лампа", "корневой элемент") {
				t.Fatalf("элементы вне «Кухни» = %v", got)
			}
		})

		t.Run("цикл в иерархии — обход завершается", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Value: f.cycleA})
			for _, name := range got {
				if name == "цикл А" || name == "цикл Б" {
					t.Fatalf("запись цикла %q в выдаче: %v", name, got)
				}
			}
			if len(got) != len(everything)-2 {
				t.Fatalf("вне цикла = %v, всего %v", got, everything)
			}
		})

		t.Run("точное дополнение in_hierarchy", func(t *testing.T) {
			for _, group := range []uuid.UUID{f.tech, f.kitchen, f.stoves, f.other, f.stove, f.cycleA} {
				in := choiceParentNames(t, db, f, storage.ChoicePredicate{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Value: group})
				out := choiceParentNames(t, db, f, storage.ChoicePredicate{Field: "parent_id", Op: metadata.FormChoiceOpNotInHierarchy, Value: group})
				union := append(append([]string{}, in...), out...)
				sort.Strings(union)
				if strings.Join(union, ",") != strings.Join(everything, ",") {
					t.Fatalf("группа %s: in %v + not_in %v ≠ всё %v", group, in, out, everything)
				}
			}
		})
	})
}
