package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Имена из 1С длиннее предела PostgreSQL (63 байта ≈ 31 кириллическая буква) —
// #1946. Форма взята из реальной конфигурации: справочник, чьё имя само длиннее
// предела, с табличными частями, которые после обрезки совпадали с ним и между
// собой, и два реквизита с общими первыми 31 буквой. До починки PostgreSQL не
// создавал такую схему вовсе («column … specified more than once»), а SQLite
// принимала её молча.
func longIdentEntities() (*metadata.Entity, *metadata.Entity) {
	warehouse := &metadata.Entity{
		Name:   "ДлСклад",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	ref := "reference:" + warehouse.Name
	structure := &metadata.Entity{
		Name: "А_СтруктураИсточниковДляНомераТелефона",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ИспользоватьКоэффициентИзмененияЦенМастера", Type: metadata.FieldTypeBool},
			{Name: "ИспользоватьКоэффициентИзмененияЦенВРемонт", Type: metadata.FieldTypeBool},
			{Name: "СкладДляВыдачиВПроизводствоПоГарантийнымЗаявкам", Type: metadata.FieldType(ref), RefEntity: warehouse.Name},
		},
		TableParts: []metadata.TablePart{
			{Name: "СерияДК", Fields: []metadata.Field{{Name: "Серия", Type: metadata.FieldTypeString}}},
			{Name: "РекламаПовторы", Fields: []metadata.Field{{Name: "Реклама", Type: metadata.FieldTypeString}}},
		},
	}
	return warehouse, structure
}

func TestLongIdents_Matrix(t *testing.T) {
	warehouse, structure := longIdentEntities()
	entities := []*metadata.Entity{warehouse, structure}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatalf("схема с длинными именами не создана: %v", err)
		}
		// Миграция идёт при каждом запуске — повтор не должен ни падать, ни
		// заводить вторую таблицу или колонку.
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatalf("повторная миграция: %v", err)
		}

		whID := uuid.New()
		if err := db.Upsert(ctx, warehouse.Name, whID, map[string]any{"Наименование": "Главный"}, warehouse); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := db.Upsert(ctx, structure.Name, id, map[string]any{
			"Наименование": "4951983100",
			"ИспользоватьКоэффициентИзмененияЦенМастера":      true,
			"ИспользоватьКоэффициентИзмененияЦенВРемонт":      false,
			"СкладДляВыдачиВПроизводствоПоГарантийнымЗаявкам": whID,
		}, structure); err != nil {
			t.Fatalf("запись объекта: %v", err)
		}
		row, err := db.GetByID(ctx, structure.Name, id, structure)
		if err != nil {
			t.Fatal(err)
		}
		// Два реквизита с общим началом — две разные колонки: значения не
		// перепутаны и не слиты в одно.
		if !truthy(row["ИспользоватьКоэффициентИзмененияЦенМастера"]) || truthy(row["ИспользоватьКоэффициентИзмененияЦенВРемонт"]) {
			t.Fatalf("реквизиты с общим началом смешались: мастера=%v, вРемонт=%v",
				row["ИспользоватьКоэффициентИзмененияЦенМастера"], row["ИспользоватьКоэффициентИзмененияЦенВРемонт"])
		}

		// Табличные части с общим началом имени — разные таблицы.
		dk, ads := structure.TableParts[0], structure.TableParts[1]
		if err := db.UpsertTablePartRows(ctx, structure.Name, dk.Name, id, []map[string]any{{"Серия": "ДК-1"}}, dk); err != nil {
			t.Fatalf("строки %s: %v", dk.Name, err)
		}
		if err := db.UpsertTablePartRows(ctx, structure.Name, ads.Name, id, []map[string]any{{"Реклама": "Авито"}, {"Реклама": "Карусель"}}, ads); err != nil {
			t.Fatalf("строки %s: %v", ads.Name, err)
		}
		dkRows, err := db.GetTablePartRows(ctx, structure.Name, dk.Name, id, dk)
		if err != nil {
			t.Fatal(err)
		}
		adRows, err := db.GetTablePartRows(ctx, structure.Name, ads.Name, id, ads)
		if err != nil {
			t.Fatal(err)
		}
		if len(dkRows) != 1 || len(adRows) != 2 {
			t.Fatalf("табличные части слились: %s=%d строк, %s=%d строк", dk.Name, len(dkRows), ads.Name, len(adRows))
		}

		// Запрос: колонки и псевдоним длиннее предела возвращаются под
		// полными именами — так модуль читает Выборка.<Поле>.
		res, err := query.Compile(`ВЫБРАТЬ
			С.ИспользоватьКоэффициентИзмененияЦенМастера,
			С.ИспользоватьКоэффициентИзмененияЦенВРемонт КАК ПризнакИспользованияКоэффициентаВРемонтныхЗаявках,
			С.СкладДляВыдачиВПроизводствоПоГарантийнымЗаявкам.Наименование КАК Склад
		ИЗ Справочник.А_СтруктураИсточниковДляНомераТелефона КАК С
		ГДЕ С.ИспользоватьКоэффициентИзмененияЦенМастера = ИСТИНА`,
			query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, cols, err := query.Run(ctx, db, &res)
		if err != nil {
			t.Fatalf("выполнение: %v\nSQL: %s", err, res.SQL)
		}
		if len(rows) != 1 {
			t.Fatalf("строк %d, ожидалась 1\nSQL: %s", len(rows), res.SQL)
		}
		got := rows[0]
		if v, ok := got[strings.ToLower("ИспользоватьКоэффициентИзмененияЦенМастера")]; !ok || !truthy(v) {
			t.Fatalf("колонка под полным именем не найдена или неверна: %v (колонки %v)", got, cols)
		}
		if v, ok := got[strings.ToLower("ПризнакИспользованияКоэффициентаВРемонтныхЗаявках")]; !ok || truthy(v) {
			t.Fatalf("длинный псевдоним не вернулся полным: %v (колонки %v)", got, cols)
		}
		if got["склад"] != "Главный" {
			t.Fatalf("ссылка с длинным именем не разыменована: %v", got)
		}
		if len(res.LongIdents) == 0 {
			t.Fatalf("компилятор не сократил ни одного имени — тест не проверяет то, ради чего написан\nSQL: %s", res.SQL)
		}
		for _, c := range cols {
			if full, short := res.LongIdents[c]; short && full != c {
				t.Fatalf("в списке колонок осталось короткое имя %q", c)
			}
		}

		// Без псевдонимов: метку даёт само физическое имя колонки, которое в
		// SQL уже короткое. Полное имя обязано вернуться и здесь.
		star, err := query.Compile(`ВЫБРАТЬ * ИЗ Справочник.А_СтруктураИсточниковДляНомераТелефона`,
			query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
		if err != nil {
			t.Fatalf("компиляция *: %v", err)
		}
		starRows, starCols, err := query.Run(ctx, db, &star)
		if err != nil {
			t.Fatalf("выполнение *: %v\nSQL: %s", err, star.SQL)
		}
		if len(starRows) != 1 {
			t.Fatalf("ВЫБРАТЬ *: строк %d", len(starRows))
		}
		for _, f := range structure.Fields {
			logical := metadata.LogicalColumnName(f)
			if _, ok := starRows[0][logical]; !ok {
				t.Fatalf("ВЫБРАТЬ *: нет колонки %q (колонки %v)", logical, starCols)
			}
		}
	})
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case decimal.Decimal:
		return !x.IsZero()
	}
	return false
}
