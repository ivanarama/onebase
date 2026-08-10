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
func sleepDuration(args []any) time.Duration {
	if len(args) == 0 {
		RaiseUserError("Приостановить: нужно указать длительность в секундах")
	}
	secs := floatArg(args, 0)
	switch {
	case secs != secs: // NaN
		RaiseUserError("Приостановить: длительность не число")
	case secs < 0:
		RaiseUserError(fmt.Sprintf("Приостановить: длительность %g отрицательная", secs))
	case secs > maxSleepSeconds:
		RaiseUserError(fmt.Sprintf("Приостановить: %g с больше предела в %d с; "+
			"длинную выдержку делают регламентным заданием, а не паузой в коде", secs, maxSleepSeconds))
	}
	return time.Duration(secs * float64(time.Second))
}

func newSleepBuiltin() BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		if d := sleepDuration(args); d > 0 {
			time.Sleep(d)
		}
		return nil, nil
	}
}

// newFrozenClockSleepBuiltin — выдержка под замороженными часами (тест-профиль):
// вместо ожидания двигает время вперёд. Так backoff проверяется headless и
// мгновенно — тест утверждает, что между попытками прошло сколько надо, не
// тратя на это реальных секунд. Незамороженные часы означают обычный прогон,
// и там выдержка настоящая.
func newFrozenClockSleepBuiltin(clock *TestClock) BuiltinFunc {
	return func(args []any, _ string, _ int) (any, error) {
		d := sleepDuration(args)
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
