package query_test

import (
	"context"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestQueryLowerUnicode(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, text := range []string{"Контакт ЁЖ Alice", "КОНТАКТ ЁЖ ALICE", "контакт ёж alice", "КоНтАкТ Ёж AlIcE"} {
			compiled, err := query.Compile(`ВЫБРАТЬ НРег(&Текст) КАК Результат ГДЕ НРег(&Текст) ПОДОБНО &Поиск`, query.CompileOpts{Dialect: db.Dialect(), Params: map[string]any{"Текст": text, "Поиск": "%контакт ёж alice%"}})
			require.NoError(t, err)
			rows, _, err := query.Run(context.Background(), db, &compiled)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, "контакт ёж alice", rows[0]["результат"])
		}
	})
}
