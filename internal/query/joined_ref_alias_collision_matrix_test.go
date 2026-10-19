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

// Средний сегмент П.ГлавныйУзел.Учётка — реквизит П, даже когда
// ГлавныйУзел одновременно служит алиасом другого явного JOIN (#1459).
func TestJoinedRefAliasCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ents := joinedRefEntities()
		ents[0].Fields = append(ents[0].Fields, metadata.Field{Name: "Учётка", Type: "reference:Учётки", RefEntity: "Учётки"})
		ents = append(ents,
			&metadata.Entity{Name: "Учётки", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}},
			&metadata.Entity{Name: "Чужие", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Код", Type: metadata.FieldTypeString},
				{Name: "Учётка", Type: metadata.FieldTypeString},
				{Name: "ДругойУзел", Type: "reference:Базы", RefEntity: "Базы"},
			}},
			&metadata.Entity{Name: "ДругиеПланы", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Код", Type: metadata.FieldTypeString},
				{Name: "ГлавныйУзел", Type: "reference:Чужие", RefEntity: "Чужие"},
			}},
		)
		if err := db.Migrate(ctx, ents); err != nil {
			t.Fatal(err)
		}
		account, base, foreign := uuid.New(), uuid.New(), uuid.New()
		for _, v := range []struct {
			ent    *metadata.Entity
			id     uuid.UUID
			fields map[string]any
		}{
			{ents[2], account, map[string]any{"Наименование": "Учётка"}},
			{ents[0], base, map[string]any{"Код": "GL", "Наименование": "Главная", "Учётка": account}},
			{ents[1], uuid.New(), map[string]any{"Код": "GL", "Наименование": "План", "ГлавныйУзел": base}},
			{ents[3], foreign, map[string]any{"Код": "GL", "Учётка": "чужая строка", "ДругойУзел": base}},
			{ents[4], uuid.New(), map[string]any{"Код": "GL", "ГлавныйУзел": foreign}},
		} {
			if err := db.Upsert(ctx, v.ent.Name, v.id, v.fields, v.ent); err != nil {
				t.Fatal(err)
			}
		}
		// Переименование только второго алиаса не должно менять ни значения,
		// ни ссылочные типы результата; проверяем обе формы каждого запроса.
		for _, alias := range []string{"ГлавныйУзел", "Другой"} {
			t.Run(alias, func(t *testing.T) {
				joined := ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Чужие КАК ` + alias + ` ПО ` + alias + `.Код = Б.Код`
				projection := `ВЫБРАТЬ П.ГлавныйУзел.Учётка КАК Учётка, ` + alias + `.ДругойУзел КАК ДругаяСсылка`
				const plainJoin = ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = Б.Код`
				for _, c := range []struct {
					name, text string
					want       map[string]any
					refs       map[string]string
				}{
					{"projection", projection + joined, map[string]any{"учётка": account.String(), "другаяссылка": base.String()}, map[string]string{"учётка": "Учётки"}},
					{"explicit-reference", `ВЫБРАТЬ П.ГлавныйУзел.Ссылка КАК Узел, ` + alias + `.ДругойУзел КАК ДругаяСсылка` + joined, map[string]any{"узел": base.String(), "другаяссылка": base.String()}, map[string]string{"узел": "Базы"}},
					{"unmatched-plan", projection + ` ИЗ Справочник.Базы КАК Б ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Планы КАК П ПО П.Код = "нет" ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Чужие КАК ` + alias + ` ПО ` + alias + `.Код = Б.Код`, map[string]any{"учётка": nil, "другаяссылка": base.String()}, map[string]string{"учётка": "Учётки"}},
					{"unmatched-other", projection + plainJoin + ` ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Чужие КАК ` + alias + ` ПО ` + alias + `.Код = "нет"`, map[string]any{"учётка": account.String(), "другаяссылка": nil}, map[string]string{"учётка": "Учётки"}},
					{"nested-select", `ВЫБРАТЬ Под.Учётка, Под.ДругаяСсылка ИЗ (` + projection + joined + `) КАК Под`, map[string]any{"учётка": account.String(), "другаяссылка": base.String()}, nil},
					{"correlation", projection + joined + ` ГДЕ П.Код В (ВЫБРАТЬ Б2.Код ИЗ Справочник.Базы КАК Б2 ГДЕ Б2.Учётка = П.ГлавныйУзел.Учётка)`, map[string]any{"учётка": account.String(), "другаяссылка": base.String()}, nil},
					{"local-shadowing", projection + joined + ` ГДЕ П.Код В (ВЫБРАТЬ П.Код ИЗ Справочник.Базы КАК Б2 ЛЕВОЕ СОЕДИНЕНИЕ Справочник.ДругиеПланы КАК П ПО П.Код = Б2.Код ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Чужие КАК ` + alias + ` ПО ` + alias + `.Код = Б2.Код ГДЕ П.ГлавныйУзел.Учётка = "чужая строка")`, map[string]any{"учётка": account.String(), "другаяссылка": base.String()}, nil},
				} {
					t.Run(c.name, func(t *testing.T) {
						r, err := query.Compile(c.text, query.CompileOpts{Entities: ents, Dialect: db.Dialect()})
						if err != nil {
							t.Fatal("Compile:", err)
						}
						for key, target := range c.refs {
							if r.RefColumns[key] != target {
								t.Errorf("RefColumns[%s]=%q, want %q", key, r.RefColumns[key], target)
							}
						}
						rows, _, err := query.Run(ctx, db, &r)
						if err != nil {
							t.Fatalf("Run: %v\nSQL: %s", err, r.SQL)
						}
						if len(rows) != 1 {
							t.Fatalf("rows=%v, want exactly one row\nSQL: %s", rows, r.SQL)
						}
						for key, want := range c.want {
							got, ok := rows[0][key]
							if !ok || (want == nil) != (got == nil) || fmt.Sprint(got) != fmt.Sprint(want) {
								t.Errorf("%s=%v, want %v\nSQL: %s", key, got, want, r.SQL)
							}
						}
					})
				}
			})
		}
	})
}
