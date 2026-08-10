package interpreter_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Две допустимые по отдельности паузы расходуют один MaxWallClock. Вторая
// прерывается общим дедлайном и не может превратить 600 мс песочницы в секунду.
func TestSleepSandbox_TwoPausesShareWallClock(t *testing.T) {
	src := `Функция Тест()
  Попытка
    Приостановить(0.5);
    Приостановить(0.5);
  Исключение
    Возврат "таймаут пойман";
  КонецПопытки;
  Возврат "завершено";
КонецФункции`
	var result any
	start := time.Now()
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil,
		interpreter.SandboxProfile{MaxWallClock: 600 * time.Millisecond}, &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "максимальное время")
	assert.NotEqual(t, "таймаут пойман", result, "жёсткий stop не должна ловить Попытка")
	assert.Less(t, time.Since(start), 800*time.Millisecond,
		"ожидание не было отменено дедлайном песочницы")
}

// Одноимённая non-callable переменная не должна сбрасывать deadline-aware
// dispatch при провале к глобальному builtin. Проверяем все публичные синонимы
// через RunSandboxed.
func TestSleepSandbox_LocalVariableCannotBypassDeadline(t *testing.T) {
	for _, name := range []string{"Приостановить", "Пауза", "Подождать", "Sleep", "Wait"} {
		t.Run(name, func(t *testing.T) {
			src := fmt.Sprintf(`Функция Тест()
  %s = 0;
  %s(0.5);
  Возврат "обход";
КонецФункции`, name, name)
			var result any
			start := time.Now()
			err := interpreter.New().RunSandboxed(parseProc(t, src), nil,
				interpreter.SandboxProfile{MaxWallClock: 100 * time.Millisecond}, &result)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "максимальное время")
			assert.NotEqual(t, "обход", result)
			assert.Less(t, time.Since(start), 300*time.Millisecond,
				"локальная переменная отключила отмену ожидания %s", name)
		})
	}
}

// Доверенная callable-инъекция и пользовательская процедура разрешаются раньше
// глобального builtin и не должны внезапно становиться зарезервированным Sleep.
// Ограничение относится к штатному блокирующему ожиданию, а не к самому имени.
func TestSleepSandbox_CallableShadowingIsPreserved(t *testing.T) {
	called := false
	custom := interpreter.BuiltinFunc(func([]any, string, int) (any, error) {
		called = true
		return nil, nil
	})
	src := `Функция Тест()
  Sleep(10);
  Возврат "готово";
КонецФункции`
	var result any
	start := time.Now()
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil,
		interpreter.SandboxProfile{MaxWallClock: 100 * time.Millisecond}, &result,
		map[string]any{"Sleep": custom})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "готово", result)
	assert.Less(t, time.Since(start), time.Second)
}

func TestSleepSandbox_UserProcedureShadowingIsPreserved(t *testing.T) {
	main := parseProc(t, `Функция Тест()
  Возврат Sleep(10);
КонецФункции`)
	helper := parseProc(t, `Функция Sleep(Секунды)
  Возврат "пользовательская процедура";
КонецФункции`)
	interp := interpreter.New()
	interp.LookupSiblingProc = func(_ string, name string) *ast.ProcedureDecl {
		if strings.EqualFold(name, "Sleep") {
			return helper
		}
		return nil
	}
	var result any
	start := time.Now()
	err := interp.RunSandboxed(main, nil,
		interpreter.SandboxProfile{MaxWallClock: 100 * time.Millisecond}, &result)
	require.NoError(t, err)
	assert.Equal(t, "пользовательская процедура", result)
	assert.Less(t, time.Since(start), time.Second)
}

// Все документированные имена должны работать и через обычный публичный
// dispatch. Нулевая выдержка проверяет регистрацию без реального ожидания.
func TestSleepDSL_AllAliasesAreRegistered(t *testing.T) {
	for _, name := range []string{"Приостановить", "Пауза", "Подождать", "Sleep", "Wait"} {
		t.Run(name, func(t *testing.T) {
			src := fmt.Sprintf(`Функция Тест()
  %s(0);
  Возврат "готово";
КонецФункции`, name)
			var result any
			require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &result))
			assert.Equal(t, "готово", result)
		})
	}
}

