package interpreter_test

import (
	"math"
	"strings"
	"testing"
	"time"

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
