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

func TestUndefinedPublicResultsAndDefaults(t *testing.T) {
	for _, strict := range []bool{false, true} {
		in := interpreter.New()
		in.StrictLexicalScope = strict
		for _, body := range []string{`Возврат Неопределено;`, `Перем Х; Возврат Х;`, `Возврат НеОбъявлено;`, `Возврат;`, ``} {
			proc := parseProcFile(t, "undefined.os", "Функция Т()\n"+body+"\nКонецФункции")
			var result any
			require.NoError(t, in.RunWithResult(proc, nil, &result))
			require.Nil(t, result, "RunWithResult: strict=%v, %s", strict, body)
			result, err := in.Call(proc, nil, nil)
			require.NoError(t, err)
			require.Nil(t, result, "Call: strict=%v, %s", strict, body)
			result, err = in.CallSandboxed(proc, nil, nil, interpreter.SandboxProfile{})
			require.NoError(t, err)
			require.Nil(t, result, "CallSandboxed: strict=%v, %s", strict, body)
			require.NoError(t, in.RunSandboxed(proc, nil, interpreter.SandboxProfile{}, &result))
			require.Nil(t, result, "RunSandboxed: strict=%v, %s", strict, body)
		}
		for _, expression := range []string{`Неопределено`, `Новый Соответствие.Получить("нет")`, `[0][9]`} {
			expr, err := parser.New(lexer.New(expression, "undefined.os")).ParseStandaloneExpr()
			require.NoError(t, err)
			require.Nil(t, in.EvalExpr(expr, nil), expression)
		}

		prog, err := parser.New(lexer.New(`Перем Модульная;
		Функция Т()
			Возврат Выбрать() = 7 И Выбрать(Неопределено) = Неопределено
				И Выбрать(Неопределено) <> 7 И Модульная = Неопределено
				И БезРезультата() = Неопределено;
		КонецФункции
		Функция Выбрать(Х = 7)
			Возврат Х;
		КонецФункции
		Процедура БезРезультата()
		КонецПроцедуры`, "defaults.os")).ParseProgram()
		require.NoError(t, err)
		in.LookupProc = func(name string) *ast.ProcedureDecl {
			for _, proc := range prog.Procedures {
				if strings.EqualFold(proc.Name.Literal, name) {
					return proc
				}
			}
			return nil
		}
		var result any
		require.NoError(t, in.RunWithResult(prog.Procedures[0], nil, &result))
		require.Equal(t, true, result, "strict=%v", strict)
	}
}

type undefinedHost struct {
	t      *testing.T
	writes int
	calls  int
}

func (h *undefinedHost) Get(string) any { return nil }
func (h *undefinedHost) Set(_ string, value any) {
	h.writes++
	require.Nil(h.t, value, "host setter must receive Go nil")
}
func (h *undefinedHost) GetDynamicField(string) (any, bool) { return nil, true }
func (h *undefinedHost) SetDynamicField(name string, value any) bool {
	h.Set(name, value)
	return true
}
func (h *undefinedHost) CallMethod(_ string, args []any) any {
	h.calls++
	require.Equal(h.t, []any{nil}, args, "host method must receive Go nil")
	return nil
}

