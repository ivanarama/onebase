package interpreter_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func runStringConversion(t *testing.T, src string, vars map[string]any) any {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "string_conversion.os")).ParseProgram()
	require.NoError(t, err)
	require.Len(t, prog.Procedures, 1)
	var result any
	require.NoError(t, interpreter.New().RunWithResult(prog.Procedures[0],
		runtime.NewObject("Test", metadata.KindDocument), &result, vars))
	return result
}

func TestToString_EmptyValuesThroughDSL(t *testing.T) {
	for _, alias := range []string{"Строка", "Str"} {
		for _, value := range []string{"Неопределено", "Null", "Пустое", "Данные.Пустое"} {
			t.Run(alias+"/"+value, func(t *testing.T) {
				call := alias + "(" + value + ")"
				src := `Функция Тест()
					Перем Пустое;
					Данные = Новый Структура("Пустое", Неопределено);
					Возврат "[" + ` + call + ` + "]|[" + СокрЛП(` + call + `)
						+ "]|[" + Лев(` + call + `, 2) + "]|" + Строка(ЗначениеЗаполнено(` + call + `));
				КонецФункции`
				require.Equal(t, "[]|[]|[]|false", runStringConversion(t, src, nil))
			})
		}
	}
}

func TestToString_ExistingConversionsThroughDSL(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"", ""}, // Вызов без аргумента.
		{`""`, ""},
		{`"  Текст  "`, "  Текст  "},
		{`"<nil>"`, "<nil>"},
		{"0", "0"},
		{"42", "42"},
		{"-12.5", "-12.5"},
		{"40802810000000000001.25", "40802810000000000001.25"},
		{"Истина", "true"},
		{"Ложь", "false"},
		{"Дата(2026, 10, 14, 12, 34, 56)", "14.10.2026 12:34:56"},
		{"Ссылка", "Гвозди"},
	}
	for _, alias := range []string{"Строка", "Str"} {
		for _, tc := range cases {
			t.Run(alias+"/"+tc.value, func(t *testing.T) {
				src := "Функция Тест()\nВозврат " + alias + "(" + tc.value + ");\nКонецФункции"
				vars := map[string]any{"Ссылка": &interpreter.MapThis{M: map[string]any{"наименование": "Гвозди"}}}
				require.Equal(t, tc.want, runStringConversion(t, src, vars))
			})
		}
	}
}

func TestToString_QueryNULLMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "ПустыеСтроки", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{entity}
		require.NoError(t, db.Migrate(ctx, entities))
		require.NoError(t, db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Наименование": "Тест"}, entity))
		const text = `ВЫБРАТЬ NULL КАК Пустое ИЗ Справочник.ПустыеСтроки`
		compiled, err := query.Compile(text, query.CompileOpts{Entities: entities, Dialect: db.Dialect()})
		require.NoError(t, err)
		rows, _, err := query.Run(ctx, db, &compiled)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Contains(t, rows[0], "пустое")
		require.Nil(t, rows[0]["пустое"], "SQL NULL сохраняется в общем query runner")
		encoded, err := json.Marshal(rows[0]["пустое"])
		require.NoError(t, err)
		require.Equal(t, "null", string(encoded))

		src := `Процедура Тест()
			Запрос = Новый Запрос;
			Запрос.Текст = "` + text + `";
			Стр = Запрос.Выполнить()[0];
			Возврат "[" + Строка(Стр.Пустое) + "]|[" + Str(Стр.Пустое)
				+ "]|[" + СокрЛП(Строка(Стр.Пустое)) + "]|[" + Лев(Str(Стр.Пустое), 2)
				+ "]|" + ЗаписатьJSON(Стр.Пустое);
		КонецПроцедуры`
		require.Equal(t, "[]|[]|[]|[]|null", runOnDBEntities(t, db, entities, src))
	})
}
