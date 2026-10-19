package interpreter_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

const numberConversionMessage = "Преобразование значения к типу Число не может быть выполнено"

// Проверяем исполнение обработчика и неизменность левой части присваивания
// через публичные входы, включая restricted sandbox и lexical scope.
func TestToNumberStringCaught(t *testing.T) {
	for _, alias := range []string{"Число", "Number"} {
		for _, input := range []string{"Не", "", " 12", "12 ", "12x", "1,2,3"} {
			for _, entry := range locationEntrypoints() {
				for _, strict := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%q/%s/strict=%v", alias, input, entry.name, strict), func(t *testing.T) {
						source := fmt.Sprintf(`Процедура Тест()
 Результат.Час = 23;
 Попытка
  Результат.Час = %s(Вход);
 Исключение
  Результат.Поймано = Истина;
  Результат.Описание = ОписаниеОшибки();
  Результат.Инфо = ИнформацияОбОшибке();
 КонецПопытки;
КонецПроцедуры`, alias)
						result := interpreter.NewStructFromMap(map[string]any{"Час": nil, "Поймано": false, "Описание": nil, "Инфо": nil})
						in := interpreter.New()
						in.StrictLexicalScope = strict
						require.NoError(t, entry.run(in, parseProc(t, source), map[string]any{"Вход": input, "Результат": result}))
						assert.Equal(t, "23", fmt.Sprint(result.Get("Час")))
						assert.Equal(t, true, result.Get("Поймано"))
						assert.Equal(t, numberConversionMessage, result.Get("Описание"))
						info, ok := result.Get("Инфо").(*interpreter.Struct)
						require.True(t, ok)
						assert.Equal(t, numberConversionMessage, info.Get("Описание"))
						assert.Equal(t, "test.os", info.Get("Источник"))
						assert.Equal(t, float64(4), info.Get("НомерСтроки"))
					})
				}
			}
		}
	}
}

func TestToNumberStringNestedRethrow(t *testing.T) {
	for _, alias := range []string{"Число", "Number"} {
		for _, entry := range locationEntrypoints() {
			for _, tc := range []struct {
				name, body string
				line       int
			}{
				{"direct", "  Результат = " + alias + "(Вход);", 2},
				{"nested-rethrow", ` Попытка
  Попытка
   Результат = ` + alias + `(Вход);
  Исключение
   Попытка
    ВызватьИсключение("внутренняя");
   Исключение
    Результат = ОписаниеОшибки();
   КонецПопытки;
   Если ОписаниеОшибки() <> "` + numberConversionMessage + `"
    Или ИнформацияОбОшибке().Источник <> "test.os"
    Или ИнформацияОбОшибке().НомерСтроки <> 4 Тогда
    ВызватьИсключение("Потеряны данные исходного исключения");
   КонецЕсли;
   ВызватьИсключение;
  КонецПопытки;
 Исключение
  ВызватьИсключение();
 КонецПопытки;`, 4},
			} {
				for _, input := range []string{"Не", ""} {
					t.Run(fmt.Sprintf("%s/%s/%s/%q", alias, entry.name, tc.name, input), func(t *testing.T) {
						err := entry.run(interpreter.New(), parseProc(t, "Процедура Тест()\n"+tc.body+"\nКонецПроцедуры"), map[string]any{"Вход": input})
						var dsl *interpreter.DSLError
						require.ErrorAs(t, err, &dsl)
						assert.Equal(t, numberConversionMessage, dsl.Msg)
						assert.Equal(t, "test.os", dsl.File)
						assert.Equal(t, tc.line, dsl.Line)
					})
				}
			}
		}
	}
}

func TestToNumberCompatibility(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"dot", "12.5", "12.5"}, {"comma", "12,5", "12.5"},
		{"negative", "-12,5", "-12.5"}, {"exponent", "1e3", "1000"},
		{"precise-string", "12345678901234567890.123456789", "12345678901234567890.123456789"},
		{"decimal", decimal.RequireFromString("12345678901234567890.123456789"), "12345678901234567890.123456789"},
		{"integer", int64(12), "12"}, {"float", 12.5, "12.5"},
		{"true", true, "1"}, {"false", false, "0"}, {"nil", nil, "0"},
		{"date", time.Date(2026, 5, 11, 14, 30, 45, 0, time.FixedZone("test", 10800)), "20260511143045"},
		{"empty-date", time.Time{}, "0"},
		{"out-of-range-date", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), "0"},
		{"map", map[string]any{"x": 1}, "0"}, {"array", []any{1}, "0"},
	}
	for _, alias := range []string{"Число", "Number"} {
		for _, tc := range cases {
			t.Run(alias+"/"+tc.name, func(t *testing.T) {
				got := evalEnv(t, "Функция Тест()\n Возврат "+alias+"(Вход);\nКонецФункции", map[string]any{"Вход": tc.input})
				require.IsType(t, decimal.Decimal{}, got)
				assert.Equal(t, tc.want, got.(decimal.Decimal).String())
			})
		}
		for _, expr := range []string{alias + "()", alias + "(Неопределено)", alias + "(Null)"} {
			t.Run(expr, func(t *testing.T) {
				got := evalEnv(t, "Функция Тест()\n Возврат "+expr+";\nКонецФункции")
				assert.Equal(t, decimal.Zero, got)
			})
		}
	}
}

// NULL выражения остаётся nil; NULL прямого строкового поля DSL уже
// превращает в пустую строку. Этот контракт одинаков на обоих диалектах.
func TestToNumberQueryMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "СтрокиЧисла", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeString},
		}}
		require.NoError(t, db.Migrate(ctx, []*metadata.Entity{ent}))
		for _, value := range []string{"Не", "12,5"} {
			require.NoError(t, db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": value, "Текст": value}, ent))
		}
		// Подготовка реального SQL NULL, который обычная запись нормализует.
		_, err := db.Exec(ctx, `UPDATE "строкичисла" SET "текст" = NULL WHERE "наименование" = 'Не'`)
		require.NoError(t, err)
		for _, alias := range []string{"Число", "Number"} {
			source := fmt.Sprintf(`Функция Тест()
 Запрос = Новый Запрос;
 Запрос.Текст = "ВЫБРАТЬ Наименование КАК Плохая, Текст, NULL КАК ПустоеЗначение ИЗ Справочник.СтрокиЧисла ГДЕ Наименование = ""Не""";
 Стр = Запрос.Выполнить()[0];
 Плохая = 23;
 Поймано = 0;
 Попытка
  Плохая = %s(Стр.Плохая);
 Исключение
  Поймано = Поймано + 1;
 КонецПопытки;
 Поле = 23;
 Попытка
  Поле = %s(Стр.Текст);
 Исключение
  Поймано = Поймано + 1;
 КонецПопытки;
 Ноль = %s(Стр.ПустоеЗначение);
 Запрос.Текст = "ВЫБРАТЬ Текст ИЗ Справочник.СтрокиЧисла ГДЕ Наименование = ""12,5""";
 Хорошая = %s(Запрос.Выполнить()[0].Текст);
 Возврат Строка(Плохая) + "|" + Строка(Поле) + "|" + Строка(Поймано) + "|" + Строка(Ноль) + "|" + Строка(Хорошая);
КонецФункции`, alias, alias, alias, alias)
			t.Run(alias, func(t *testing.T) { assert.Equal(t, "23|23|2|0|12.5", runOnDB(t, db, ent, source)) })
		}
	})
}
