package interpreter_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/require"
)

func TestCallEntrySandboxedWithBindingsReturnsOutputAfterEarlyReturn(t *testing.T) {
	proc := parseProc(t, `
Процедура ПередЗакрытием(Отказ)
	Отказ = Истина;
	Возврат;
КонецПроцедуры`)
	in := interpreter.New()
	in.StrictLexicalScope = true

	result, err := in.CallEntrySandboxedWithBindings(proc, nil, []any{false}, interpreter.SandboxProfile{})
	require.NoError(t, err)
	require.Equal(t, true, result.Bindings["Отказ"])
}

func TestCallEntrySandboxedWithBindingsPreservesErrorAndTimeout(t *testing.T) {
	t.Run("exception", func(t *testing.T) {
		proc := parseProc(t, `
Процедура ПередЗакрытием(Отказ)
	Отказ = Истина;
	ВызватьИсключение("не закрывать");
КонецПроцедуры`)
		_, err := interpreter.New().CallEntrySandboxedWithBindings(proc, nil, []any{false}, interpreter.SandboxProfile{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "не закрывать")
	})

	t.Run("timeout", func(t *testing.T) {
		proc := parseProc(t, `
Процедура ПередЗакрытием(Отказ)
	Пока Истина Цикл
	КонецЦикла;
КонецПроцедуры`)
		_, err := interpreter.New().CallEntrySandboxedWithBindings(proc, nil, []any{false}, interpreter.SandboxProfile{MaxWallClock: 5 * time.Millisecond})
		require.Error(t, err)
		require.True(t, strings.Contains(strings.ToLower(err.Error()), "время") || strings.Contains(strings.ToLower(err.Error()), "timeout"), err.Error())
	})
}

func TestOrdinaryCallsKeepNestedParametersLocal(t *testing.T) {
	entry := parseProc(t, `Функция Старт(Значение = "caller")
	Вложенная(Значение);
	Возврат Значение + ":" + Вложенная();
КонецФункции`)
	helper := parseProc(t, `Функция Вложенная(Значение = "default")
	Значение = "changed:" + Значение;
	Возврат Значение;
КонецФункции`)
	for _, strict := range []bool{false, true} {
		name := "legacy"
		if strict {
			name = "strict"
		}
		t.Run(name, func(t *testing.T) {
			in := interpreter.New()
			in.StrictLexicalScope = strict
			in.LookupProc = func(name string) *ast.ProcedureDecl {
				if strings.EqualFold(name, "Вложенная") {
					return helper
				}
				return nil
			}
			got, err := in.Call(entry, nil, []any{"caller"})
			require.NoError(t, err)
			require.Equal(t, "caller:changed:default", got)
			var result any
			err = in.RunWithResult(entry, nil, &result, map[string]any{"Значение": "caller"})
			require.NoError(t, err)
			require.Equal(t, "caller:changed:default", result)
		})
	}
}

func TestEntryBindingsSurviveNestedCallAndNormalOrEarlyReturn(t *testing.T) {
	helper := parseProc(t, `Процедура Вложенная(Отказ)
	Отказ = Истина;
КонецПроцедуры`)
	for _, strict := range []bool{false, true} {
		for _, early := range []bool{false, true} {
			name := "legacy"
			if strict {
				name = "strict"
			}
			if early {
				name += "/early"
			} else {
				name += "/normal"
			}
			t.Run(name, func(t *testing.T) {
				ending := ""
				if early {
					ending = "Возврат;\nОтказ = Ложь;"
				}
				entry := parseProc(t, `Процедура ПередЗакрытием(Отказ, Текст = "default")
	Вложенная(Отказ);
	Если Отказ Тогда
		ВызватьИсключение("nested parameter leaked");
	КонецЕсли;
	Отказ = Истина;
	Текст = "changed:" + Текст;
`+ending+`
КонецПроцедуры`)
				in := interpreter.New()
				in.StrictLexicalScope = strict
				in.LookupProc = func(name string) *ast.ProcedureDecl {
					if strings.EqualFold(name, "Вложенная") {
						return helper
					}
					return nil
				}
				got, err := in.CallEntrySandboxedWithBindings(entry, nil, []any{false}, interpreter.SandboxProfile{})
				require.NoError(t, err)
				require.Equal(t, map[string]any{"Отказ": true, "Текст": "changed:default"}, got.Bindings)
			})
		}
	}
}
