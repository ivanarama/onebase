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

// choice_filter по табличной части выбираемого справочника (#1822): запись
// подходит, если в её ТЧ есть хотя бы одна строка с нужной ссылкой. Матрица
// SQLite/PostgreSQL: EXISTS по таблице части обязан одинаково работать на
// обоих диалектах, не задваивать запись с двумя подходящими строками ни в
// выдаче, ни в total и сочетаться с обычным условием по шапке через AND.
func TestChoiceFilterTablePartMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		direction := &metadata.Entity{
			Name: "ChoiceTPDir", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		brand := &metadata.Entity{
			Name: "ChoiceTPBrand", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Активен", Type: metadata.FieldTypeBool},
			},
			TableParts: []metadata.TablePart{{
				Name: "Направления",
				Fields: []metadata.Field{
					{Name: "Направление", Type: metadata.FieldType("reference:ChoiceTPDir"), RefEntity: "ChoiceTPDir"},
					{Name: "Комментарий", Type: metadata.FieldTypeString},
				},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{direction, brand}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		fridge, sewing := uuid.New(), uuid.New()
		for id, name := range map[uuid.UUID]string{fridge: "ХД", sewing: "ШМ"} {
			if err := db.Upsert(ctx, direction.Name, id, map[string]any{"Наименование": name}, direction); err != nil {
				t.Fatalf("seed direction: %v", err)
			}
		}
		for _, b := range []struct {
			name   string
			active bool
			dirs   []uuid.UUID
		}{
			{"Холодильный", true, []uuid.UUID{fridge}},
			{"Швейный", true, []uuid.UUID{sewing}},
			// Две строки с одним и тем же направлением и одна с другим: запись
			// обязана прийти один раз.
			{"Универсальный", true, []uuid.UUID{fridge, sewing, fridge}},
			{"Неактивный холодильный", false, []uuid.UUID{fridge}},
			{"Без направлений", true, nil},
		} {
			id := uuid.New()
			if err := db.Upsert(ctx, brand.Name, id, map[string]any{"Наименование": b.name, "Активен": b.active}, brand); err != nil {
				t.Fatalf("seed brand %s: %v", b.name, err)
			}
			rows := make([]map[string]any, 0, len(b.dirs))
			for _, d := range b.dirs {
				rows = append(rows, map[string]any{"Направление": d.String()})
			}
			if len(rows) > 0 {
				if err := db.UpsertTablePartRows(ctx, brand.Name, "Направления", id, rows, brand.TableParts[0]); err != nil {
					t.Fatalf("seed table part of %s: %v", b.name, err)
				}
			}
		}

		list := func(t *testing.T, predicates ...storage.ChoicePredicate) (string, int) {
			t.Helper()
			params := storage.ListParams{ChoicePredicates: predicates}
			rows, err := db.List(ctx, brand.Name, brand, params)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			total, err := db.CountList(ctx, brand.Name, brand, params)
			if err != nil {
				t.Fatalf("CountList: %v", err)
			}
			names := make([]string, 0, len(rows))
			for _, r := range rows {
				names = append(names, strings.TrimSpace(r["Наименование"].(string)))
			}
			sort.Strings(names)
			return strings.Join(names, ", "), total
		}

		byDir := func(id uuid.UUID) storage.ChoicePredicate {
			return storage.ChoicePredicate{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Value: id}
		}
		if got, total := list(t, byDir(fridge)); got != "Неактивный холодильный, Универсальный, Холодильный" || total != 3 {
			t.Errorf("ХД: %q, total %d", got, total)
		}
		if got, total := list(t, byDir(sewing)); got != "Универсальный, Швейный" || total != 2 {
			t.Errorf("ШМ: %q, total %d", got, total)
		}
		// Регистр имён ТЧ и колонки не важен — как у реквизитов шапки.
		lower := storage.ChoicePredicate{Field: "направления.НАПРАВЛЕНИЕ", Op: metadata.FormChoiceOpEqual, Value: sewing.String()}
		if got, _ := list(t, lower); got != "Универсальный, Швейный" {
			t.Errorf("регистр имён: %q", got)
		}
		active := true
		if got, total := list(t, byDir(fridge), storage.ChoicePredicate{Field: "Активен", Op: metadata.FormChoiceOpEqual, Value: active}); got != "Универсальный, Холодильный" || total != 2 {
			t.Errorf("ХД AND Активен: %q, total %d", got, total)
		}
		if got, total := list(t, byDir(uuid.New())); got != "" || total != 0 {
			t.Errorf("несуществующее направление: %q, total %d", got, total)
		}
	})
}