func TestUndefinedHostBoundariesAndSerialization(t *testing.T) {
	host := &undefinedHost{t: t}
	callbackCalls, factoryCalls := 0, 0
	vars := map[string]any{
		"Приёмник": host,
		"ПустаяФункция": interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
			callbackCalls++
			require.Equal(t, []any{nil, nil}, args)
			return nil, nil
		}),
		"__factory_пустаяфабрика": func(args []any) any {
			factoryCalls++
			require.Equal(t, []any{nil}, args)
			return nil
		},
	}
	const src = `Функция Т()
		Х = Приёмник.Поле;
		Приёмник.Поле = Х;
		Приёмник["Поле"] = Х;
		Если Приёмник.Метод(Х) <> Неопределено Или ПустаяФункция(Х,) <> Неопределено Тогда
			Возврат "метод";
		КонецЕсли;
		Если Новый ПустаяФабрика(Х) <> Неопределено Или ПустаяФабрика(Х) <> Неопределено Тогда
			Возврат "фабрика";
		КонецЕсли;
		М = [Х];
		М[0] = Неопределено;
		С = Новый Структура("значение", Х);
		С.значение = Неопределено;
		К = Новый Соответствие;
		К["значение"] = Х;
		Для Каждого Элемент Из М Цикл
			Если Элемент <> Неопределено Тогда Возврат "итерация"; КонецЕсли;
		КонецЦикла;
		Если XMLТипЗнч(Х) <> "undefined" Или XMLСтрока(Х) <> "" Тогда Возврат "XML"; КонецЕсли;
		Если XMLЗначение("undefined", "") <> Неопределено Тогда Возврат "XML round-trip"; КонецЕсли;
		Если ПрочитатьJSON("null") <> Неопределено Тогда Возврат "JSON round-trip"; КонецЕсли;
		Если ТипЗнч(Х) <> "Неопределено" Или ЗначениеЗаполнено(Х) Тогда Возврат "тип"; КонецЕсли;
		Возврат ЗаписатьJSON([М, С, К]);
	КонецФункции`
	result := evalWithVars(t, src, vars)
	require.JSONEq(t, `[[null],{"значение":null},{"значение":null}]`, result.(string))
	require.Equal(t, 2, host.writes)
	require.Equal(t, 1, host.calls)
	require.Equal(t, 1, callbackCalls)
	require.Equal(t, 2, factoryCalls)

	// Returning a collection must preserve its host nil without mutating/copying
	// collection identity. This also covers the direct public serialization path.
	array := evalWithVars(t, `Функция Т()
		М = [Неопределено];
		Возврат М;
	КонецФункции`, nil).(*interpreter.Array)
	require.Nil(t, array.Index(0))
	encoded, err := interpreter.MarshalDSLValue(array)
	require.NoError(t, err)
	require.JSONEq(t, `[null]`, string(encoded))
}

type undefinedDebugHook struct {
	t     *testing.T
	seen  bool
	steps int
}

func (h *undefinedDebugHook) HookCheckBreakpoint(_ string, _ int, condition func(string) (bool, error)) bool {
	ok, err := condition(`ТипЗнч(Пусто) = "Неопределено" И Пусто <> 0 И НЕ Пусто`)
	require.NoError(h.t, err)
	require.True(h.t, ok)
	return true
}
func (h *undefinedDebugHook) HookShouldStep(string, int) bool { return false }
func (h *undefinedDebugHook) HookPushFrame(string, int)       {}
func (h *undefinedDebugHook) HookPopFrame()                   {}
func (h *undefinedDebugHook) HookOnPause(_ string, _ int, vars map[string]any, eval func(string) (any, error), _ string) {
	h.steps++
	for _, name := range []string{"пусто", "локальная"} {
		if value, ok := vars[name]; ok {
			require.Nil(h.t, value, "debugger variable %s", name)
			if name == "локальная" {
				h.seen = true
			}
		}
	}
	value, err := eval("Неопределено")
	require.NoError(h.t, err)
	require.Nil(h.t, value)
}

func TestUndefinedDebuggerBoundary(t *testing.T) {
	hook := &undefinedDebugHook{t: t}
	in := interpreter.New()
	in.DebugSource = func() interpreter.DebugHook { return hook }
	proc := parseProcFile(t, "debug-undefined.os", `Процедура Т()
		Перем Локальная;
		Локальная = Неопределено;
		Возврат;
	КонецПроцедуры`)
	require.NoError(t, in.Run(proc, nil, map[string]any{"Пусто": nil}))
	require.True(t, hook.seen)
	require.Positive(t, hook.steps)
}
