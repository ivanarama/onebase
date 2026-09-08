package query_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestTypedColumns_FailClosed(t *testing.T) {
	left := &metadata.Entity{
		Name: "Левая", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "УникальноеЧисло", Type: metadata.FieldTypeNumber},
			{Name: "Конфликт", Type: metadata.FieldTypeNumber},
		},
	}
	right := &metadata.Entity{
		Name: "Правая", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "УникальнаяСтрока", Type: metadata.FieldTypeString},
			{Name: "Конфликт", Type: metadata.FieldTypeString},
		},
	}
	opts := query.CompileOpts{Entities: []*metadata.Entity{left, right}, Dialect: storage.SQLiteDialect{}}

	cases := []struct {
		name string
		src  string
	}{
		{
			name: "уникальное поле второго источника",
			src:  `ВЫБРАТЬ п.УникальнаяСтрока ИЗ Справочник.Левая КАК л ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Правая КАК п ПО 1 = 1`,
		},
		{
			name: "одно имя разных типов",
			src:  `ВЫБРАТЬ л.Конфликт, п.Конфликт ИЗ Справочник.Левая КАК л ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Правая КАК п ПО 1 = 1`,
		},
		{
			name: "выражение",
			src:  `ВЫБРАТЬ УникальноеЧисло + 1 КАК Итог ИЗ Справочник.Левая`,
		},
		{
			name: "объединение",
			src:  `ВЫБРАТЬ УникальноеЧисло ИЗ Справочник.Левая ОБЪЕДИНИТЬ ВСЕ ВЫБРАТЬ Конфликт ИЗ Справочник.Левая`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := query.Compile(tc.src, opts)
			require.NoError(t, err)
			assert.Empty(t, res.TypedColumns)
		})
	}
}

func TestTypedColumns_RegisterSystemFieldsAreExplicit(t *testing.T) {
	reg := &metadata.Register{Name: "Остатки"}
	res, err := query.Compile(
		`ВЫБРАТЬ Период, ВидДвижения ИЗ РегистрНакопления.Остатки`,
		query.CompileOpts{Registers: []*metadata.Register{reg}, Dialect: storage.SQLiteDialect{}},
	)
	require.NoError(t, err)
	require.Equal(t, metadata.FieldTypeDate, res.TypedColumns["период"].Type)
	require.Equal(t, metadata.FieldTypeString, res.TypedColumns["виддвижения"].Type)

	entity := &metadata.Entity{
		Name: "Произвольное", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Период", Type: metadata.FieldTypeNumber}},
	}
	res, err = query.Compile(
		`ВЫБРАТЬ Период ИЗ Справочник.Произвольное`,
		query.CompileOpts{Entities: []*metadata.Entity{entity}, Dialect: storage.SQLiteDialect{}},
	)
	require.NoError(t, err)
	require.Equal(t, metadata.FieldTypeNumber, res.TypedColumns["период"].Type)
}
