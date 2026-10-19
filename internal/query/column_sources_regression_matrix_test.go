package query_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// A source qualifier can share a column's spelling without becoming a column.
// Exercise both scalar wrapping and the qualification enabled by an auto-JOIN.
func TestColumnSourcesQualifierMatchesFieldMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		client := &metadata.Entity{Name: "КлиентыАлиаса1183", Kind: metadata.KindCatalog}
		order := &metadata.Entity{Name: "ЗаказыАлиаса1183", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString},
				{Name: "Метка", Type: metadata.FieldTypeString},
				{Name: "Клиент", Type: metadata.FieldType("reference:" + client.Name), RefEntity: client.Name}}}
		price := &metadata.Entity{Name: "Цена", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString},
				{Name: "Цена", Type: metadata.FieldTypeNumber},
				{Name: "Категория", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{order, price, client}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		clientID := uuid.New()
		if err := db.Upsert(ctx, client.Name, clientID, map[string]any{"Наименование": "Клиент"}, client); err != nil {
			t.Fatal(err)
		}
		for _, num := range []int{2, 9, 100} {
			label := fmt.Sprint(num)
			if err := db.Upsert(ctx, order.Name, uuid.New(), map[string]any{
				"Ключ": label, "Метка": label, "Клиент": clientID.String(),
			}, order); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, price.Name, uuid.New(), map[string]any{"Ключ": label, "Цена": num}, price); err != nil {
				t.Fatal(err)
			}
		}
		for _, autoJoin := range []bool{false, true} {
			for _, tc := range []struct {
				name, alias, declaration, clause string
				want                             []string
			}{
				{"number filter", "Цена", " КАК Цена", `ГДЕ Цена.Цена < 3 УПОРЯДОЧИТЬ ПО Цена.Цена`, []string{"2"}},
				{"number order", "Цена", " КАК Цена", `ГДЕ Цена.Цена > 0 УПОРЯДОЧИТЬ ПО Цена.Цена`, []string{"2", "9", "100"}},
				{"source name", "Цена", "", `ГДЕ Цена.Цена < 3 УПОРЯДОЧИТЬ ПО Цена.Цена`, []string{"2"}},
				{"text filter", "Категория", " КАК Категория", `ГДЕ Категория.Категория = "" УПОРЯДОЧИТЬ ПО Категория.Цена`, []string{"2", "9", "100"}},
			} {
				t.Run(fmt.Sprintf("%s/autoJoin=%t", tc.name, autoJoin), func(t *testing.T) {
					projection := "А.Метка"
					if autoJoin {
						projection += ", А.Клиент"
					}
					text := `ВЫБРАТЬ ` + projection + ` ИЗ Документ.ЗаказыАлиаса1183 КАК А
						ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.Цена` + tc.declaration +
						` ПО А.Ключ = ` + tc.alias + `.Ключ ` + tc.clause
					r, err := query.Compile(text, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
					if err != nil {
						t.Fatal(err)
					}
					rows, cols, err := db.RunQuery(ctx, r.SQL, r.Args)
					if err != nil {
						t.Fatalf("%v; SQL=%s", err, r.SQL)
					}
					var got []string
					for _, row := range rows {
						got = append(got, fmt.Sprint(row[cols[0]]))
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("got %v, want %v; SQL=%s", got, tc.want, r.SQL)
					}
				})
			}
		}
	})
}

func TestColumnSourcesAccumulationVirtualAttributesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{
			Name:       "АтрибутыОборотов1183",
			Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
			Attributes: []metadata.Field{{Name: "Категория", Type: metadata.FieldTypeString}},
		}
		regs := []*metadata.Register{reg}
		if err := db.MigrateRegisters(ctx, regs); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteMovements(ctx, reg.Name, "Документ1183", uuid.New(), []map[string]any{{
			"Ключ": "NULL-attr", "Количество": 1, "ВидДвижения": "Приход",
		}}, reg, nil); err != nil {
			t.Fatal(err)
		}
		for _, vt := range []string{"Остатки", "Обороты", "ОстаткиИОбороты"} {
			for _, field := range []string{"Категория", "Р.Категория"} {
				for _, predicate := range []string{` = ""`, ` <> "X"`} {
					t.Run(vt+"/"+field+predicate, func(t *testing.T) {
						text := `ВЫБРАТЬ Р.Ключ ИЗ РегистрНакопления.` + reg.Name + `.` + vt + `() КАК Р ГДЕ ` + field + predicate
						r, err := query.Compile(text, query.CompileOpts{Registers: regs, Dialect: db.Dialect()})
						if err != nil {
							t.Fatal(err)
						}
						rows, _, err := db.RunQuery(ctx, r.SQL, r.Args)
						if err != nil {
							t.Fatalf("%v; SQL=%s", err, r.SQL)
						}
						if len(rows) != 1 {
							t.Fatalf("NULL attribute lost: rows=%v; SQL=%s", rows, r.SQL)
						}
					})
				}
			}
		}
	})
}

