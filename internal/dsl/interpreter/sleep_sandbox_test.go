package interpreter_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Пауза в песочнице ограничена её wall-clock. Лимиты профиля (дедлайн,
// итерации) проверяются МЕЖДУ операторами, а спящий оператор до проверки не
// доходит — без понижения предела `Приостановить(300)` держал бы сессию пять
// минут в профиле, которому отведено десять секунд. Это ровно тот класс дыры,
// который закрывают запреты сети/файлов/exec рядом.
func TestSleep_SandboxCapsPauseToWallClock(t *testing.T) {
	src := `Функция Тест()
  Попытка
    Приостановить(60);
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
  Возврат "без ошибки";
КонецФункции`
	p := interpreter.SandboxProfile{MaxWallClock: 200 * time.Millisecond}
	var result any
	start := time.Now()
	require.NoError(t, interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result))
	assert.Less(t, time.Since(start), 5*time.Second,
		"песочница проспала бы минуту, хотя ей отведено 0,2 с")
	msg, _ := result.(string)
	assert.Contains(t, msg, "больше предела",
		"пауза сверх лимита профиля должна отклоняться, а не выполняться")
}

// Короткая пауза в песочнице остаётся разрешённой: ждать безопасно, запрещать
// нечего — ограничивается только длительность.
func TestSleep_SandboxAllowsPauseWithinWallClock(t *testing.T) {
	src := `Процедура Тест()
  Приостановить(0.01);
КонецПроцедуры`
	var result any
	require.NoError(t, interpreter.New().RunSandboxed(
		parseProc(t, src), nil, interpreter.RestrictedProfile(), &result))
}

// Профиль без лимита времени работает по общему потолку в 300 с — понижение
// касается только профилей с wall-clock.
func TestSleep_ProfileWithoutWallClockKeepsDefaultLimit(t *testing.T) {
	src := `Функция Тест()
  Попытка
    Приостановить(600);
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
  Возврат "без ошибки";
КонецФункции`
	var result any
	require.NoError(t, interpreter.New().RunSandboxed(
		parseProc(t, src), nil, interpreter.SandboxProfile{}, &result))
	msg, _ := result.(string)
	assert.Contains(t, msg, "300")
}

// Часы после паузы сдвигаются, но по сдвинутым часам не отличить «код выждал
// 30 с» от «код выставил дату на 30 с вперёд». Рекордер Мок.Паузы даёт
// утверждать саму выдержку и её длительность — без него тест на backoff
// проверяет не то, что хотел.
func TestSleep_TestProfileRecordsPauses(t *testing.T) {
	profile := interpreter.NewTestProfile()
	profile.Reset()
	src := `Функция Тест()
  Часы.Установить(Дата(2026, 8, 10, 12, 0, 0));
  Приостановить(30);
  Приостановить(60);
  Возврат Строка(Мок.Паузы.Количество()) + "|" + Строка(Мок.Паузы[0].Секунды) + "|" + Строка(Мок.Паузы[1].Секунды);
КонецФункции`
	var result any
	start := time.Now()
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &result, profile.Vars()))
	assert.Less(t, time.Since(start), time.Second, "тест-профиль не должен спать по-настоящему")
	assert.Equal(t, []string{"2", "30", "60"}, strings.Split(result.(string), "|"))
}

// Reset чистит записи пауз вместе с остальными рекордерами — иначе выдержки
// предыдущего теста утекали бы в следующий и ассерты «сколько раз ждали»
// врали бы через один.
func TestSleep_TestProfileResetClearsPauses(t *testing.T) {
	profile := interpreter.NewTestProfile()
	profile.Reset()
	// Часы замораживаем, чтобы пауза не тратила реального времени.
	src := `Функция Тест()
  Часы.Установить(Дата(2026, 8, 10, 12, 0, 0));
  Приостановить(1);
  Возврат Строка(Мок.Паузы.Количество());
КонецФункции`
	var first, second any
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &first, profile.Vars()))
	profile.Reset()
	require.NoError(t, interpreter.New().RunWithResult(parseProc(t, src), nil, &second, profile.Vars()))
	assert.Equal(t, "1", first)
	assert.Equal(t, "1", second, "записи паузы протекли между тестами")
}
