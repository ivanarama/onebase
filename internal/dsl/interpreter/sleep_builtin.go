package interpreter

import (
	"fmt"
	"time"
)

// Приостановить(Секунды) — блокирующая выдержка времени (issue #708).
//
// Без неё политика повторов оставалась декоративной: счётчик попыток растёт,
// «4xx без повтора» соблюдается, а интервал между попытками нулевой — три
// попытки уходят в перегруженный сервис мгновенно, ровно тогда, когда он
// просил подождать. Реальный backoff приходилось делать через
// ВыполнитьКоманду("powershell", "Start-Sleep …"): это требовало включить
// exec.enabled (то есть открыть запуск ЛЮБЫХ команд ОС ради паузы) и было
// платформозависимо. Запасной вариант — активное ожидание по РазностьДат —
// выжигает ядро на всё время паузы.
//
// Верхний предел обязателен: выдержка блокирует поток целиком, и опечатка в
// «секундах» (взяли миллисекунды) подвесила бы воркер регламентного задания на
// часы. Предел на ОДИН вызов, а не на суммарное ожидание: цикл повторов с
// нарастающей задержкой — законный сценарий, ради которого всё и делается.
const maxSleepSeconds = 300

// sleepDuration разбирает аргумент и проверяет границы. Общая для боевого
// билтина и тест-профиля, чтобы правила не разъехались.
//
// Отказ поднимается через RaiseUserError, как у остальных объектов DSL (Часы,
// коллекции): сообщение видит прикладной разработчик, и ловится оно Попыткой.
// limitSeconds — действующий предел одной паузы: по умолчанию maxSleepSeconds,
// а в песочнице — её собственный wall-clock (см. NewSleepFunctions).
func sleepDuration(args []any) time.Duration {
	return sleepDurationLimited(args, maxSleepSeconds)
}

func sleepDurationLimited(args []any, limitSeconds float64) time.Duration {
	if len(args) == 0 {
		RaiseUserError("Приостановить: нужно указать длительность в секундах")
	}
	secs := floatArg(args, 0)
	switch {
	case secs != secs: // NaN
		RaiseUserError("Приостановить: длительность не число")
	case secs < 0:
		RaiseUserError(fmt.Sprintf("Приостановить: длительность %g отрицательная", secs))
	case secs > limitSeconds:
		RaiseUserError(fmt.Sprintf("Приостановить: %g с больше предела в %g с; "+
			"длинную выдержку делают регламентным заданием, а не паузой в коде", secs, limitSeconds))
	}
	return time.Duration(secs * float64(time.Second))
}

func newSleepBuiltin() BuiltinFunc {
	return newSleepBuiltinLimited(maxSleepSeconds)
}

func newSleepBuiltinLimited(limitSeconds float64) BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if d := sleepDurationLimited(args, limitSeconds); d > 0 {
			time.Sleep(d)
		}
		return nil, nil
	}
}

// NewSleepFunctions — Приостановить с пониженным пределом одной паузы. Нужна
// песочнице: её лимиты (wall-clock, итерации) проверяются МЕЖДУ операторами, а
// спящий оператор до проверки не доходит. Без понижения предела пауза была бы
// единственным способом занять поток дольше отведённого профилю — код с
// `Приостановить(300)` держал бы сессию пять минут при лимите в десять секунд.
// Сама возможность не запрещается: ждать безопасно, ограничивать надо только
// длительность.
func NewSleepFunctions(limit time.Duration) map[string]any {
	limitSeconds := limit.Seconds()
	if limit <= 0 || limitSeconds > maxSleepSeconds {
		limitSeconds = maxSleepSeconds
	}
	fn := newSleepBuiltinLimited(limitSeconds)
	return map[string]any{
		"Приостановить": fn,
		"Пауза":         fn,
		"Sleep":         fn,
	}
}

// newFrozenClockSleepBuiltin — выдержка под замороженными часами (тест-профиль):
// вместо ожидания двигает время вперёд. Так backoff проверяется headless и
// мгновенно — тест утверждает, что между попытками прошло сколько надо, не
// тратя на это реальных секунд. Незамороженные часы означают обычный прогон,
// и там выдержка настоящая.
func newFrozenClockSleepBuiltin(clock *TestClock, pauses *Array) BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		d := sleepDuration(args)
		if pauses != nil {
			recordCall(pauses, map[string]any{"Секунды": d.Seconds()})
		}
		if clock == nil || clock.frozen == nil {
			if d > 0 {
				time.Sleep(d)
			}
			return nil, nil
		}
		moved := clock.frozen.Add(d)
		clock.frozen = &moved
		return nil, nil
	}
}
