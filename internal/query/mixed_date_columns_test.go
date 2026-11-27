package query_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestDocumentAndRegisterDateColumns(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := t.Context()
		dates := []metadata.Field{{Name: "Срок", Type: metadata.FieldTypeDate}, {Name: "Метка", Type: metadata.FieldTypeString}}
		doc := &metadata.Entity{Kind: metadata.KindDocument, Name: "Док", Fields: dates}
		reg := &metadata.InfoRegister{Name: "Сроки", Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}}, Resources: dates}
		opts := query.CompileOpts{Dialect: db.Dialect(), Entities: []*metadata.Entity{doc}, InfoRegs: []*metadata.InfoRegister{reg}}
		require.NoError(t, db.Migrate(ctx, opts.Entities))
		require.NoError(t, db.MigrateInfoRegisters(ctx, opts.InfoRegs))
		for _, hour := range []int{9, 10} {
			stamp := time.Date(2026, 11, 1, hour, 0, 0, 0, time.UTC)
			values := map[string]any{"Срок": stamp, "Метка": stamp.Format(time.RFC3339)}
			require.NoError(t, db.Upsert(ctx, doc.Name, uuid.New(), values, doc))
			require.NoError(t, db.InfoRegSet(ctx, reg, map[string]any{"Ключ": stamp.Format("15")}, values, nil))
		}
		const from = "ВЫБРАТЬ Р.Ключ ИЗ Документ.Док КАК Д СОЕДИНЕНИЕ РегистрСведений.Сроки КАК Р ПО "
		for _, tc := range []struct {
			predicate string
			count     int
		}{
			{"Д.Срок = Р.Срок", 2},
			{"1 = 1 ГДЕ Д.Срок < Р.Срок", 1},
			{"1 = 1 ГДЕ (Р.Срок) > (Д.Срок)", 1},
			{"Д.Метка = Р.Метка", 2},
		} {
			compiled, err := query.Compile(from+tc.predicate, opts)
			require.NoError(t, err)
			rows, _, err := query.Run(ctx, db, &compiled)
			require.NoError(t, err)
			require.Len(t, rows, tc.count, tc.predicate)
		}
		// Both matches and mismatches survive DISTINCT when the comparison is projected.
		compiled, err := query.Compile("ВЫБРАТЬ РАЗЛИЧНЫЕ Д.Срок = Р.Срок КАК Совпали ИЗ Документ.Док КАК Д СОЕДИНЕНИЕ РегистрСведений.Сроки КАК Р ПО 1 = 1", opts)
		require.NoError(t, err)
		rows, _, err := query.Run(ctx, db, &compiled)
		require.NoError(t, err)
		require.Len(t, rows, 2)

		// Dates inside a larger expression and string fields are not date operands.
		for _, predicate := range []string{"Д.Срок = Р.Срок + 1", "Д.Метка = Р.Срок", "Р.Срок = Д.Метка"} {
			compiled, err := query.Compile(from+predicate, opts)
			require.NoError(t, err)
			require.NotContains(t, compiled.SQL, "ob_utc_datetime", predicate)
		}
	})
}