// Те же алиасы обязаны использовать frozen-clock builtin в onebase test, а не
// случайно проваливаться в настоящий time.Sleep.
func TestSleepDSL_AllAliasesUseFrozenClock(t *testing.T) {
	for _, name := range []string{"Приостановить", "Пауза", "Подождать", "Sleep", "Wait"} {
		t.Run(name, func(t *testing.T) {
			profile := interpreter.NewTestProfile()
			profile.Reset()
			src := fmt.Sprintf(`Функция Тест()
  Часы.Установить(Дата(2000, 8, 10, 12, 0, 0));
  %s(30);
  Возврат Строка(ТекущаяДатаВремя()) + "|" + Строка(Мок.Паузы[0].Секунды);
КонецФункции`, name)
			var result any
			start := time.Now()
			require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &result, profile.Vars()))
			assert.Less(t, time.Since(start), time.Second)
			assert.Equal(t, "10.08.2000 12:00:30|30", result)
		})
	}
}

// Вне sandbox пользовательская инъекция по-прежнему затеняет штатный Sleep:
// security-исключение не меняет глобальный порядок разрешения функций.
func TestSleepDSL_ShadowingOutsideSandboxIsUnchanged(t *testing.T) {
	called := false
	custom := interpreter.BuiltinFunc(func([]any, string, int) (any, error) {
		called = true
		return nil, nil
	})
	src := `Функция Тест()
  Sleep(10);
  Возврат "готово";
КонецФункции`
	var result any
	start := time.Now()
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &result,
		map[string]any{"Sleep": custom}))
	assert.True(t, called)
	assert.Equal(t, "готово", result)
	assert.Less(t, time.Since(start), time.Second, "пользовательский Sleep не затенил штатный")
}

// MaxFloat проходит через публичный DSL-вызов, а не напрямую в helper. Раньше
// преобразование в time.Duration переполнялось в отрицательное значение и
// превращало огромную паузу в мгновенный успешный вызов.
func TestSleepDSL_RejectsMaxFloatWithoutDurationOverflow(t *testing.T) {
	src := `Функция Тест()
  Попытка
    Приостановить(МаксЧисло);
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
  Возврат "принято";
КонецФункции`
	var result any
	err := interpreter.New().RunWithResult(parseProc(t, src), nil, &result,
		map[string]any{"МаксЧисло": math.MaxFloat64})
	require.NoError(t, err)
	msg, ok := result.(string)
	require.True(t, ok, "результат = %T (%v)", result, result)
	assert.Contains(t, msg, "больше предела")
}

// Числовая строка не является числом параметра: неявный ParseFloat скрывал
// ошибки данных и расходился с объявленной сигнатурой Приостановить(Число).
func TestSleepDSL_RejectsNumericString(t *testing.T) {
	src := `Функция Тест()
  Попытка
    Приостановить("0.01");
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
  Возврат "принято";
КонецФункции`
	var result any
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &result))
	assert.Contains(t, result.(string), "ожидается число")
}

// Под замороженными часами пауза остаётся мгновенной, двигает те же часы и
// записывается в Мок.Паузы. Reset очищает и часы, и запись между тестами.
func TestSleepDSL_FrozenClockAndResetStayConsistent(t *testing.T) {
	profile := interpreter.NewTestProfile()
	profile.Reset()
	src := `Функция Тест()
  Часы.Установить(Дата(2000, 8, 10, 12, 0, 0));
  Приостановить(30);
  Возврат Строка(ТекущаяДатаВремя()) + "|" + Строка(Мок.Паузы.Количество()) + "|" + Строка(Мок.Паузы[0].Секунды);
КонецФункции`
	var result any
	start := time.Now()
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &result, profile.Vars()))
	assert.Less(t, time.Since(start), time.Second)
	parts := strings.Split(result.(string), "|")
	require.Len(t, parts, 3)
	assert.Contains(t, parts[0], "12:00:30")
	assert.Equal(t, []string{"1", "30"}, parts[1:])

	profile.Reset()
	resetSrc := `Функция Тест()
  Возврат Строка(Мок.Паузы.Количество()) + "|" + Строка(Год(ТекущаяДатаВремя()));
КонецФункции`
	result = nil
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, resetSrc), nil, &result, profile.Vars()))
	resetParts := strings.Split(result.(string), "|")
	require.Len(t, resetParts, 2)
	assert.Equal(t, "0", resetParts[0])
	assert.NotEqual(t, "2000", resetParts[1], "замороженные часы протекли через Reset")
}
