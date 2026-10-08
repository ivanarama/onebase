package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Системные колонки документа и справочника (#1436). В таблице они лежат
// физическими именами (posted, deletion_mark), а в языке запросов пишутся
// по-русски — как Период у регистра. Класс источника документ от справочника не
// отличает, поэтому алиас разрешается по фактическим метаданным.
//
// Тест матричный: проверяется ИСПОЛНЕНИЕ полученного SQL. Ошибка здесь не
// падает на компиляции, а молча меняет результат запроса, а через этот
// компилятор идут отчёты, виджеты и прикладные модули.
func documentSystemColumnEntities() []*metadata.Entity {
	return []*metadata.Entity{
		{Name: "ПродажаСК", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		}},
		{Name: "КлиентСК", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		}},
		// Документ с СОБСТВЕННЫМ реквизитом Проведен: алиас не имеет права его
		// перекрыть, иначе правка молча поменяет смысл работающего запроса.
		{Name: "ОсобыйСК", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Проведен", Type: metadata.FieldTypeString},
		}},
	}
}

func TestDocumentSystemColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := documentSystemColumnEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		sale, catalog, special := ents[0], ents[1], ents[2]

		postedID, draftID := uuid.New(), uuid.New()
		if err := db.Upsert(ctx, sale.Name, postedID, map[string]any{"Номер": "П-1", "Сумма": "100"}, sale); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, sale.Name, draftID, map[string]any{"Номер": "Ч-1", "Сумма": "300"}, sale); err != nil {
			t.Fatal(err)
		}
		if err := db.SetPosted(ctx, sale.Name, postedID, true); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, catalog.Name, uuid.New(), map[string]any{"Наименование": "Живой"}, catalog); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, special.Name, uuid.New(),
			map[string]any{"Номер": "О-1", "Проведен": "вручную"}, special); err != nil {
			t.Fatal(err)
		}

		compile := func(src string) (query.Result, error) {
			return query.Compile(src, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
		}
		rowsOf := func(t *testing.T, src string) []string {
			t.Helper()
			res, err := compile(src)
			if err != nil {
				t.Fatalf("компиляция: %v\nЗапрос: %s", err, src)
			}
			rows, err := db.Query(ctx, res.SQL, res.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, res.SQL)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var v any
				if err := rows.Scan(&v); err != nil {
					t.Fatalf("скан: %v\nSQL: %s", err, res.SQL)
				}
				if b, ok := v.([]byte); ok {
					out = append(out, string(b))
					continue
				}
				out = append(out, strings.TrimSpace(strings.Trim(stringOf(v), " ")))
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("выдача: %v\nSQL: %s", err, res.SQL)
			}
			return out
		}

		t.Run("неквалифицированный Проведен", func(t *testing.T) {
			got := rowsOf(t, `ВЫБРАТЬ Номер ИЗ Документ.ПродажаСК ГДЕ Проведен = Истина`)
			if len(got) != 1 || got[0] != "П-1" {
				t.Fatalf("получено %v, ждали [П-1]", got)
			}
		})

		t.Run("квалифицированный Проведен", func(t *testing.T) {
			got := rowsOf(t, `ВЫБРАТЬ Д.Номер ИЗ Документ.ПродажаСК КАК Д ГДЕ Д.Проведен = Истина`)
			if len(got) != 1 || got[0] != "П-1" {
				t.Fatalf("получено %v, ждали [П-1]", got)
			}
		})

		t.Run("ПометкаУдаления у справочника", func(t *testing.T) {
			got := rowsOf(t, `ВЫБРАТЬ Наименование ИЗ Справочник.КлиентСК ГДЕ ПометкаУдаления = Ложь`)
			if len(got) != 1 || got[0] != "Живой" {
				t.Fatalf("получено %v, ждали [Живой]", got)
			}
		})

		t.Run("ОБЪЕДИНИТЬ: обе ветви свои", func(t *testing.T) {
			got := rowsOf(t, `ВЫБРАТЬ Номер ИЗ Документ.ПродажаСК ГДЕ Проведен = Истина
				ОБЪЕДИНИТЬ ВСЕ
				ВЫБРАТЬ Номер ИЗ Документ.ПродажаСК ГДЕ Проведен = Ложь`)
			if len(got) != 2 {
				t.Fatalf("получено %v, ждали обе строки", got)
			}
		})

		t.Run("подзапрос", func(t *testing.T) {
			got := rowsOf(t, `ВЫБРАТЬ Номер ИЗ Документ.ПродажаСК
				ГДЕ Ссылка В (ВЫБРАТЬ Ссылка ИЗ Документ.ПродажаСК ГДЕ Проведен = Истина)`)
			if len(got) != 1 || got[0] != "П-1" {
				t.Fatalf("получено %v, ждали [П-1]", got)
			}
		})

		t.Run("собственный реквизит важнее алиаса", func(t *testing.T) {
			got := rowsOf(t, `ВЫБРАТЬ Проведен ИЗ Документ.ОсобыйСК`)
			if len(got) != 1 || got[0] != "вручную" {
				t.Fatalf("получено %v, ждали [вручную]: алиас перекрыл собственный реквизит", got)
			}
		})

		t.Run("у справочника Проведена нет", func(t *testing.T) {
			res, err := compile(`ВЫБРАТЬ Наименование ИЗ Справочник.КлиентСК ГДЕ Проведен = Истина`)
			if err != nil {
				return // отказ на компиляции — тоже приемлемый исход
			}
			rows, qerr := db.Query(ctx, res.SQL, res.Args...)
			if qerr == nil {
				for rows.Next() {
				}
				rows.Close()
				qerr = rows.Err()
			}
			if qerr == nil {
				t.Fatalf("справочник принял Проведен: SQL %s", res.SQL)
			}
			if strings.Contains(res.SQL, "posted") {
				t.Fatalf("алиас документа применён к справочнику: %s", res.SQL)
			}
		})

		// Прикладной код уже пишет `Пуб.posted КАК Проведен`: имя после КАК —
		// объявляемый алиас вывода, а не колонка. Перевести его значило бы
		// вернуть `AS posted` и сломать чтение Строка.Проведен.
		t.Run("имя после КАК остаётся алиасом вывода", func(t *testing.T) {
			res, err := compile(`ВЫБРАТЬ Пуб.posted КАК Проведен ИЗ Документ.ПродажаСК КАК Пуб
				УПОРЯДОЧИТЬ ПО Проведен`)
			if err != nil {
				t.Fatalf("компиляция: %v", err)
			}
			if !strings.Contains(res.SQL, "AS проведен") {
				t.Fatalf("алиас вывода переведён в системную колонку: %s", res.SQL)
			}
			rows, err := db.Query(ctx, res.SQL, res.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, res.SQL)
			}
			defer rows.Close()
			if names := rows.FieldNames(); len(names) != 1 || names[0] != "проведен" {
				t.Fatalf("колонки выдачи %v, ждали [проведен]\nSQL: %s", names, res.SQL)
			}
		})

		t.Run("МАКС и МИН", func(t *testing.T) {
			res, err := compile(`ВЫБРАТЬ МАКС(Сумма) КАК Б, МИН(Сумма) КАК М ИЗ Документ.ПродажаСК`)
			if err != nil {
				t.Fatalf("компиляция: %v", err)
			}
			if !strings.Contains(res.SQL, "MAX(") || !strings.Contains(res.SQL, "MIN(") {
				t.Fatalf("МАКС/МИН не развернулись: %s", res.SQL)
			}
			rows, err := db.Query(ctx, res.SQL, res.Args...)
			if err != nil {
				t.Fatalf("исполнение: %v\nSQL: %s", err, res.SQL)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatalf("пустая выдача: %s", res.SQL)
			}
		})
	})
}

func stringOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}
