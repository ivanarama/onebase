package interpreter_test

import (
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// RunSandboxed is the execution entry point used by onebase eval.
func TestStructPropertyPreservesValueContract(t *testing.T) {
	for _, method := range []string{"Свойство", "сВоЙсТвО", "Property", "pRoPeRtY"} {
		t.Run(method, func(t *testing.T) {
			proc := parseProc(t, fmt.Sprintf(`Функция Тест()
С = Новый Структура("Текст,Число,Ноль,ПустаяСтрока,Флаг,Пусто", "значение", 7, 0, "", Ложь, Неопределено);
Р = Новый Массив;
Р.Добавить(С.%[1]s("тЕКСТ"));
Р.Добавить(С.%[1]s("число"));
Р.Добавить(С.%[1]s("ноль"));
Р.Добавить(С.%[1]s("пустаястрока"));
Р.Добавить(С.%[1]s("флаг"));
Р.Добавить(С.%[1]s("пусто"));
Р.Добавить(С.%[1]s("нет"));
Р.Добавить(С.%[1]s());
Р.Добавить(С.ТеКсТ);
С.Вставить("", "пустой ключ");
Р.Добавить(С.%[1]s());
Возврат Р;
КонецФункции`, method))
			var result any
			require.NoError(t, interpreter.New().RunSandboxed(proc, nil, interpreter.RestrictedProfile(), &result))
			values := result.(*interpreter.Array).Iterate()
			require.Len(t, values, 10)
			for index, want := range map[int]int64{1: 7, 2: 0} {
				number, ok := values[index].(decimal.Decimal)
				require.True(t, ok, "value %d must remain a number, got %T", index, values[index])
				require.True(t, number.Equal(decimal.NewFromInt(want)))
			}
			require.Equal(t, "значение", values[0])
			require.Equal(t, []any{"", false, nil, nil, nil, "значение", "пустой ключ"}, values[3:])
		})
	}
}

func TestStructPropertyKeepsIndexReadUnsupported(t *testing.T) {
	proc := parseProc(t, `Функция Тест()
С = Новый Структура("Ключ", 7);
Возврат С["Ключ"];
КонецФункции`)
	err := interpreter.New().RunSandboxed(proc, nil, interpreter.RestrictedProfile(), nil)
	var dslErr *interpreter.DSLError
	require.ErrorAs(t, err, &dslErr)
	require.Contains(t, dslErr.Msg, "Тип «Структура» не поддерживает индексное чтение")
}

func TestStructPropertyRejectsExtraArguments(t *testing.T) {
	for _, method := range []string{"Свойство", "Property"} {
		for _, args := range []string{`"А", Выход`, `"А", Выход, 3`} {
			t.Run(method+"/"+args, func(t *testing.T) {
				setup := `С = Новый Структура("А", 1);
Выход = "не изменён";
`
				call := fmt.Sprintf("С.%s(%s);", method, args)
				proc := parseProc(t, "Функция Тест()\n"+setup+call+"\nКонецФункции")
				err := interpreter.New().RunSandboxed(proc, nil, interpreter.RestrictedProfile(), nil)
				var dslErr *interpreter.DSLError
				require.ErrorAs(t, err, &dslErr)
				require.Contains(t, dslErr.Msg, "Структура.Свойство(Ключ)")
				require.Contains(t, dslErr.Msg, "лишние аргументы")
				require.Contains(t, dslErr.Msg, "выходной параметр не поддерживается")

				caught := parseProc(t, "Функция Тест()\n"+setup+"Попытка\n"+call+`
Исключение
Р = Новый Массив;
Р.Добавить(ОписаниеОшибки());
Р.Добавить(Выход);
Р.Добавить(С.Свойство("А"));
Возврат Р;
КонецПопытки;
Возврат Неопределено;
КонецФункции`)
				var result any
				require.NoError(t, interpreter.New().RunSandboxed(caught, nil, interpreter.RestrictedProfile(), &result))
				values := result.(*interpreter.Array).Iterate()
				require.Equal(t, dslErr.Msg, values[0])
				require.Equal(t, "не изменён", values[1])
				require.Equal(t, decimal.NewFromInt(1), values[2])
			})
		}
	}
}
