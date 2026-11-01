package interpreter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// «Знач» в списке параметров — модификатор передачи по значению, как в 1С.
// Раньше он разбирался как ИМЯ параметра: «Ф(Знач Текст, Тег)» объявляла три
// параметра (Знач, Текст, Тег), аргументы сдвигались, последний молча
// становился Неопределено. Проверка — исполнением модуля, как в procrun.
func runModule(t *testing.T, src string) any {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "module.os")).ParseProgram()
	require.NoError(t, err)
	in := interpreter.New()
	// Вызовы процедур модуля по имени — как у загруженного модуля (LookupProc).
	in.LookupProc = func(name string) *ast.ProcedureDecl {
		for _, p := range prog.Procedures {
			if strings.EqualFold(p.Name.Literal, name) {
				return p
			}
		}
		return nil
	}
	entry := prog.Procedures[len(prog.Procedures)-1]
	var result any
	require.NoError(t, in.RunWithResult(entry, nil, &result, nil))
	return result
}

func TestParamByValModifier(t *testing.T) {
	got := runModule(t, `
Функция Ф(Знач Текст, Тег)
	Текст = Текст + "!";
	Возврат "[" + Текст + "][" + Тег + "]";
КонецФункции

Функция Значение(Val Х, Знач У = "по умолчанию")
	Возврат Х + "/" + У;
КонецФункции

Функция Тест()
	Исходный = "абв";
	Итог = Ф(Исходный, "тег") + " " + Исходный + " " + Значение("х");
	Возврат Итог;
КонецФункции
`)
	require.Equal(t, "[абв!][тег] абв х/по умолчанию", got)
}

// «Знач» остаётся допустимым ИМЕНЕМ параметра, когда за ним не следует имя:
// так в рабочей конфигурации объявлены ПустоеЗнач(Знач) и
// ЗаполнитьЕслиЕсть(Об, Имя, Знач).
func TestParamNamedZnachStillWorks(t *testing.T) {
	got := runModule(t, `
Функция ПустоеЗнач(Знач)
	Возврат Знач = Неопределено;
КонецФункции

Функция Склеить(Об, Имя, Знач)
	Возврат Об + Имя + Знач;
КонецФункции

Функция Тест()
	Возврат ?(ПустоеЗнач(Неопределено), "Да", "Нет") + "|" + Склеить("а", "б", "в");
КонецФункции
`)
	require.Equal(t, "Да|абв", got)
}

func TestParamListRequiresComma(t *testing.T) {
	_, err := parser.New(lexer.New(`
Функция Ф(Текст Тег)
	Возврат Текст;
КонецФункции
`, "module.os")).ParseProgram()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "ожидалась «,» или «)»"), err.Error())
}
