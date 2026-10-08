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

// parent_id — служебная ссылка иерархического справочника на самого себя
// (#1819, вариант 1). Дерево товарных групп:
//
//	Техника (папка)
//	├── Кухня (папка)
//	│   ├── Плиты (папка)
//	│   │   └── плита
//	│   └── чайник
//	└── утюг
//	Прочее (папка)
//	└── лампа
//	корневой элемент
//
// Плюс две папки, ссылающиеся друг на друга, — цикл, какой оставляет битая
// загрузка: построение поддерева обязано завершаться.
type choiceParentFixture struct {
	groups                            *metadata.Entity
	tech, kitchen, stoves, other      uuid.UUID
	stove, kettle, iron, lamp, orphan uuid.UUID
	cycleA, cycleB                    uuid.UUID
}

func seedChoiceParentFixture(t *testing.T, db *storage.DB) choiceParentFixture {
	t.Helper()
	ctx := context.Background()
	groups := &metadata.Entity{
		Name: "ChoiceGroup", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{groups}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	f := choiceParentFixture{
		groups: groups,
		tech:   choiceTestUUID("000000001901"), kitchen: choiceTestUUID("000000001902"),
		stoves: choiceTestUUID("000000001903"), other: choiceTestUUID("000000001904"),
		stove: choiceTestUUID("000000001911"), kettle: choiceTestUUID("000000001912"),
		iron: choiceTestUUID("000000001913"), lamp: choiceTestUUID("000000001914"),
		orphan: choiceTestUUID("000000001915"),
		cycleA: choiceTestUUID("000000001921"), cycleB: choiceTestUUID("000000001922"),
	}
	for _, row := range []struct {
		id     uuid.UUID
		name   string
		parent uuid.UUID
		folder bool
	}{
		{f.tech, "Техника", uuid.Nil, true},
		{f.kitchen, "Кухня", f.tech, true},
		{f.stoves, "Плиты", f.kitchen, true},
		{f.other, "Прочее", uuid.Nil, true},
		{f.stove, "плита", f.stoves, false},
		{f.kettle, "чайник", f.kitchen, false},
		{f.iron, "утюг", f.tech, false},
		{f.lamp, "лампа", f.other, false},
		{f.orphan, "корневой элемент", uuid.Nil, false},
		{f.cycleA, "цикл А", uuid.Nil, true},
		{f.cycleB, "цикл Б", f.cycleA, true},
	} {
		fields := map[string]any{"Наименование": row.name, "ЭтоГруппа": row.folder}
		if row.parent != uuid.Nil {
			fields["Родитель"] = row.parent.String()
		}
		if err := db.Upsert(ctx, groups.Name, row.id, fields, groups); err != nil {
			t.Fatalf("seed %s: %v", row.name, err)
		}
	}
	// Замыкаем цикл: у «цикл А» родитель «цикл Б». Upsert такую запись может
	// отвергнуть, поэтому — прямым UPDATE, как её и оставляет битая загрузка.
	if _, err := db.Exec(ctx, "UPDATE "+metadata.TableName(groups.Name)+" SET parent_id = "+db.Dialect().Placeholder(1)+
		" WHERE id = "+db.Dialect().Placeholder(2), storageUUIDArg(db, f.cycleB), storageUUIDArg(db, f.cycleA)); err != nil {
		t.Fatalf("close cycle: %v", err)
	}
	return f
}

// storageUUIDArg — идентификатор в виде, который принимает колонка id диалекта.
func storageUUIDArg(db *storage.DB, id uuid.UUID) any {
	if db.Dialect().Name() == "postgres" {
		return id
	}
	return id.String()
}

func choiceParentNames(t *testing.T, db *storage.DB, f choiceParentFixture, predicates ...storage.ChoicePredicate) []string {
	t.Helper()
	params := storage.ListParams{ChoicePredicates: predicates}
	rows, err := db.List(context.Background(), f.groups.Name, f.groups, params)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	total, err := db.CountList(context.Background(), f.groups.Name, f.groups, params)
	if err != nil {
		t.Fatalf("CountList: %v", err)
	}
	names := choiceRowNames(rows)
	sort.Strings(names)
	if total != len(names) {
		t.Fatalf("CountList=%d, а строк %d: %v", total, len(names), names)
	}
	return names
}

func TestChoiceFilterParentIDMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := seedChoiceParentFixture(t, db)
		want := func(names ...string) string {
			sort.Strings(names)
			return strings.Join(names, ",")
		}

		t.Run("in_hierarchy — строго внутри группы, сама группа не входит", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Value: f.tech})
			if strings.Join(got, ",") != want("Кухня", "Плиты", "плита", "чайник", "утюг") {
				t.Fatalf("поддерево «Техники» = %v", got)
			}
		})

		t.Run("in_hierarchy с is_folder=false — только элементы поддерева", func(t *testing.T) {
			got := choiceParentNames(t, db, f,
				storage.ChoicePredicate{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Value: f.tech},
				storage.ChoicePredicate{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false})
			if strings.Join(got, ",") != want("плита", "чайник", "утюг") {
				t.Fatalf("элементы «Техники» = %v", got)
			}
		})

		t.Run("eq — только непосредственные дети", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpEqual, Value: f.tech})
			if strings.Join(got, ",") != want("Кухня", "утюг") {
				t.Fatalf("дети «Техники» = %v", got)
			}
		})

		t.Run("лист дерева — пустая выдача", func(t *testing.T) {
			if got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Value: f.stove}); len(got) != 0 {
				t.Fatalf("внутри элемента нашлись записи: %v", got)
			}
		})

		t.Run("цикл в иерархии — обход завершается", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Value: f.cycleA})
			if strings.Join(got, ",") != want("цикл А", "цикл Б") {
				t.Fatalf("поддерево цикла = %v", got)
			}
		})

		// eq_or_empty из #1781 применим и к parent_id: «пусто» — корень.
		// Колонка — служебная parent_id, а не имя синтетического реквизита:
		// ошибка колонки дала бы SQL-ошибку либо чужие строки.
		t.Run("eq_or_empty — дети группы и корневые записи", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpEqualOrEmpty, Value: f.tech})
			if strings.Join(got, ",") != want("Кухня", "утюг", "Техника", "Прочее", "корневой элемент") {
				t.Fatalf("дети «Техники» и корни = %v", got)
			}
		})

		t.Run("eq_or_empty без источника — только корневые записи", func(t *testing.T) {
			got := choiceParentNames(t, db, f, storage.ChoicePredicate{
				Field: "parent_id", Op: metadata.FormChoiceOpEqualOrEmpty, Value: nil})
			if strings.Join(got, ",") != want("Техника", "Прочее", "корневой элемент") {
				t.Fatalf("корни = %v", got)
			}
		})

		t.Run("корневые записи не проходят никакой отбор", func(t *testing.T) {
			for _, root := range []uuid.UUID{f.tech, f.other} {
				for _, op := range []metadata.FormChoiceOperator{metadata.FormChoiceOpEqual, metadata.FormChoiceOpInHierarchy} {
					for _, name := range choiceParentNames(t, db, f, storage.ChoicePredicate{Field: "parent_id", Op: op, Value: root}) {
						if name == "Техника" || name == "Прочее" || name == "корневой элемент" {
							t.Fatalf("%s %s: корневая запись %q в выдаче", op, root, name)
						}
					}
				}
			}
		})
	})
}

// parent_id — поле только иерархического справочника: у обычного его нет.
func TestChoiceFilterParentIDRequiresHierarchy(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		plain := &metadata.Entity{
			Name: "ChoicePlain", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{plain}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		_, err := db.List(ctx, plain.Name, plain, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
			{Field: "parent_id", Op: metadata.FormChoiceOpEqual, Value: uuid.New()},
		}})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("parent_id у неиерархического справочника: err=%v", err)
		}
	})
}