// Закрытая грамматика: всё, кроме eq по ссылочной колонке существующей ТЧ, —
// ошибка запроса, а не тихое условие без отбора.
func TestChoiceFilterTablePartRejects(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		direction := &metadata.Entity{Name: "ChoiceTPRejDir", Kind: metadata.KindCatalog, Hierarchical: true}
		brand := &metadata.Entity{
			Name: "ChoiceTPRejBrand", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{{
				Name: "Направления",
				Fields: []metadata.Field{
					{Name: "Направление", Type: metadata.FieldType("reference:ChoiceTPRejDir"), RefEntity: "ChoiceTPRejDir"},
					{Name: "Комментарий", Type: metadata.FieldTypeString},
				},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{direction, brand}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		id := uuid.New()
		for name, predicate := range map[string]storage.ChoicePredicate{
			"нет ТЧ":            {Field: "Цены.Направление", Op: metadata.FormChoiceOpEqual, Value: id},
			"нет колонки":       {Field: "Направления.Бренд", Op: metadata.FormChoiceOpEqual, Value: id},
			"строковая колонка": {Field: "Направления.Комментарий", Op: metadata.FormChoiceOpEqual, Value: "x"},
			"in_hierarchy":      {Field: "Направления.Направление", Op: metadata.FormChoiceOpInHierarchy, Value: id},
			"eq_or_empty":       {Field: "Направления.Направление", Op: metadata.FormChoiceOpEqualOrEmpty, Value: id},
			"пустое значение":   {Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Value: nil},
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := db.List(ctx, brand.Name, brand, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{predicate}}); err == nil {
					t.Fatalf("условие %+v принято", predicate)
				}
			})
		}
	})
}

// Имя каталога не должно сталкиваться с тем, как SQL адресует строки его ТЧ:
// при фиксированном алиасе «choice_tp» каталог с таким именем затенялся, и
// корреляция parent_id = id сравнивала поля одной строки ТЧ — подходящая
// запись пропадала из выдачи и total (#1917, ревью круг 1).
func TestChoiceFilterTablePartCatalogNamedLikeAlias(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		direction := &metadata.Entity{Name: "choice_tp_dir", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "name", Type: metadata.FieldTypeString}}}
		brand := &metadata.Entity{
			Name: "choice_tp", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "name", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{{Name: "directions", Fields: []metadata.Field{
				{Name: "direction", Type: metadata.FieldType("reference:choice_tp_dir"), RefEntity: "choice_tp_dir"},
			}}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{direction, brand}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		dir, id := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, direction.Name, dir, map[string]any{"name": "d"}, direction); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, brand.Name, id, map[string]any{"name": "b"}, brand); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertTablePartRows(ctx, brand.Name, "directions", id, []map[string]any{{"direction": dir.String()}}, brand.TableParts[0]); err != nil {
			t.Fatal(err)
		}
		params := storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
			{Field: "directions.direction", Op: metadata.FormChoiceOpEqual, Value: dir},
		}}
		rows, err := db.List(ctx, brand.Name, brand, params)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		total, err := db.CountList(ctx, brand.Name, brand, params)
		if err != nil {
			t.Fatalf("CountList: %v", err)
		}
		if len(rows) != 1 || total != 1 {
			t.Fatalf("каталог choice_tp: строк %d, total %d, ожидалось 1/1", len(rows), total)
		}
	})
}
