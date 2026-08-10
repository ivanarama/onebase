package interpreter

import (
	"fmt"
	"math"
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
// часы. В обычном запуске предел относится к одному вызову. В песочнице
// дополнительно действует единый wall-clock всего запуска: пауза использует
// только его остаток.
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
	if !isNumeric(args[0]) {
		RaiseUserError("Приостановить: ожидается число, получено " + getTypeName(args[0]))
	}
	secs, ok := toFloat(args[0])
	if !ok {
		RaiseUserError("Приостановить: длительность не удалось преобразовать в число")
	}
	switch {
	case math.IsNaN(secs):
		RaiseUserError("Приостановить: длительность NaN недопустима")
	case math.IsInf(secs, 0):
		RaiseUserError("Приостановить: длительность должна быть конечным числом")
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
		waitForSleep(sleepDuration(args), nil)
		return nil, nil
	}
}

// waitForSleep ждёт через timer, чтобы sandbox мог отменить ожидание общим
// дедлайном. Прямой time.Sleep здесь снова сделал бы блокирующий builtin дырой
// в MaxWallClock.
func waitForSleep(d time.Duration, ec *execCtx) {
	if ec != nil {
		ec.checkDeadline()
	}
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	if ec == nil || ec.deadlineDone == nil {
		<-timer.C
		return
	}
	select {
	case <-timer.C:
		// Если таймер паузы и дедлайн готовы одновременно, дедлайн имеет
		// приоритет: запуск не должен успешно закончиться за границей бюджета.
		ec.checkDeadline()
	case <-ec.deadlineDone:
		panic(dslStop{err: errSandboxTimeout})
	}
}

// newSandboxSleepFunctions возвращает паузу, связанную с execCtx конкретного
// запуска. Все вызовы видят один deadlineDone, поэтому каждый следующий вызов
// расходует остаток общего MaxWallClock, а не получает новый полный лимит.
func newSandboxSleepFunctions(ec *execCtx) map[string]any {
	fn := BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		waitForSleep(sleepDuration(args), ec)
		return nil, nil
	})
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
			waitForSleep(d, nil)
			return nil, nil
		}
		moved := clock.frozen.Add(d)
		clock.frozen = &moved
		return nil, nil
	}
}
