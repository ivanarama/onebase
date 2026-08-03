package interpreter_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// stubDB — mock QueryDB для тестов.
type stubDB struct {
	rows []map[string]any
	err  error
}

func (s *stubDB) QueryAll(_ context.Context, _ string, _ ...any) ([]map[string]any, error) {
	return s.rows, s.err
}
func (s *stubDB) Dialect() storage.Dialect { return nil }

// stubReg — mock QueryRegistry.
type stubReg struct{}

func (s *stubReg) Registers() []*metadata.Register               { return nil }
func (s *stubReg) InfoRegisters() []*metadata.InfoRegister       { return nil }
func (s *stubReg) AccountRegisters() []*metadata.AccountRegister { return nil }
func (s *stubReg) Entities() []*metadata.Entity                  { return nil }

func evalQuery(t *testing.T, src string, db interpreter.QueryDB, reg interpreter.QueryRegistry) any {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	require.NotEmpty(t, prog.Procedures, "no procedures")

	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)

	factory := interpreter.NewQueryFactory(context.Background(), db, reg)
	extra := map[string]any{
		"__factory_Запрос": factory,
		"__factory_Query":  factory,
	}
	var result any
	err2 := interp.RunWithResult(prog.Procedures[0], obj, &result, extra)
	require.NoError(t, err2)
	return result
}

func TestQuery_Execute_ReturnsArrayOfStruct(t *testing.T) {
	db := &stubDB{
		rows: []map[string]any{
			{"Наименование": "Кабель", "Количество": 10},
			{"Наименование": "Провод", "Количество": 5},
		},
	}
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Текст = "ВЫБРАТЬ Наименование, Количество ИЗ Справочник.Номенклатура";
		Результат = Запрос.Выполнить();
		Возврат Результат.Количество();
	КонецПроцедуры`
	result := evalQuery(t, src, db, &stubReg{})
	assert.Equal(t, float64(2), result)
}

func TestQuery_Execute_AccessFields(t *testing.T) {
	db := &stubDB{
		rows: []map[string]any{
			{"Наименование": "Кабель"},
		},
	}
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Номенклатура";
		Результат = Запрос.Выполнить();
		Если Результат.Количество() > 0 Тогда
			Возврат Результат[0].Наименование;
		КонецЕсли;
		Возврат "";
	КонецПроцедуры`
	result := evalQuery(t, src, db, &stubReg{})
	assert.Equal(t, "Кабель", result)
}

func TestQuery_Execute_AccessReferenceReservedAlias(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name  string
		query string
		field string
	}{
		{
			name:  "Russian",
			query: "ВЫБРАТЬ О.Ссылка КАК Ссылка ИЗ Документ.Обращение КАК О",
			field: "Ссылка",
		},
		{
			name:  "English",
			query: "SELECT O.Reference AS Reference FROM Document.Обращение AS O",
			field: "Reference",
		},
		{
			name:  "short English alias",
			query: "SELECT O.Ref AS Ref FROM Document.Обращение AS O",
			field: "Ref",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &stubDB{rows: []map[string]any{{"id": id}}}
			src := `Процедура Тест()
				Запрос = Новый Запрос;
				Запрос.Текст = "` + tt.query + `";
				Результат = Запрос.Выполнить();
				Возврат Результат[0].` + tt.field + `;
			КонецПроцедуры`

			result := evalQuery(t, src, db, &stubReg{})

			assert.Equal(t, id, result)
		})
	}
}

