package query_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Resolve parameter casts from the exact source, including JOIN, virtual and
// derived sources, nested SELECTs and independent UNION branches. Execute the
// compiled query so a wrong cast cannot hide behind a plausible SQL string.
func TestParameterFieldSourceMatrix(t *testing.T) {
	dbtest.ForEachDialectWithoutUUIDTextCast(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		text := &metadata.Entity{Name: "ТекстПараметра", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Внешний_id", Type: metadata.FieldTypeString},
			{Name: "Склад", Type: metadata.FieldTypeString},
		}}
		store := &metadata.Entity{Name: "СкладПараметра", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Регион", Type: metadata.FieldType("reference:СкладПараметра"), RefEntity: "СкладПараметра"}}}
		text.Fields = append(text.Fields, metadata.Field{Name: "Ответственный", Type: metadata.FieldType("reference:" + store.Name), RefEntity: store.Name})
		doc := &metadata.Entity{Name: "ДокПараметра", Kind: metadata.KindDocument}
		entities := []*metadata.Entity{text, store, doc}
		reg := &metadata.Register{Name: "ОстаткиПараметра", Dimensions: []metadata.Field{
			{Name: "Склад", Type: metadata.FieldType("reference:" + store.Name), RefEntity: store.Name},
			{Name: "Текст_id", Type: metadata.FieldTypeString},
		}, Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		id, docID := uuid.New(), uuid.New()
		for _, row := range []struct {
			e      *metadata.Entity
			id     uuid.UUID
			fields map[string]any
		}{
			{store, id, map[string]any{"Наименование": "склад"}},
			{text, uuid.New(), map[string]any{"Наименование": "пусто", "Внешний_id": "", "Склад": "", "Ответственный": id}},
			{text, uuid.New(), map[string]any{"Наименование": "uuid", "Внешний_id": id.String(), "Склад": id.String(), "Ответственный": id}},
			{doc, docID, map[string]any{}},
		} {
			if err := db.Upsert(ctx, row.e.Name, row.id, row.fields, row.e); err != nil {
				t.Fatal(err)
			}
		}
		period := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, doc.Name, docID, []map[string]any{{"ВидДвижения": "Приход", "Склад": id, "Текст_id": "", "Количество": float64(1)}}, reg, &period); err != nil {
			t.Fatal(err)
		}
		if db.Dialect().Name() == "postgres" {
			rows, err := db.QueryAll(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`)
			if err != nil {
				t.Fatal(err)
			}
			if rows[0]["rolsuper"] != false {
				t.Fatal("matrix requires an ordinary PostgreSQL role")
			}
			t.Logf("PostgreSQL role rolsuper=%v", rows[0]["rolsuper"])
			rows, err = db.QueryAll(ctx, `SELECT count(*) AS n FROM pg_cast WHERE castsource = 'uuid'::regtype AND casttarget = 'text'::regtype AND castcontext = 'i'`)
			if err != nil {
				t.Fatal(err)
			}
			if vtoLabel(rows[0]["n"]) != "0" {
				t.Fatal("matrix requires no global implicit uuid-to-text cast")
			}
		}
		vt := `РегистрНакопления.ОстаткиПараметра.Остатки() КАК О`
		joined := `Справочник.ТекстПараметра КАК Т ВНУТРЕННЕЕ СОЕДИНЕНИЕ ` + vt + ` ПО 1 = 1`
		for _, tc := range []struct {
			name, src string
			param     any
			want      []string
		}{
			{"text suffix", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + joined + ` ГДЕ Т.Внешний_id = &П`, "", []string{"пусто"}},
			{"text shares virtual reference name", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + joined + ` ГДЕ Т.Склад = &П`, "", []string{"пусто"}},
			{"joined text suffix", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + vt + ` ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ТекстПараметра КАК Т ПО 1 = 1 ГДЕ Т.Внешний_id = &П`, "", []string{"пусто"}},
			{"joined text shares virtual reference name", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + vt + ` ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ТекстПараметра КАК Т ПО 1 = 1 ГДЕ Т.Склад = &П`, "", []string{"пусто"}},
			{"virtual reference shares text name", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + joined + ` ГДЕ О.Склад = &П И Т.Наименование = "uuid"`, id.String(), []string{"uuid"}},
			{"virtual reference parameter first", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + joined + ` ГДЕ &П = О.Склад И Т.Наименование = "uuid"`, id.String(), []string{"uuid"}},
			{"virtual reference list", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + joined + ` ГДЕ О.Склад В (&П) И Т.Наименование = "uuid"`, []any{id.String()}, []string{"uuid"}},
			{"joined text UUID", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + vt + ` ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ТекстПараметра КАК Т ПО 1 = 1 ГДЕ Т.Склад = &П`, id.String(), []string{"uuid"}},
			{"joined text UUID parameter first", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + vt + ` ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ТекстПараметра КАК Т ПО 1 = 1 ГДЕ &П = Т.Склад`, id.String(), []string{"uuid"}},
			{"joined text UUID list", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + vt + ` ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ТекстПараметра КАК Т ПО 1 = 1 ГДЕ Т.Склад В (&П)`, []any{id.String()}, []string{"uuid"}},
			{"virtual reference empty", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ ` + joined + ` ГДЕ О.Склад = &П`, "", nil},
			{"entity reference empty", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ Справочник.ТекстПараметра КАК Т ГДЕ Т.Ссылка = &П`, "", nil},
			{"recorder reference empty", `ВЫБРАТЬ Р.Склад КАК Метка ИЗ РегистрНакопления.ОстаткиПараметра КАК Р ГДЕ Р.Регистратор = &П`, "", nil},
			{"navigation reference empty", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ Справочник.ТекстПараметра КАК Т ГДЕ Т.Ответственный.Регион = &П`, "", nil},
			{"short navigation reference empty", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ Справочник.ТекстПараметра КАК Т ГДЕ Ответственный.Регион = &П`, "", nil},
			{"register filter text suffix", `ВЫБРАТЬ О.Текст_id КАК Метка ИЗ РегистрНакопления.ОстаткиПараметра.Остатки(, Текст_id = &П) КАК О`, "", []string{""}},
			{"derived text empty", `ВЫБРАТЬ Д.Метка ИЗ (ВЫБРАТЬ Т.Наименование КАК Метка, Т.Внешний_id КАК Значение ИЗ Справочник.ТекстПараметра КАК Т) КАК Д ГДЕ Д.Значение = &П`, "", []string{"пусто"}},
			{"derived text UUID", `ВЫБРАТЬ Д.Метка ИЗ (ВЫБРАТЬ Т.Наименование КАК Метка, Т.Склад КАК Значение ИЗ Справочник.ТекстПараметра КАК Т) КАК Д ГДЕ Д.Значение = &П`, id.String(), []string{"uuid"}},
			{"nested qualifier shadows outer text", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ Справочник.ТекстПараметра КАК Т ГДЕ Т.Наименование = "uuid" И EXISTS (ВЫБРАТЬ Т.Склад ИЗ ` + strings.ReplaceAll(vt, "КАК О", "КАК Т") + ` ГДЕ Т.Склад = &П)`, id.String(), []string{"uuid"}},
			{"union separate types", `ВЫБРАТЬ Т.Наименование КАК Метка ИЗ Справочник.ТекстПараметра КАК Т ГДЕ Склад = &П ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ "остаток" КАК Метка ИЗ ` + vt + ` ГДЕ Склад = &П`, id.String(), []string{"uuid", "остаток"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				res, err := query.Compile(tc.src, query.CompileOpts{Entities: entities, Registers: []*metadata.Register{reg}, Params: map[string]any{"П": tc.param}, Dialect: db.Dialect()})
				if err != nil {
					t.Fatal(err)
				}
				rows, err := db.QueryAll(ctx, res.SQL, res.Args...)
				if err != nil {
					t.Fatalf("%v\nSQL: %s", err, res.SQL)
				}
				query.NormalizeColumns(&res, rows)
				got := []string{}
				for _, r := range rows {
					got = append(got, vtoLabel(r["метка"]))
				}

				if len(got) != len(tc.want) || strings.Join(got, "|") != strings.Join(tc.want, "|") {
					t.Fatalf("got %v, want %v\nSQL: %s", got, tc.want, res.SQL)
				}
			})
		}
	})
}
