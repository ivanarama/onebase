package query_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// ПЕРВЫЕ, ИМЕЮЩИЕ и ЕстьNULL меняют исполняемый SQL, поэтому одной проверки
// текста недостаточно: прогоняем один и тот же публичный Compile на обеих СУБД.
func TestFirstHavingCoalesceMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name: "ПервыеМатрица",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Категория", Type: metadata.FieldTypeString},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
				{Name: "Комментарий", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, item := range []map[string]any{
			{"Наименование": "A1", "Категория": "A", "Сумма": 2},
			{"Наименование": "A2", "Категория": "A", "Сумма": 2, "Комментарий": "есть"},
			{"Наименование": "B1", "Категория": "B", "Сумма": 1},
		} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), item, ent); err != nil {
				t.Fatalf("запись: %v", err)
			}
		}

		first, err := query.Compile(
			`ВЫБРАТЬ ПЕРВЫЕ 2 Наименование ИЗ Справочник.ПервыеМатрица УПОРЯДОЧИТЬ ПО Наименование`,
			query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()},
		)
		if err != nil {
			t.Fatalf("компиляция ПЕРВЫЕ: %v", err)
		}
		rows, err := db.Query(ctx, first.SQL, first.Args...)
		if err != nil {
			t.Fatalf("исполнение ПЕРВЫЕ: %v\nSQL: %s", err, first.SQL)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("скан ПЕРВЫЕ: %v", err)
			}
			names = append(names, name)
		}
		rows.Close()
		if len(names) != 2 || names[0] != "A1" || names[1] != "A2" {
			t.Fatalf("ПЕРВЫЕ вернуло %v, ожидалось [A1 A2]", names)
		}

		having, err := query.Compile(
			`ВЫБРАТЬ Категория ИЗ Справочник.ПервыеМатрица СГРУППИРОВАТЬ ПО Категория ИМЕЮЩИЕ СУММА(Сумма) > 3`,
			query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()},
		)
		if err != nil {
			t.Fatalf("компиляция ИМЕЮЩИЕ: %v", err)
		}
		var category string
		if err := db.QueryRow(ctx, having.SQL, having.Args...).Scan(&category); err != nil {
			t.Fatalf("исполнение ИМЕЮЩИЕ: %v\nSQL: %s", err, having.SQL)
		}
		if category != "A" {
			t.Fatalf("ИМЕЮЩИЕ вернуло категорию %q, ожидалась A", category)
		}

		coalesce, err := query.Compile(
			`ВЫБРАТЬ ЕстьNULL(Комментарий, "нет") ИЗ Справочник.ПервыеМатрица ГДЕ Наименование = "A1"`,
			query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()},
		)
		if err != nil {
			t.Fatalf("компиляция ЕстьNULL: %v", err)
		}
		var comment string
		if err := db.QueryRow(ctx, coalesce.SQL, coalesce.Args...).Scan(&comment); err != nil {
			t.Fatalf("исполнение ЕстьNULL: %v\nSQL: %s", err, coalesce.SQL)
		}
		if comment != "нет" {
			t.Fatalf("ЕстьNULL вернуло %q, ожидалось нет", comment)
		}
	})
}

// «ПЕРВЫЕ 0» обязано отдавать пустой результат, а не падать и не игнорировать
// лимит. LIMIT 0 понимают оба диалекта, но проверяем исполнением: молча
// вернувшиеся все строки были бы хуже ошибки компиляции, которую мы сняли (#741).
func TestFirstZeroMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name:   "ПервыеНольМатрица",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, name := range []string{"A1", "A2", "B1"} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"Наименование": name}, ent); err != nil {
				t.Fatalf("запись: %v", err)
			}
		}

		res, err := query.Compile(
			`ВЫБРАТЬ ПЕРВЫЕ 0 Наименование ИЗ Справочник.ПервыеНольМатрица УПОРЯДОЧИТЬ ПО Наименование`,
			query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()},
		)
		if err != nil {
			t.Fatalf("компиляция ПЕРВЫЕ 0: %v", err)
		}
		rows, err := db.Query(ctx, res.SQL, res.Args...)
		if err != nil {
			t.Fatalf("исполнение ПЕРВЫЕ 0: %v\nSQL: %s", err, res.SQL)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
		}
		if n != 0 {
			t.Fatalf("ПЕРВЫЕ 0 вернуло %d строк, ожидалось 0\nSQL: %s", n, res.SQL)
		}
	})
}
