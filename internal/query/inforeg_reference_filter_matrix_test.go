package query_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// A SELECT alias must not hide the register's input column in WHERE, even
// when the resource auto-JOIN brings an identically named reference field.
func TestInfoRegisterReferenceFilterAliasMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Основание", Type: "reference:Заявка", RefEntity: "Заявка"},
			}}
		entities := []*metadata.Entity{doc}
		require.NoError(t, db.Migrate(ctx, entities))
		firstID, secondID := uuid.New(), uuid.New()
		require.NoError(t, db.Upsert(ctx, doc.Name, secondID, map[string]any{"Номер": "002"}, doc))
		require.NoError(t, db.Upsert(ctx, doc.Name, firstID, map[string]any{
			"Номер": "001", "Основание": secondID.String(),
		}, doc))
		ir := &metadata.InfoRegister{Name: "РесурсыПробы",
			Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Основание", Type: "reference:Заявка", RefEntity: "Заявка"}},
		}
		require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}))
		for key, id := range map[string]uuid.UUID{"first": firstID, "second": secondID} {
			require.NoError(t, db.InfoRegSet(ctx, ir, map[string]any{"Ключ": key}, map[string]any{"Основание": id.String()}, nil))
		}
		for _, sourceAlias := range []string{"", " КАК Регистр"} {
			t.Run("source="+sourceAlias, func(t *testing.T) {
				for _, tc := range []struct{ projection, column string }{
					{"Основание", "основание"},
					{"Основание КАК Основание", "основание"},
					{"Основание КАК Док", "док"},
				} {
					t.Run(tc.projection, func(t *testing.T) {
						for _, order := range []string{"", " УПОРЯДОЧИТЬ ПО " + tc.column} {
							t.Run("order="+order, func(t *testing.T) {
								compiled, err := query.Compile("ВЫБРАТЬ "+tc.projection+
									" ИЗ РегистрСведений."+ir.Name+sourceAlias+" ГДЕ Основание = &Р"+order,
									query.CompileOpts{Entities: entities, InfoRegs: []*metadata.InfoRegister{ir},
										Dialect: db.Dialect(), Params: map[string]any{"Р": firstID.String()}})
								require.NoError(t, err)
								rows, _, err := query.Run(ctx, db, &compiled)
								require.NoError(t, err, compiled.SQL)
								require.Len(t, rows, 1, compiled.SQL)
								require.Equal(t, "001", rows[0][tc.column], compiled.SQL)
							})
						}
					})
				}
			})
		}
	})
}
