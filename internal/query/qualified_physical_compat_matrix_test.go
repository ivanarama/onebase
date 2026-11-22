package query_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestQualifiedPhysicalReferenceToScalarMatrix(t *testing.T) {
	for _, typ := range []metadata.FieldType{metadata.FieldTypeBool, metadata.FieldTypeString, metadata.FieldTypeNumber} {
		t.Run(string(typ), func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ref := func(name, entity string) metadata.Field {
					return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
				}
				target := &metadata.Entity{Name: "ScalarTarget211", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
				joined := &metadata.Entity{Name: "ScalarJoined211", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Collision", Type: typ}}}
				main := &metadata.Entity{Name: "ScalarMain211", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}, ref("Link", joined.Name), ref("Collision", target.Name)}}
				entities := []*metadata.Entity{target, joined, main}
				ctx := context.Background()
				if err := db.Migrate(ctx, entities); err != nil {
					t.Fatal(err)
				}
				first, second := any(true), any(false)
				column := "флаг"
				if typ == metadata.FieldTypeString {
					first, second, column = "first value", "second value", "значение"
				}
				if typ == metadata.FieldTypeNumber {
					first, second, column = 17, 2, "значение"
				}
				targetID, firstID, secondID := uuid.New(), uuid.New(), uuid.New()
				for _, row := range []struct {
					entity *metadata.Entity
					id     uuid.UUID
					fields map[string]any
				}{
					{target, targetID, map[string]any{"Наименование": "Unrelated reference"}},
					{joined, firstID, map[string]any{"Наименование": "first", "Collision": first}},
					{joined, secondID, map[string]any{"Наименование": "second", "Collision": second}},
					{main, uuid.New(), map[string]any{"Номер": "first", "Link": firstID, "Collision": targetID}},
					{main, uuid.New(), map[string]any{"Номер": "second", "Link": secondID, "Collision": targetID}},
					{main, uuid.New(), map[string]any{"Номер": "missing", "Link": nil, "Collision": targetID}},
				} {
					if err := db.Upsert(ctx, row.entity.Name, row.id, row.fields, row.entity); err != nil {
						t.Fatal(err)
					}
				}
				opts := query.CompileOpts{Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Expected": first}}
				const join = ` ИЗ Документ.ScalarMain211 КАК М ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ScalarJoined211 КАК П ПО П.Ссылка = М.Link`
				projection := `ВЫБРАТЬ М.Номер КАК Номер, П.Collision КАК ` + column
				columns := []string{"номер", column}
				result := assertQualifiedRows(t, db, opts, projection+join, columns, []string{"first|" + fmt.Sprint(first), "second|" + fmt.Sprint(second), "missing|<NULL>"})
				if slices.Contains(result.BoolColumns, column) != (typ == metadata.FieldTypeBool) {
					t.Fatalf("source-owned boolean descriptor: %v for %s", result.BoolColumns, typ)
				}
				if _, wrongReference := result.RefColumns[column]; wrongReference {
					t.Fatal("joined scalar inherited the main source reference descriptor")
				}
				assertQualifiedRows(t, db, opts, projection+join+` ГДЕ П.Collision = &Expected`, columns, []string{"first|" + fmt.Sprint(first)})
				assertQualifiedRows(t, db, opts, projection+join+` И П.Collision = &Expected`, columns, []string{"first|" + fmt.Sprint(first), "second|<NULL>", "missing|<NULL>"})
				if typ == metadata.FieldTypeNumber {
					assertQualifiedRows(t, db, opts, projection+join+` ГДЕ П.Collision > 10`, columns, []string{"first|17"})
				}
			})
		})
	}
}

func TestQualifiedPhysicalSystemColumnsShadowingMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, field, source string
		kind                metadata.Kind
	}{
		{"catalog_deletion_mark", "ПометкаУдаления", "Справочник", metadata.KindCatalog},
		{"document_posted", "Проведен", "Документ", metadata.KindDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				outer := &metadata.Entity{Name: "SystemOuter211", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}, {Name: tc.field, Type: metadata.FieldTypeBool}}}
				inner := &metadata.Entity{Name: "SystemInner211", Kind: tc.kind, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
				entities := []*metadata.Entity{outer, inner}
				if err := db.Migrate(ctx, entities); err != nil {
					t.Fatal(err)
				}
				if err := db.Upsert(ctx, outer.Name, uuid.New(), map[string]any{"Номер": "outer", tc.field: true}, outer); err != nil {
					t.Fatal(err)
				}
				if err := db.Upsert(ctx, inner.Name, uuid.New(), map[string]any{"Наименование": "inner"}, inner); err != nil {
					t.Fatal(err)
				}
				opts := query.CompileOpts{Entities: entities, Dialect: db.Dialect()}
				text := `ВЫБРАТЬ М.Номер КАК Номер ИЗ Документ.SystemOuter211 КАК М ГДЕ ЛОЖЬ В (ВЫБРАТЬ М.` + tc.field + ` ИЗ ` + tc.source + `.SystemInner211 КАК М)`
				assertQualifiedRows(t, db, opts, text, []string{"номер"}, []string{"outer"})
				text = `ВЫБРАТЬ М.Номер КАК Номер ИЗ Документ.SystemOuter211 КАК М ГДЕ ИСТИНА В (ВЫБРАТЬ М.` + tc.field + ` ИЗ ` + tc.source + `.SystemInner211 КАК М)`
				assertQualifiedRows(t, db, opts, text, []string{"номер"}, []string{})
			})
		})
	}
}

