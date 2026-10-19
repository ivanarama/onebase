package query_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Public compilation and execution prove both the intentional WHERE expansion
// and unchanged JOIN NULL semantics. Identical field names have opposite types.
func TestColumnSourcesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		a := &metadata.Entity{Name: "ИсточникТекст1183", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Метка", Type: metadata.FieldTypeString},
			{Name: "Ключ", Type: metadata.FieldTypeString},
			{Name: "Значение", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeNumber},
			{Name: "Связь", Type: metadata.FieldTypeString},
		}}
		b := &metadata.Entity{Name: "ИсточникЧисло1183", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Ключ", Type: metadata.FieldTypeString},
			{Name: "Значение", Type: metadata.FieldTypeNumber},
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Связь", Type: metadata.FieldTypeString},
		}}
		ents := []*metadata.Entity{a, b}
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct {
			label string
			num   any
			text  any
		}{
			{"А2", 2, nil}, {"А9", 9, ""}, {"А100", 100, "Y"},
			{"АН", nil, nil}, {"АЕ", nil, ""}, {"АХ", nil, "X"}, {"АУ", nil, "Y"},
		} {
			var value any
			if row.num != nil {
				value = fmt.Sprint(row.num)
			}
			if err := db.Upsert(ctx, a.Name, uuid.New(), map[string]any{
				"Метка": row.label, "Ключ": row.label, "Значение": value,
			}, a); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, b.Name, uuid.New(), map[string]any{
				"Ключ": row.label, "Значение": row.num, "Текст": row.text,
			}, b); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Upsert(ctx, a.Name, uuid.New(), map[string]any{"Метка": "АЗ", "Ключ": "нет"}, a); err != nil {
			t.Fatal(err)
		}
		join := ` ИЗ Документ.ИсточникТекст1183 КАК Т
			ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ИсточникЧисло1183 КАК К ПО Т.Ключ = К.Ключ `
		allExceptX := []string{"А100", "А2", "А9", "АЕ", "АЗ", "АН", "АУ"}
		empty := []string{"А2", "А9", "АЕ", "АЗ", "АН"}
		projection, err := query.Compile(`ВЫБРАТЬ К.Текст`+join+`ГДЕ Т.Метка = "АЗ"`, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
		if err != nil {
			t.Fatal(err)
		}
		rows, cols, err := db.RunQuery(ctx, projection.SQL, projection.Args)
		if err != nil || len(rows) != 1 || rows[0][cols[0]] != nil {
			t.Fatalf("projection must retain NULL: rows=%v, err=%v; SQL=%s", rows, err, projection.SQL)
		}
		for _, tc := range []struct {
			name, text string
			want       []string
		}{
			{"joined number compare", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ К.Значение < 10 УПОРЯДОЧИТЬ ПО Т.Метка`, []string{"А2", "А9"}},
			{"joined number order", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ К.Значение > 0 УПОРЯДОЧИТЬ ПО К.Значение`, []string{"А2", "А9", "А100"}},
			{"main string keeps lexical order", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ Т.Значение <> "" УПОРЯДОЧИТЬ ПО Т.Значение`, []string{"А100", "А2", "А9"}},
			{"joined text inequality", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ К.Текст <> "X" УПОРЯДОЧИТЬ ПО Т.Метка`, allExceptX},
			{"joined text empty", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ К.Текст = "" УПОРЯДОЧИТЬ ПО Т.Метка`, empty},
			{"reversed literal", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ "" = К.Текст УПОРЯДОЧИТЬ ПО Т.Метка`, empty},
			{"parameter", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ К.Текст = &Пусто УПОРЯДОЧИТЬ ПО Т.Метка`, empty},
			{"negation", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ НЕ (К.Текст = "X") УПОРЯДОЧИТЬ ПО Т.Метка`, allExceptX},
			{"boolean group", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ (К.Текст = "" ИЛИ К.Текст = "Y") УПОРЯДОЧИТЬ ПО Т.Метка`, allExceptX},
			{"nested alias reused", `ВЫБРАТЬ Т.Метка ИЗ Документ.ИсточникТекст1183 КАК Т
				ГДЕ Т.Ключ В (ВЫБРАТЬ Т.Ключ ИЗ Справочник.ИсточникЧисло1183 КАК Т ГДЕ Т.Значение < 10)
				УПОРЯДОЧИТЬ ПО Т.Метка`, []string{"А2", "А9"}},
			{"correlated qualifier", `ВЫБРАТЬ Т.Метка` + join + `ГДЕ Т.Ключ В
				(ВЫБРАТЬ Внутр.Ключ ИЗ Документ.ИсточникТекст1183 КАК Внутр ГДЕ Внутр.Ключ = Т.Ключ И К.Значение < 10)
				УПОРЯДОЧИТЬ ПО Т.Метка`, []string{"А2", "А9"}},
			{"projection alias cannot hide qualified number", `ВЫБРАТЬ Т.Метка КАК Значение` + join +
				`ГДЕ К.Значение < 10 УПОРЯДОЧИТЬ ПО К.Значение`, []string{"А2", "А9"}},
			{"union scopes", `ВЫБРАТЬ Т.Метка ИЗ Документ.ИсточникТекст1183 КАК Т ГДЕ Т.Значение = "100"
				ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Т.Ключ КАК Метка ИЗ Справочник.ИсточникЧисло1183 КАК Т ГДЕ Т.Значение < 10
				УПОРЯДОЧИТЬ ПО Метка`, []string{"А100", "А2", "А9"}},
			{"ON does not match nulls", `ВЫБРАТЬ Т.Метка ИЗ Документ.ИсточникТекст1183 КАК Т
				ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.ИсточникЧисло1183 КАК К ПО Т.Связь = К.Связь`, nil},
			{"comma join does not match nulls", `ВЫБРАТЬ Т.Метка ИЗ Документ.ИсточникТекст1183 КАК Т,
				Справочник.ИсточникЧисло1183 КАК К ГДЕ Т.Связь = К.Связь`, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r, err := query.Compile(tc.text, query.CompileOpts{Entities: ents, Dialect: db.Dialect(), Params: map[string]any{"Пусто": ""}})
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(r.SQL, "COALESCE(к.значение") {
					t.Fatalf("numeric column borrowed text type: %s", r.SQL)
				}
				rows, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
				if err != nil {
					t.Fatalf("%s: %v", r.SQL, err)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row[cols[0]]))
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %v, want %v; SQL: %s", got, tc.want, r.SQL)
				}
			})
		}
	})
}

func TestColumnSourcesVirtualMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		a := &metadata.Entity{Name: "Ведущий1183", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Ключ", Type: metadata.FieldTypeString}, {Name: "Значение", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeNumber},
		}}
		ir := &metadata.InfoRegister{Name: "Сведения1183", Dimensions: []metadata.Field{
			{Name: "Ключ", Type: metadata.FieldTypeString}, {Name: "Значение", Type: metadata.FieldTypeNumber},
		}, Resources: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}}
		reg := &metadata.Register{Name: "Накопление1183", Dimensions: ir.Dimensions,
			Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		if err := db.Migrate(ctx, []*metadata.Entity{a}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{2, 9, 100} {
			key := fmt.Sprint(n)
			if err := db.Upsert(ctx, a.Name, uuid.New(), map[string]any{"Ключ": key}, a); err != nil {
				t.Fatal(err)
			}
			if err := db.InfoRegSet(ctx, ir, map[string]any{"Ключ": key, "Значение": n}, map[string]any{"Текст": ""}, nil); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteMovements(ctx, reg.Name, a.Name, uuid.New(), []map[string]any{{
				"ВидДвижения": "Приход", "Ключ": key, "Значение": n, "Количество": 1,
			}}, reg, nil); err != nil {
				t.Fatal(err)
			}
		}
		for _, source := range []string{
			"РегистрСведений.Сведения1183", "РегистрСведений.Сведения1183.СрезПоследних()",
			"РегистрНакопления.Накопление1183.Остатки()", "РегистрНакопления.Накопление1183.Обороты()",
		} {
			t.Run(source, func(t *testing.T) {
				filter := `Р.Значение < 10`
				if strings.HasPrefix(source, "РегистрСведений") {
					filter += ` И Р.Текст = ""`
				}
				r, err := query.Compile(`ВЫБРАТЬ Т.Ключ ИЗ Документ.Ведущий1183 КАК Т
					ЛЕВОЕ СОЕДИНЕНИЕ `+source+` КАК Р ПО Т.Ключ = Р.Ключ
					ГДЕ `+filter+` УПОРЯДОЧИТЬ ПО Р.Значение`, query.CompileOpts{
					Entities: []*metadata.Entity{a}, InfoRegs: []*metadata.InfoRegister{ir},
					Registers: []*metadata.Register{reg}, Dialect: db.Dialect(),
				})
				if err != nil {
					t.Fatal(err)
				}
				rows, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
				if err != nil {
					t.Fatalf("%s: %v", r.SQL, err)
				}
				var got []string
				for _, row := range rows {
					got = append(got, fmt.Sprint(row[cols[0]]))
				}
				if !reflect.DeepEqual(got, []string{"2", "9"}) {
					t.Fatalf("got %v; SQL: %s", got, r.SQL)
				}
			})
		}
	})
}

func TestColumnSourcesUnknownAndAmbiguous(t *testing.T) {
	a := &metadata.Entity{Name: "Числа1183", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Значение", Type: metadata.FieldTypeNumber}}}
	for _, source := range []string{
		`Справочник.Числа1183 КАК Б`, // same type still has two distinct owners
		`Справочник.Неизвестный1183 КАК Б`,
		`(ВЫБРАТЬ "строка" КАК Значение ИЗ Справочник.Числа1183) КАК Б`,
	} {
		for _, field := range []string{"Значение", "Б.Значение"} {
			if field == "Б.Значение" && strings.HasPrefix(source, "Справочник.Числа1183") {
				continue
			}
			r, err := query.Compile(`ВЫБРАТЬ А.Значение ИЗ Справочник.Числа1183 КАК А
				ЛЕВОЕ СОЕДИНЕНИЕ `+source+` ПО ИСТИНА ГДЕ `+field+` < 10`, query.CompileOpts{Entities: []*metadata.Entity{a}, Dialect: storage.SQLiteDialect{}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(r.SQL, "CAST(") {
				t.Fatalf("unknown/ambiguous column borrowed number type: %s", r.SQL)
			}
		}
	}
}