func TestQuery_SetParameter(t *testing.T) {
	var capturedSQL string
	var capturedArgs []any
	db := &captureDB{onQuery: func(sql string, args []any) {
		capturedSQL = sql
		capturedArgs = args
	}}
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Номенклатура ГДЕ Наименование = &Имя";
		Запрос.УстановитьПараметр("Имя", "Кабель");
		Запрос.Выполнить();
		Возврат 1;
	КонецПроцедуры`
	evalQuery(t, src, db, &stubReg{})
	assert.NotEmpty(t, capturedSQL)
	assert.Contains(t, capturedArgs, "Кабель")
}

// Объект документа и оборачивающий его UI this реализуют GetRefUUID. Параметр
// запроса должен получить обычную UUID-строку, а не внутренний Go-объект,
// который PostgreSQL-драйвер не умеет кодировать.
func TestQuery_ReferenceLikeParameter_UnwrapsUUID(t *testing.T) {
	var capturedArgs []any
	db := &captureDB{onQuery: func(_ string, args []any) {
		capturedArgs = args
	}}
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Текст = "ВЫБРАТЬ Номер ИЗ Документ.Заказ ГДЕ Ссылка <> &ЭтотДокумент";
		Запрос.УстановитьПараметр("ЭтотДокумент", ЭтотОбъект);
		Запрос.Выполнить();
	КонецПроцедуры`
	evalQuery(t, src, db, &stubReg{})

	require.Len(t, capturedArgs, 1)
	id, ok := capturedArgs[0].(string)
	require.Truef(t, ok, "параметр имеет тип %T, ожидалась UUID-строка", capturedArgs[0])
	_, err := uuid.Parse(id)
	require.NoError(t, err)
}

func TestQuery_EmptyText_Panics(t *testing.T) {
	db := &stubDB{}
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Выполнить();
		Возврат 1;
	КонецПроцедуры`
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, _ := p.ParseProgram()
	interp := interpreter.New()
	obj := runtime.NewObject("Test", metadata.KindDocument)
	factory := interpreter.NewQueryFactory(context.Background(), db, &stubReg{})
	extra := map[string]any{"__factory_Запрос": factory}
	err := interp.Run(prog.Procedures[0], obj, extra)
	assert.Error(t, err)
}

func TestQuery_TextProperty_GetSet(t *testing.T) {
	db := &stubDB{rows: []map[string]any{{"x": 1}}}
	src := `Процедура Тест()
		Запрос = Новый Запрос;
		Запрос.Текст = "SELECT 1";
		Возврат Запрос.Текст;
	КонецПроцедуры`
	result := evalQuery(t, src, db, &stubReg{})
	assert.Equal(t, "SELECT 1", result)
}

// captureDB перехватывает SQL+args без реального выполнения.
type captureDB struct {
	onQuery func(sql string, args []any)
}

func (c *captureDB) QueryAll(_ context.Context, sql string, args ...any) ([]map[string]any, error) {
	if c.onQuery != nil {
		c.onQuery(sql, args)
	}
	return nil, nil
}
func (c *captureDB) Dialect() storage.Dialect { return nil }

// TestQuery_ArrayParam_ExpandsInClause проверяет что Массив в IN (&Param)
// раскрывается в несколько позиционных параметров, а не передаётся как один объект.
func TestQuery_ArrayParam_ExpandsInClause(t *testing.T) {
	var capturedSQL string
	var capturedArgs []any
	db := &captureDB{onQuery: func(sql string, args []any) {
		capturedSQL = sql
		capturedArgs = args
	}}
	src := `Процедура Тест()
		Список = Новый Массив;
		Список.Добавить("uuid-1");
		Список.Добавить("uuid-2");
		Список.Добавить("uuid-3");
		Запрос = Новый Запрос;
		Запрос.Текст = "ВЫБРАТЬ Наименование ИЗ Справочник.Номенклатура ГДЕ Ссылка В (&Список)";
		Запрос.УстановитьПараметр("Список", Список);
		Запрос.Выполнить();
		Возврат 1;
	КонецПроцедуры`
	evalQuery(t, src, db, &stubReg{})
	assert.NotEmpty(t, capturedSQL)
	// Должно быть 3 отдельных аргумента, не один *Array
	assert.Len(t, capturedArgs, 3)
	assert.Equal(t, "uuid-1", capturedArgs[0])
	assert.Equal(t, "uuid-2", capturedArgs[1])
	assert.Equal(t, "uuid-3", capturedArgs[2])
}
