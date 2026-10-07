package interpreter_test

import (
	"strings"
	"testing"
	"time"

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
