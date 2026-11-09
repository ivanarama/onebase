package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// База, созданная до #1946, хранит длинные имена по-старому: SQLite — целиком,
// PostgreSQL — обрезанными до 63 байт. Новая миграция обязана найти такую
// таблицу и колонку и переименовать их, а не завести рядом пустые: иначе после
// обновления платформы справочник выглядел бы пустым, а данные лежали бы в
// таблице, которую приложение больше не читает.
//
// Сущность с длинным именем и табличная часть с длинным именем — разные
// объекты: табличная часть длинной сущности на старом PostgreSQL совпадала с
// ней после обрезки, то есть такой старой базы там не могло быть.
func TestMigrateRenamesLegacyLongIdents_Matrix(t *testing.T) {
	catalog := &metadata.Entity{
		Name: "СправочникСОченьДлиннымИменемИзКонфигурации1С",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "РеквизитСОченьДлиннымИменемКоторыйПришёлИзОдинЭс", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name:   "ДлДок",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "ТабличнаяЧастьСОченьДлиннымИменемИзОдинЭс", Fields: []metadata.Field{{Name: "Строчка", Type: metadata.FieldTypeString}}},
		},
	}
	long := catalog.Fields[1]
	tp := doc.TableParts[0]
	oldTable := metadata.TableLogical(catalog.Name)
	oldTP := metadata.TablePartTableLogical(doc.Name, tp.Name)
	oldCol := metadata.LogicalColumnName(long)
	for _, n := range []string{oldTable, oldTP, oldCol} {
		if len(n) <= metadata.MaxSQLIdentBytes {
			t.Fatalf("имя %q должно быть длиннее предела — иначе тест ничего не проверяет", n)
		}
	}

	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		d := db.Dialect()
		// Ровно так таблицы заводила прежняя платформа: имя как есть, а
		// PostgreSQL сам обрезал его до 63 байт.
		for _, ddl := range []string{
			"CREATE TABLE " + oldTable + " (id " + d.TypeUUID() + " PRIMARY KEY, наименование TEXT, " + oldCol + " TEXT)",
			"CREATE TABLE длдок (id " + d.TypeUUID() + " PRIMARY KEY, номер TEXT)",
			"CREATE TABLE " + oldTP + " (id " + d.TypeUUID() + " PRIMARY KEY, parent_id " + d.TypeUUID() + ", строка INTEGER, строчка TEXT)",
		} {
			if _, err := db.Exec(ctx, ddl); err != nil {
				t.Fatalf("старая схема: %v", err)
			}
		}
		catID, docID := uuid.New(), uuid.New()
		p := d.Placeholder
		for _, ins := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO " + oldTable + " (id, наименование, " + oldCol + ") VALUES (" + p(1) + ", " + p(2) + ", " + p(3) + ")",
				[]any{catID.String(), "Элемент", "значение из старой базы"}},
			{"INSERT INTO длдок (id, номер) VALUES (" + p(1) + ", " + p(2) + ")", []any{docID.String(), "Д-1"}},
			{"INSERT INTO " + oldTP + " (id, parent_id, строка, строчка) VALUES (" + p(1) + ", " + p(2) + ", 1, " + p(3) + ")",
				[]any{uuid.New().String(), docID.String(), "строка из старой базы"}},
		} {
			if _, err := db.Exec(ctx, ins.sql, ins.args...); err != nil {
				t.Fatal(err)
			}
		}

		entities := []*metadata.Entity{catalog, doc}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatalf("миграция старой базы: %v", err)
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatalf("повторная миграция: %v", err)
		}

		row, err := db.GetByID(ctx, catalog.Name, catID, catalog)
		if err != nil {
			t.Fatalf("объект старой базы не читается: %v", err)
		}
		if row[long.Name] != "значение из старой базы" {
			t.Fatalf("данные длинной колонки потеряны: %v", row)
		}
		rows, err := db.GetTablePartRows(ctx, doc.Name, tp.Name, docID, tp)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0]["Строчка"] != "строка из старой базы" {
			t.Fatalf("строки табличной части потеряны: %v", rows)
		}
		// Старые таблицы не остались рядом пустыми дублями.
		for _, old := range []string{oldTable, oldTP} {
			for _, name := range metadata.LegacySQLIdents(old) {
				if exists, err := db.TableExists(ctx, name); err != nil || exists {
					t.Fatalf("таблица под прежним именем %q осталась (err=%v)", name, err)
				}
			}
		}
		if !strings.HasPrefix(metadata.TableName(catalog.Name), "справочниксоченьдлинным") {
			t.Fatalf("короткое имя потеряло узнаваемое начало: %s", metadata.TableName(catalog.Name))
		}
	})
}
