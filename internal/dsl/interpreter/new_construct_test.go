package interpreter_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// runNew создаёт объект статической формой Новый через публичный прогон модуля
// и возвращает то, что модуль вернул (план 173, срез A).
func runNew(t *testing.T, src string, extra map[string]any) any {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)

	interp := interpreter.New()
	var result any
	var vars []map[string]any
	if extra != nil {
		vars = append(vars, extra)
	}
	require.NoError(t, interp.RunWithResult(prog.Procedures[0], nil, &result, vars...))
	return result
}

func TestNewStaticFormsThroughConstruct(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want any
	}{
		{"массив", "Процедура Тест()\n\tВозврат Новый Массив;\nКонецПроцедуры\n", &interpreter.Array{}},
		{"соответствие", "Процедура Тест()\n\tВозврат Новый Соответствие;\nКонецПроцедуры\n", &interpreter.Map{}},
		{"структура", "Процедура Тест()\n\tВозврат Новый Структура;\nКонецПроцедуры\n", &interpreter.Struct{}},
		{"таблицазначений", "Процедура Тест()\n\tВозврат Новый ТаблицаЗначений;\nКонецПроцедуры\n", &interpreter.ValueTable{}},
		{"регекс", "Процедура Тест()\n\tВозврат Новый Регекс(\"а\");\nКонецПроцедуры\n", nil},
		{"шаблонhtml", "Процедура Тест()\n\tВозврат Новый ШаблонHTML(\"<b>х</b>\");\nКонецПроцедуры\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runNew(t, tc.src, nil)
			require.NotNil(t, got)
			if tc.want != nil {
				assert.IsType(t, tc.want, got)
			}
		})
	}
}

func TestNewUnknownTypeKeepsUserError(t *testing.T) {
	l := lexer.New("Процедура Тест()\n\tХ = Новый НесуществующийТип;\nКонецПроцедуры\n", "test.os")
	prog, err := parser.New(l).ParseProgram()
	require.NoError(t, err)

	interp := interpreter.New()
	var result any
	err = interp.RunWithResult(prog.Procedures[0], nil, &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Новый: неизвестный тип НесуществующийТип")
}

// Внешняя фабрика получает те же аргументы, что и раньше: срез A — чистый
// рефакторинг dispatch, поведение не меняется.
func TestNewInjectedFactoryReceivesSameArgs(t *testing.T) {
	var gotArgs []any
	factory := func(args []any) any {
		gotArgs = args
		return "создано фабрикой"
	}
	got := runNew(t, "Процедура Тест()\n\tВозврат Новый МойТип(1, \"два\");\nКонецПроцедуры\n",
		map[string]any{"__factory_мойтип": factory})
	assert.Equal(t, "создано фабрикой", got)
	require.Len(t, gotArgs, 2)
	assert.Equal(t, "1", fmt.Sprint(gotArgs[0]))
	assert.Equal(t, "два", gotArgs[1])
}

// Обходной фрагмент: dispatch-helper не должен ломать цепочки свойств после
// конструктора (постфикс после Новый остаётся на месте).
func TestNewStaticWithPostfixChain(t *testing.T) {
	got := runNew(t, "Процедура Тест()\n\tХ = Новый Соответствие;\n\tХ.Вставить(\"Ключ\", 7);\n\tВозврат Х.Получить(\"Ключ\");\nКонецПроцедуры\n", nil)
	assert.Equal(t, "7", fmt.Sprint(got))
}
