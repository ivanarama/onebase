package query_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func boolLiteralEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "ПрофилиИзвлечения",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
		},
	}
}

// issue #704: Истина/Ложь в тексте запроса — литералы, а не имена колонок.
// Раньше они доезжали до SQL как идентификаторы («... = истина»), и запрос падал
// «no such column». Литерал диалектозависим: PostgreSQL хранит булево как
// BOOLEAN, SQLite — как INTEGER 0/1 (см. Dialect.TypeBool).
func TestBoolLiteral_SQLiteVsPostgres(t *testing.T) {
	ent := boolLiteralEntity()
	src := `ВЫБРАТЬ Наименование ИЗ Справочник.ПрофилиИзвлечения ГДЕ Активен = Истина ИЛИ Активен = Ложь`

	cases := []struct {
		name    string
		dialect storage.Dialect
		want    string
	}{
		{"sqlite", storage.SQLiteDialect{}, "WHERE активен = 1 OR активен = 0"},
		{"postgres", storage.PgDialect{}, "WHERE активен = TRUE OR активен = FALSE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := query.Compile(src, query.CompileOpts{
				Entities: []*metadata.Entity{ent},
				Dialect:  c.dialect,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.SQL, c.want) {
				t.Errorf("ожидалось %q в SQL:\n%s", c.want, r.SQL)
			}
			if strings.Contains(strings.ToLower(r.SQL), "истина") ||
				strings.Contains(strings.ToLower(r.SQL), "ложь") {
				t.Errorf("булев литерал уехал в SQL именем колонки:\n%s", r.SQL)
			}
		})
	}
}

// Слово остаётся именем там, где оно синтаксически имя: после точки (поле
// «Истина» у источника) и в позиции алиаса «КАК Истина». Иначе литерал сломал
// бы существующие конфигурации с таким полем.
func TestBoolLiteral_NotSubstitutedForNames(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Настройка",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Истина", Type: metadata.FieldTypeString},
		},
	}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "после точки и в алиасе",
			src:  `ВЫБРАТЬ Н.Истина КАК Истина ИЗ Справочник.Настройка КАК Н`,
			want: "н.истина AS истина",
		},
		{
			// Поле источника важнее литерала: иначе конфигурация с таким полем
			// молча читала бы константу вместо колонки.
			name: "без префикса — поле источника",
			src:  `ВЫБРАТЬ Истина ИЗ Справочник.Настройка`,
			want: "истина",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := query.Compile(c.src, query.CompileOpts{
				Entities: []*metadata.Entity{ent},
				Dialect:  storage.SQLiteDialect{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.SQL, c.want) {
				t.Errorf("имя поля заменено литералом, ожидалось %q:\n%s", c.want, r.SQL)
			}
		})
	}
}