func TestQualifiedPhysicalOppositeTypesMatrix(t *testing.T) {
	for _, typ := range []metadata.FieldType{metadata.FieldTypeBool, metadata.FieldTypeString, metadata.FieldTypeNumber} {
		t.Run(string(typ), func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				target := &metadata.Entity{Name: "Target211", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
				ref := func(name, entity string) metadata.Field {
					return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
				}
				joined := &metadata.Entity{Name: "Joined211", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, ref("Collision", target.Name)}}
				main := &metadata.Entity{Name: "Main211", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}, ref("Link", joined.Name), {Name: "Collision", Type: typ}}}
				entities := []*metadata.Entity{target, joined, main}
				ctx := context.Background()
				if err := db.Migrate(ctx, entities); err != nil {
					t.Fatal(err)
				}
				targetID, joinedID := uuid.New(), uuid.New()
				value := any(true)
				if typ == metadata.FieldTypeString {
					value = "different scalar"
				}
				if typ == metadata.FieldTypeNumber {
					value = 17
				}
				for _, row := range []struct {
					entity *metadata.Entity
					id     uuid.UUID
					fields map[string]any
				}{
					{target, targetID, map[string]any{"Наименование": "Target"}},
					{joined, joinedID, map[string]any{"Наименование": "Joined", "Collision": targetID}},
					{main, uuid.New(), map[string]any{"Номер": "linked", "Link": joinedID, "Collision": value}},
					{main, uuid.New(), map[string]any{"Номер": "missing", "Link": nil, "Collision": value}},
				} {
					if err := db.Upsert(ctx, row.entity.Name, row.id, row.fields, row.entity); err != nil {
						t.Fatal(err)
					}
				}
				opts := query.CompileOpts{Entities: entities, Dialect: db.Dialect(), Params: map[string]any{"Target": targetID.String()}}
				const join = ` ИЗ Документ.Main211 КАК М ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Joined211 КАК П ПО П.Ссылка = М.Link`
				// Preserve the existing qualified bare-reference projection contract.
				assertQualifiedRows(t, db, opts, `ВЫБРАТЬ М.Номер КАК Номер, П.Collision КАК Значение`+join, []string{"номер", "значение"}, []string{"linked|" + targetID.String(), "missing|<NULL>"})
				assertQualifiedRows(t, db, opts, `ВЫБРАТЬ М.Номер КАК Номер, П.Collision.Ссылка КАК Ид`+join, []string{"номер", "ид"}, []string{"linked|" + targetID.String(), "missing|<NULL>"})
				assertQualifiedRows(t, db, opts, `ВЫБРАТЬ М.Номер КАК Номер`+join+` ГДЕ П.Collision = &Target`, []string{"номер"}, []string{"linked"})
				assertQualifiedRows(t, db, opts, `ВЫБРАТЬ М.Номер КАК Номер, П.Collision.Наименование КАК Имя`+join, []string{"номер", "имя"}, []string{"linked|Target", "missing|<NULL>"})
			})
		})
	}
}

func TestQualifiedPhysicalDerivedVirtualMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "DerivedVirtual211", Kind: metadata.KindDocument, Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString}, {Name: "КоличествоОстаток", Type: metadata.FieldTypeNumber},
		}}
		registers := matrixRegisters()
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, registers); err != nil {
			t.Fatal(err)
		}
		seedMatrixRegister(t, ctx, db, registers[0])
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Номер": "yes", "КоличествоОстаток": 8}, entity); err != nil {
			t.Fatal(err)
		}
		opts := query.CompileOpts{Entities: []*metadata.Entity{entity}, Registers: registers, Dialect: db.Dialect()}
		for _, tc := range []struct{ name, projection, alias string }{
			{"qualified_star", "ВТ.*", " КАК ВТ"},
			{"bare_star", "*", " КАК ВТ"},
			{"unaliased_star", "*", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				text := `ВЫБРАТЬ Д.Номер КАК Номер ИЗ Документ.DerivedVirtual211 КАК Д
					ГДЕ Д.КоличествоОстаток В (ВЫБРАТЬ Д.КоличествоОстаток
					ИЗ (ВЫБРАТЬ ` + tc.projection + ` ИЗ РегистрНакопления.ОстаткиМатрица.Остатки()` + tc.alias + `) КАК Д)`
				assertQualifiedRows(t, db, opts, text, []string{"номер"}, []string{"yes"})
			})
		}
	})
}
