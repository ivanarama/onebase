package interpreter_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// RunSandboxed — публичный путь исполнения, используемый onebase eval.
func TestForEach_Structure(t *testing.T) {
	for _, tc := range []struct {
		name, setup, body string
		want              []any
	}{
		{"pairs", `С = Новый Структура("Имя,Ноль,Флаг,Пусто", "текст", 0, Ложь, Неопределено);`,
			`Р.Добавить(Э.Ключ); Р.Добавить(Э.Значение); Р.Добавить(ТипЗнч(Э));`,
			[]any{"имя", "текст", "КлючИЗначение", "ноль", decimal.NewFromInt(0), "КлючИЗначение", "флаг", false, "КлючИЗначение", "пусто", nil, "КлючИЗначение"}},
		{"empty", `С = Новый Структура;`, `Р.Добавить(Э.Ключ);`, nil},
		{"replace-keeps-position", `С = Новый Структура("А,Б,В", 1, 2, 3);
С.Вставить("А", 10); С.Б = 20;`,
			`Р.Добавить(Э.Ключ); Р.Добавить(Э.Значение);`, []any{"а", decimal.NewFromInt(10), "б", decimal.NewFromInt(20), "в", decimal.NewFromInt(3)}},
		{"replace-delete-reinsert", `С = Новый Структура("А,Б,В", 1, 2, 3);
С.Вставить("А", 10); С.Б = 20; С.Удалить("А"); С.Вставить("А", 100);`,
			`Р.Добавить(Э.Ключ); Р.Добавить(Э.Значение);`, []any{"б", decimal.NewFromInt(20), "в", decimal.NewFromInt(3), "а", decimal.NewFromInt(100)}},
		{"break-continue", `С = Новый Структура("А,Б,В,Г", 1, 2, 3, 4);`,
			`Если Э.Ключ = "б" Тогда Продолжить; КонецЕсли;
Если Э.Ключ = "г" Тогда Прервать; КонецЕсли;
Р.Добавить(Э.Значение);`, []any{decimal.NewFromInt(1), decimal.NewFromInt(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := parseProc(t, "Функция Тест()\n"+tc.setup+`
Р = Новый Массив;
Для Каждого Э Из С Цикл
`+tc.body+`
КонецЦикла;
Возврат Р;
КонецФункции`)
			var result any
			require.NoError(t, interpreter.New().RunSandboxed(proc, nil, interpreter.RestrictedProfile(), &result))
			require.Equal(t, tc.want, result.(*interpreter.Array).Iterate())
		})
	}
}

func TestForEach_NonIterableIsCatchableDSLError(t *testing.T) {
	for expr, typ := range map[string]string{"17": "Число", `"текст"`: "Строка", "Ложь": "Булево", "Неопределено": "Неопределено"} {
		t.Run(typ, func(t *testing.T) {
			proc := parseProc(t, "Функция Тест()\nДля Каждого Э Из "+expr+" Цикл\nКонецЦикла;\nКонецФункции")
			err := interpreter.New().RunSandboxed(proc, nil, interpreter.RestrictedProfile(), nil)
			var dslErr *interpreter.DSLError
			require.ErrorAs(t, err, &dslErr)
			require.Equal(t, "test.os", dslErr.File)
			require.Equal(t, 2, dslErr.Line)
			require.Contains(t, dslErr.Msg, "Для Каждого")
			require.Contains(t, dslErr.Msg, "не является коллекцией")

			caught := parseProc(t, "Функция Тест()\nПопытка\nДля Каждого Э Из "+expr+` Цикл
КонецЦикла;
Исключение
Возврат Истина;
КонецПопытки;
Возврат Ложь;
КонецФункции`)
			var result any
			require.NoError(t, interpreter.New().RunSandboxed(caught, nil, interpreter.RestrictedProfile(), &result))
			require.Equal(t, true, result)
		})
	}
}

type foreachThisCollection struct{}

func (foreachThisCollection) IterateThis() []interpreter.This {
	return []interpreter.This{&interpreter.MapThis{M: map[string]any{"Сумма": float64(2)}}, &interpreter.MapThis{M: map[string]any{"Сумма": float64(3)}}}
}

type foreachRowsCollection struct{}

func (foreachRowsCollection) IterateRows() []map[string]any {
	return []map[string]any{{"Сумма": float64(2)}, {"Сумма": float64(3)}}
}

func TestForEach_ExistingCollections(t *testing.T) {
	for name, coll := range map[string]any{
		"IterateThis": foreachThisCollection{}, "IterateRows": foreachRowsCollection{},
		"slice": []any{&interpreter.MapThis{M: map[string]any{"Сумма": float64(2)}}, &interpreter.MapThis{M: map[string]any{"Сумма": float64(3)}}},
		"rows":  []map[string]any{{"Сумма": float64(2)}, {"Сумма": float64(3)}},
	} {
		t.Run(name, func(t *testing.T) {
			proc := parseProc(t, `Функция Тест()
Сумма = 0;
Для Каждого Э Из Коллекция Цикл Сумма = Сумма + Э.Сумма; КонецЦикла;
Возврат Сумма;
КонецФункции`)
			var result any
			require.NoError(t, interpreter.New().RunWithResult(proc, nil, &result, map[string]any{"Коллекция": coll}))
			require.True(t, numEq(result, 5), "получено: %v", result)
		})
	}
}

func TestForEach_StructureHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proc := parseProc(t, `Функция Тест()
Для Каждого Э Из Новый Структура("А,Б", 1, 2) Цикл
  Остановить();
КонецЦикла;
КонецФункции`)
	err := interpreter.New().RunSandboxed(proc, nil, interpreter.SandboxProfile{Context: ctx}, nil,
		map[string]any{"Остановить": interpreter.BuiltinFunc(func([]any, string, int) (any, error) { cancel(); return nil, nil })})
	require.True(t, errors.Is(err, context.Canceled), "получено: %v", err)
}

func TestForEach_StructureInReadOnlyBreakpoint(t *testing.T) {
	helper := parseProc(t, `Функция Сумма()
Сумма = 0;
Коллекция = Новый Структура("А,Б", 2, 3);
Для Каждого Э Из Коллекция Цикл
  Сумма = Сумма + Э.Значение;
КонецЦикла;
Возврат Сумма;
КонецФункции`)
	in := interpreter.New()
	in.LookupProc = func(name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "Сумма") {
			return helper
		}
		return nil
	}
	hook := runWithConditionInterp(t, in, "Сумма() = 5", nil)
	require.NoError(t, hook.err)
	require.True(t, hook.stopped)
}