// Comparing two main-source text columns keeps the existing empty-value rule.
func TestColumnSourcesMainColumnComparisonMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "СравнениеТекста1183", Kind: metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString},
				{Name: "ПолеА", Type: metadata.FieldTypeString}, {Name: "ПолеБ", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Метка": "NULL-vs-empty", "ПолеБ": ""}, ent); err != nil {
			t.Fatal(err)
		}
		raw, _, err := db.RunQuery(ctx, `SELECT метка FROM сравнениетекста1183 WHERE полеа = полеб`, nil)
		if err != nil {
			t.Fatal(err)
		}
		r, err := query.Compile(`ВЫБРАТЬ Т.Метка ИЗ Документ.СравнениеТекста1183 КАК Т ГДЕ Т.ПолеА = Т.ПолеБ`,
			query.CompileOpts{Entities: []*metadata.Entity{ent}, Dialect: db.Dialect()})
		if err != nil {
			t.Fatal(err)
		}
		compiled, _, err := db.RunQuery(ctx, r.SQL, r.Args)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("raw SQL rows=%d; compiled rows=%d; SQL=%s", len(raw), len(compiled), r.SQL)
		if len(raw) != 0 || len(compiled) != 1 {
			t.Fatalf("main-source comparison changed: raw=%v compiled=%v", raw, compiled)
		}
	})
}

// IN makes this WHERE fall outside the simple joined-text expansion.
// The main VT must keep its own rule regardless of AS and the joined source.
func TestColumnSourcesMainVirtualWithJoinMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{
			Name:       "ВедущийРегистр1183",
			Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
			Attributes: []metadata.Field{{Name: "Категория", Type: metadata.FieldTypeString}},
		}
		ent := &metadata.Entity{Name: "КлиентРегистра1183", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "ИмяКлюч", Type: metadata.FieldTypeString}}}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"ИмяКлюч": "NULL-attr"}, ent); err != nil {
			t.Fatal(err)
		}
		if err := db.WriteMovements(ctx, reg.Name, "Документ1183", uuid.New(), []map[string]any{{
			"Ключ": "NULL-attr", "Количество": 1, "ВидДвижения": "Приход",
		}}, reg, nil); err != nil {
			t.Fatal(err)
		}
		for _, vt := range []string{"Остатки", "Обороты", "ОстаткиИОбороты"} {
			for _, alias := range []string{"", " КАК Р"} {
				t.Run(vt+alias, func(t *testing.T) {
					field := "Категория"
					if alias != "" {
						field = "Р.Категория"
					}
					text := `ВЫБРАТЬ Ключ ИЗ РегистрНакопления.` + reg.Name + `.` + vt + `() ` + alias + `
						ЛЕВОЕ СОЕДИНЕНИЕ Справочник.КлиентРегистра1183 КАК Д ПО Ключ = Д.ИмяКлюч
						ГДЕ ` + field + ` <> "X" И Д.ИмяКлюч В ("NULL-attr")`
					r, err := query.Compile(text, query.CompileOpts{Entities: []*metadata.Entity{ent},
						Registers: []*metadata.Register{reg}, Dialect: db.Dialect()})
					if err != nil {
						t.Fatal(err)
					}
					rows, _, err := db.RunQuery(ctx, r.SQL, r.Args)
					if err != nil {
						t.Fatalf("%v; SQL=%s", err, r.SQL)
					}
					if len(rows) != 1 {
						t.Fatalf("main VT NULL attribute lost: rows=%v; SQL=%s", rows, r.SQL)
					}
				})
			}
		}
	})
}
