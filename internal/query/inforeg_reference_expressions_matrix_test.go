package query_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Reference resources need output aliases only as complete SELECT items, never
// inside an aggregate, scalar function or larger expression (PR #1961).
func TestInfoRegisterReferenceExpressionsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ad := &metadata.Entity{Name: "Реклама", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{ad}
		require.NoError(t, db.Migrate(ctx, entities))
		adID := uuid.New()
		require.NoError(t, db.Upsert(ctx, ad.Name, adID, map[string]any{"Наименование": "Объявление"}, ad))
		for _, periodic := range []bool{false, true} {
			t.Run(fmt.Sprintf("periodic=%t", periodic), func(t *testing.T) {
				ir := &metadata.InfoRegister{
					Name: fmt.Sprintf("РесурсыПробы%t", periodic), Periodic: periodic,
					Dimensions: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
					Resources:  []metadata.Field{{Name: "Реклама", Type: "reference:Реклама", RefEntity: "Реклама"}},
				}
				require.NoError(t, db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}))
				var period *time.Time
				if periodic {
					value := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
					period = &value
				}
				require.NoError(t, db.InfoRegSet(ctx, ir, map[string]any{"Номер": "1"}, map[string]any{"Реклама": adID.String()}, period))
				opts := query.CompileOpts{Entities: entities, InfoRegs: []*metadata.InfoRegister{ir}, Dialect: db.Dialect()}
				for _, source := range []string{"", ".СрезПоследних()", ".СрезПервых()"} {
					t.Run("source="+source, func(t *testing.T) {
						for _, tc := range []struct {
							projection, column, want string
						}{
							{"КОЛИЧЕСТВО(Реклама) КАК Количество", "количество", "1"},
							{"КОЛИЧЕСТВО(Реклама) + 1 КАК Количество", "количество", "2"},
							{"МАКСИМУМ(Реклама) КАК Значение", "значение", "Объявление"},
							{"COALESCE(Реклама, \"пусто\") КАК Значение", "значение", "Объявление"},
							{"ВЫБОР КОГДА Реклама ЕСТЬ NULL ТОГДА \"пусто\" ИНАЧЕ Реклама КОНЕЦ КАК Значение", "значение", "Объявление"},
							{"Реклама", "реклама", "Объявление"},
							{"Номер, Реклама", "реклама", "Объявление"},
							{"РАЗЛИЧНЫЕ Реклама", "реклама", "Объявление"},
							{"Реклама КАК Значение", "значение", "Объявление"},
						} {
							t.Run(tc.projection, func(t *testing.T) {
								compiled, err := query.Compile("ВЫБРАТЬ "+tc.projection+" ИЗ РегистрСведений."+ir.Name+source, opts)
								require.NoError(t, err)
								rows, _, err := query.Run(ctx, db, &compiled)
								require.NoError(t, err, compiled.SQL)
								require.Len(t, rows, 1)
								require.Contains(t, rows[0], tc.column)
								require.Equal(t, tc.want, fmt.Sprint(rows[0][tc.column]), compiled.SQL)
							})
						}
					})
				}
			})
		}
	})
}
