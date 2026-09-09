package query_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Строковые функции языка запросов (ПОДСТРОКА/ДЛИНАСТРОКИ/ЛЕВ/ПРАВ/ВРЕГ/НРЕГ/
// СОКРЛ/СОКРП/СОКРЛП) и оператор ПОДОБНО. Тест матричный, потому что проверяет
// ИСПОЛНЕНИЕ, а не текст SQL: у диалектов разные встроенные функции, и
// расхождение видно только на данных. Данные — кириллица: встроенные
// UPPER/LOWER в SQLite меняют регистр только ASCII, а substr, считающий байты,
// разрезал бы русскую букву пополам.
func stringFuncEntities() []*metadata.Entity {
	return []*metadata.Entity{{
		Name: "КлиентСтр",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}}
}

func seedStringFuncs(t *testing.T, ctx context.Context, db *storage.DB, ents []*metadata.Entity) {
	t.Helper()
	e := ents[0]
	rows := []map[string]any{
		{"Наименование": "Ленина", "Телефон": "+7 (999) 111-22-33"},
		{"Наименование": "  Гагарина  ", "Телефон": "8 999 444-55-66"},
		{"Наименование": "Скидка 10%", "Телефон": "служебный"},
	}
	for _, r := range rows {
		if err := db.Upsert(ctx, e.Name, uuid.New(), r, e); err != nil {
			t.Fatalf("upsert %v: %v", r, err)
		}
	}
}

func TestQueryStringFunctionsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := stringFuncEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		seedStringFuncs(t, ctx, db, ents)

		cases := []struct {
			name, src string
			params    map[string]any
			want      string
		}{
			{
				// Позиция считается с ЕДИНИЦЫ и в СИМВОЛАХ: побайтовый разрез
				// вернул бы половину буквы, а не «Лен».
				name: "ПОДСТРОКА",
				src:  `ВЫБРАТЬ ПОДСТРОКА(К.Наименование, 1, 3) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "Лен",
			},
			{
				name: "ПОДСТРОКА нулевая позиция",
				src:  `ВЫБРАТЬ ПОДСТРОКА(К.Наименование, 0, 2) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "Л",
			},
			{
				name: "ПОДСТРОКА отрицательная позиция",
				src:  `ВЫБРАТЬ ПОДСТРОКА(К.Наименование, -2, 2) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "",
			},
			{
				name: "ДЛИНАСТРОКИ",
				src:  `ВЫБРАТЬ ДЛИНАСТРОКИ(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "6",
			},
			{
				name: "STRINGLENGTH",
				src:  `ВЫБРАТЬ STRINGLENGTH(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "6",
			},
			{
				name: "ЛЕВ",
				src:  `ВЫБРАТЬ ЛЕВ(К.Наименование, 3) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "Лен",
			},
			{
				name: "LEFTSTR",
				src:  `ВЫБРАТЬ LEFTSTR(К.Наименование, 3) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "Лен",
			},
			{
				name: "ЛЕВ отрицательная длина",
				src:  `ВЫБРАТЬ ЛЕВ(К.Наименование, -1) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "Ленин",
			},
			{
				// Ровно то, ради чего ПРАВ и нужен: сравнение телефона по хвосту.
				name: "ПРАВ",
				src:  `ВЫБРАТЬ ПРАВ(К.Телефон, 5) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "22-33",
			},
			{
				name: "RIGHTSTR",
				src:  `ВЫБРАТЬ RIGHTSTR(К.Телефон, 5) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "22-33",
			},
			{
				name: "ПРАВ отрицательная длина",
				src:  `ВЫБРАТЬ ПРАВ(К.Наименование, -1) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "енина",
			},
			{
				// Встроенный UPPER в SQLite вернул бы «ленина» без изменений.
				name: "ВРЕГ",
				src:  `ВЫБРАТЬ ВРЕГ(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "ЛЕНИНА",
			},
			{
				name: "НРЕГ",
				src:  `ВЫБРАТЬ НРЕГ(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
				want: "ленина",
			},
			{
				name: "СОКРЛП",
				src:  `ВЫБРАТЬ СОКРЛП(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Телефон = "8 999 444-55-66"`,
				want: "Гагарина",
			},
			{
				name: "TRIMALL",
				src:  `ВЫБРАТЬ TRIMALL(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Телефон = "8 999 444-55-66"`,
				want: "Гагарина",
			},
			{
				name: "СОКРЛ",
				src:  `ВЫБРАТЬ СОКРЛ(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Телефон = "8 999 444-55-66"`,
				want: "Гагарина  ",
			},
			{
				name: "TRIMLEFT",
				src:  `ВЫБРАТЬ TRIMLEFT(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Телефон = "8 999 444-55-66"`,
				want: "Гагарина  ",
			},
			{
				name: "СОКРП",
				src:  `ВЫБРАТЬ СОКРП(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Телефон = "8 999 444-55-66"`,
				want: "  Гагарина",
			},
			{
				name: "TRIMRIGHT",
				src:  `ВЫБРАТЬ TRIMRIGHT(К.Наименование) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Телефон = "8 999 444-55-66"`,
				want: "  Гагарина",
			},
			{
				// ПОДОБНО — то, чего в языке не было вовсе: подстрока в ГДЕ.
				name:   "ПОДОБНО",
				src:    `ВЫБРАТЬ К.Наименование КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование ПОДОБНО &Шаблон`,
				params: map[string]any{"Шаблон": "%енин%"},
				want:   "Ленина",
			},
			{
				// Переносимый регистронезависимый поиск: НРЕГ с обеих сторон.
				// Сам ПОДОБНО регистр не приводит — на SQLite встроенный LIKE
				// складывает регистр только для латиницы, на PG не складывает
				// вовсе, и обещать одинаковое поведение без НРЕГ нельзя.
				name:   "ПОДОБНО+НРЕГ",
				src:    `ВЫБРАТЬ К.Наименование КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ НРЕГ(К.Наименование) ПОДОБНО НРЕГ(&Шаблон)`,
				params: map[string]any{"Шаблон": "%ЕНИН%"},
				want:   "Ленина",
			},
			{
				name:   "НЕ ПОДОБНО",
				src:    `ВЫБРАТЬ К.Телефон КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ НЕ К.Наименование ПОДОБНО &Шаблон И К.Телефон <> "служебный"`,
				params: map[string]any{"Шаблон": "%енин%"},
				want:   "8 999 444-55-66",
			},
			{
				// СПЕЦСИМВОЛ → ESCAPE: обратная косая черта делает процент
				// обычным символом. Без ESCAPE шаблон совпал бы и с "Скидка 100".
				name:   "СПЕЦСИМВОЛ",
				src:    `ВЫБРАТЬ К.Наименование КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование ПОДОБНО &Шаблон СПЕЦСИМВОЛ "\"`,
				params: map[string]any{"Шаблон": "%10\\%%"},
				want:   "Скидка 10%",
			},
		}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				res, err := query.Compile(c.src, query.CompileOpts{
					Entities: ents,
					Params:   c.params,
					Dialect:  db.Dialect(),
				})
				if err != nil {
					t.Fatalf("компиляция: %v", err)
				}
				rows, err := db.Query(ctx, res.SQL, res.Args...)
				if err != nil {
					t.Fatalf("исполнение: %v\nSQL: %s", err, res.SQL)
				}
				defer rows.Close()
				if !rows.Next() {
					t.Fatalf("пустая выдача\nSQL: %s", res.SQL)
				}
				// Скан в any: ДЛИНАСТРОКИ отдаёт целое, и жёсткий *string упал
				// бы на PostgreSQL, где тип колонки настоящий, а не
				// динамический, как в SQLite.
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatalf("скан: %v\nSQL: %s", err, res.SQL)
				}
				got := fmt.Sprintf("%v", raw)
				if b, ok := raw.([]byte); ok {
					got = string(b)
				}
				if got != c.want {
					t.Errorf("получено %q, ждали %q\nSQL: %s", got, c.want, res.SQL)
				}
				if rows.Next() {
					t.Errorf("ждали ровно одну строку\nSQL: %s", res.SQL)
				}
			})
		}
	})
}

func TestQuerySubstringRejectsNegativeLengthMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := stringFuncEntities()
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		seedStringFuncs(t, ctx, db, ents)

		res, err := query.Compile(
			`ВЫБРАТЬ ПОДСТРОКА(К.Наименование, 1, -1) КАК Р ИЗ Справочник.КлиентСтр КАК К ГДЕ К.Наименование = "Ленина"`,
			query.CompileOpts{Entities: ents, Dialect: db.Dialect()},
		)
		if err != nil {
			t.Fatalf("компиляция: %v", err)
		}
		rows, err := db.Query(ctx, res.SQL, res.Args...)
		if err == nil {
			rows.Close()
			t.Fatalf("отрицательная длина должна завершать запрос ошибкой; SQL: %s", res.SQL)
		}
	})
}
