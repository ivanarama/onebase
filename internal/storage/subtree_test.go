package storage_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// IDsInSubtree: сама группа и всё вложенное на любую глубину; чужие ветки,
// корневые элементы и замкнутый цикл parent_id — без зависания.
func TestIDsInSubtreeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := seedChoiceParentFixture(t, db)
		names := map[string]string{
			f.tech.String(): "Техника", f.kitchen.String(): "Кухня", f.stoves.String(): "Плиты", f.other.String(): "Прочее",
			f.stove.String(): "плита", f.kettle.String(): "чайник", f.iron.String(): "утюг", f.lamp.String(): "лампа",
			f.orphan.String(): "корневой", f.cycleA.String(): "цикл А", f.cycleB.String(): "цикл Б",
		}
		all := []uuid.UUID{f.tech, f.kitchen, f.stoves, f.other, f.stove, f.kettle, f.iron, f.lamp, f.orphan, f.cycleA, f.cycleB}
		check := func(root uuid.UUID, want string) {
			t.Helper()
			got, err := db.IDsInSubtree(context.Background(), f.groups, root, all)
			if err != nil {
				t.Fatal(err)
			}
			var list []string
			for id := range got {
				list = append(list, names[id])
			}
			sort.Strings(list)
			if strings.Join(list, ",") != want {
				t.Fatalf("поддерево %s: %v, ожидалось %s", names[root.String()], list, want)
			}
		}
		check(f.kitchen, "Кухня,Плиты,плита,чайник")
		check(f.tech, "Кухня,Плиты,Техника,плита,утюг,чайник")
		check(f.cycleA, "цикл А,цикл Б")
	})
}

// FolderIDsByName: только группы и только точное наименование — элемент с тем
// же именем и группа с похожим не попадают.
func TestFolderIDsByNameMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := seedChoiceParentFixture(t, db)
		ctx := context.Background()
		got, err := db.FolderIDsByName(ctx, f.groups, "Кухня")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != f.kitchen {
			t.Fatalf("Кухня: %v, ожидалась одна группа %s", got, f.kitchen)
		}
		for _, name := range []string{"плита", "Кух", "кухня ", "нет такой"} {
			got, err := db.FolderIDsByName(ctx, f.groups, name)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("%q: %v, ожидалось пусто", name, got)
			}
		}
	})
}
