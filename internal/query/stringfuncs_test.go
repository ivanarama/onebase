package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Трансляция строковых функций по диалектам. Исполнение проверяет матричный
// тест рядом; здесь фиксируется САМ ВЫБОР функции — он у диалектов разный там,
// где встроенная SQLite не умеет юникод (UPPER/LOWER) или её нет вовсе
// (LEFT/RIGHT). Компиляция для PostgreSQL проверяется и без живой PG.
func stringFuncCompileEntities() []*metadata.Entity {
	return []*metadata.Entity{{
		Name: "КлиентТр",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}}
}

func TestStringFuncsCompileByDialect(t *testing.T) {
	for _, c := range []struct {
		name    string
		dialect storage.Dialect
		src     string
		want    string
	}{
		// SQLite использует UDF для PostgreSQL-совместимых границ ПОДСТРОКА.
		{"подстрока-sqlite", storage.SQLiteDialect{}, `ВЫБРАТЬ ПОДСТРОКА(Наименование, 1, 3) ИЗ Справочник.КлиентТр`, "ob_substr("},
		{"подстрока-pg", storage.PgDialect{}, `ВЫБРАТЬ ПОДСТРОКА(Наименование, 1, 3) ИЗ Справочник.КлиентТр`, "substr("},
		{"длина-sqlite", storage.SQLiteDialect{}, `ВЫБРАТЬ ДЛИНАСТРОКИ(Наименование) ИЗ Справочник.КлиентТр`, "length("},
		{"длина-pg", storage.PgDialect{}, `ВЫБРАТЬ ДЛИНАСТРОКИ(Наименование) ИЗ Справочник.КлиентТр`, "length("},
		{"сокрлп-pg", storage.PgDialect{}, `ВЫБРАТЬ СОКРЛП(Наименование) ИЗ Справочник.КлиентТр`, "trim("},
		{"stringlength-pg", storage.PgDialect{}, `ВЫБРАТЬ STRINGLENGTH(Наименование) ИЗ Справочник.КлиентТр`, "length("},
		{"trimleft-pg", storage.PgDialect{}, `ВЫБРАТЬ TRIMLEFT(Наименование) ИЗ Справочник.КлиентТр`, "ltrim("},
		{"trimright-pg", storage.PgDialect{}, `ВЫБРАТЬ TRIMRIGHT(Наименование) ИЗ Справочник.КлиентТр`, "rtrim("},
		{"trimall-pg", storage.PgDialect{}, `ВЫБРАТЬ TRIMALL(Наименование) ИЗ Справочник.КлиентТр`, "trim("},

		// Юникод-регистр: в SQLite через свою функцию, в PG — нативно.
		{"врег-sqlite", storage.SQLiteDialect{}, `ВЫБРАТЬ ВРЕГ(Наименование) ИЗ Справочник.КлиентТр`, "ob_upper("},
		{"врег-pg", storage.PgDialect{}, `ВЫБРАТЬ ВРЕГ(Наименование) ИЗ Справочник.КлиентТр`, "upper("},
		{"нрег-sqlite", storage.SQLiteDialect{}, `ВЫБРАТЬ НРЕГ(Наименование) ИЗ Справочник.КлиентТр`, "ob_lower("},
		{"нрег-pg", storage.PgDialect{}, `ВЫБРАТЬ НРЕГ(Наименование) ИЗ Справочник.КлиентТр`, "lower("},

		// ЛЕВ/ПРАВ: в SQLite left()/right() нет вовсе.
		{"лев-sqlite", storage.SQLiteDialect{}, `ВЫБРАТЬ ЛЕВ(Наименование, 3) ИЗ Справочник.КлиентТр`, "ob_left("},
		{"лев-pg", storage.PgDialect{}, `ВЫБРАТЬ ЛЕВ(Наименование, 3) ИЗ Справочник.КлиентТр`, "left("},
		{"leftstr-pg", storage.PgDialect{}, `ВЫБРАТЬ LEFTSTR(Наименование, 3) ИЗ Справочник.КлиентТр`, "left("},
		{"прав-sqlite", storage.SQLiteDialect{}, `ВЫБРАТЬ ПРАВ(Наименование, 3) ИЗ Справочник.КлиентТр`, "ob_right("},
		{"прав-pg", storage.PgDialect{}, `ВЫБРАТЬ ПРАВ(Наименование, 3) ИЗ Справочник.КлиентТр`, "right("},
		{"rightstr-pg", storage.PgDialect{}, `ВЫБРАТЬ RIGHTSTR(Наименование, 3) ИЗ Справочник.КлиентТр`, "right("},

		// ПОДОБНО и СПЕЦСИМВОЛ — ключевые слова, а не функции.
		{"подобно-pg", storage.PgDialect{}, `ВЫБРАТЬ Наименование ИЗ Справочник.КлиентТр ГДЕ Наименование ПОДОБНО &П`, " LIKE "},
		{"спецсимвол-pg", storage.PgDialect{}, `ВЫБРАТЬ Наименование ИЗ Справочник.КлиентТр ГДЕ Наименование ПОДОБНО &П СПЕЦСИМВОЛ "\"`, " ESCAPE "},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := query.Compile(c.src, query.CompileOpts{
				Entities: stringFuncCompileEntities(),
				Params:   map[string]any{"П": "%x%"},
				Dialect:  c.dialect,
			})
			if err != nil {
				t.Fatalf("компиляция: %v", err)
			}
			// Сравнение без учёта регистра: ЛЕВ/ПРАВ выходят как LEFT(/RIGHT(
			// — их имена совпадают с ключевыми словами соединений, и общий
			// маппинг ключевых слов поднимает их в верхний регистр. Для
			// PostgreSQL это та же функция: имена функций там
			// регистронезависимы.
			if !strings.Contains(strings.ToLower(r.SQL), strings.ToLower(c.want)) {
				t.Errorf("ждали %q в SQL, получено:\n%s", c.want, r.SQL)
			}
		})
	}
}

// Имя функции не должно склеиваться с JOIN-ключевыми словами: ЛЕВОЕ/ПРАВОЕ
// СОЕДИНЕНИЕ — не вызовы ЛЕВ()/ПРАВ(), и подмена сломала бы соединения.
func TestStringFuncsDoNotBreakJoins(t *testing.T) {
	ents := []*metadata.Entity{
		{Name: "Заказ", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Клиент", Type: "reference:КлиентТр", RefEntity: "КлиентТр"},
		}},
		stringFuncCompileEntities()[0],
	}
	src := `ВЫБРАТЬ З.Клиент ИЗ Документ.Заказ КАК З
	        ЛЕВОЕ СОЕДИНЕНИЕ Справочник.КлиентТр КАК К ПО З.Клиент = К.Ссылка`
	for _, d := range []storage.Dialect{storage.SQLiteDialect{}, storage.PgDialect{}} {
		r, err := query.Compile(src, query.CompileOpts{Entities: ents, Dialect: d})
		if err != nil {
			t.Fatalf("%s: компиляция: %v", d.Name(), err)
		}
		if !strings.Contains(strings.ToUpper(r.SQL), "LEFT JOIN") {
			t.Errorf("%s: ждали LEFT JOIN, получено:\n%s", d.Name(), r.SQL)
		}
		if strings.Contains(r.SQL, "ob_left(") {
			t.Errorf("%s: ЛЕВОЕ СОЕДИНЕНИЕ подменено вызовом ЛЕВ():\n%s", d.Name(), r.SQL)
		}
	}
}
