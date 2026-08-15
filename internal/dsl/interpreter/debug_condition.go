package interpreter

import (
	"strings"
	"time"
)

// Условие точки останова вычисляется В ЖИВОМ КОНТЕКСТЕ и на КАЖДОМ проходе
// строки — без участия человека. Поэтому оно единственное из отладочных
// выражений, которому нельзя давать полный доступ (#883).
//
// Табло и консоль отладчика остаются полноценными намеренно: их человек
// набирает сам, по одному разу, глядя на результат. Условие — наоборот:
// написано один раз, исполняется тысячи раз молча. `Записать(Объект)` в нём
// менял бы данные при каждой проверке, а вызов функции модуля с побочными
// эффектами делал бы отладчик частью поведения программы: убрал точку —
// программа работает иначе.
//
// В отличие от обычного SandboxProfile, одних известных глобальных имён здесь
// недостаточно: уже созданный writer может лежать в локальной переменной, а
// произвольная функция модуля — спрятать запись внутри себя. Поэтому режим
// навязывается центральными границами интерпретатора: объектные методы,
// внедрённые функции/фабрики, запись в объекты/коллекции и модульные переменные
// fail-closed, а This-объекты выдаются через read-only membrane. Чистые DSL-
// helpers и поля локальных объектов остаются доступны — это совместимый язык
// условий, ради которого условные точки и существуют.

// conditionDeadline — предел на одно вычисление условия. Секунды здесь много:
// условие проверяется на каждом проходе строки, и «медленное условие»
// превращает отладку в зависание. Останов по дедлайну виден как ошибка
// условия — точка останавливается и показывает причину.
const conditionDeadline = 2 * time.Second

// conditionLoopIters — потолок итераций цикла внутри условия.
const conditionLoopIters = 100_000

// conditionOverlay строит immutable overlay внешних возможностей. Запись в
// данные закрывается не списком имён, а центральной read-only границей
// интерпретатора (см. readonly.go и evalCall).
func conditionOverlay() map[string]any {
	p := SandboxProfile{DenyNet: true, DenyFile: true, DenyExec: true}
	overlay := make(map[string]any, 32)
	for k, v := range p.Vars() {
		overlay[strings.ToLower(k)] = v
	}
	return overlay
}

// withConditionLimits применяет ограничения условия к контексту запуска и
// возвращает функцию восстановления.
//
// Сохранение и восстановление, а не отдельный execCtx: условие вычисляется
// внутри живого запуска, синхронно, в том же кадре — ровно так же, как
// evalDebugExpr уже сохраняет и возвращает позицию и debug-hook. Отдельный
// контекст отрезал бы условие от окружения остановленного оператора, ради
// которого оно и пишется.
func withConditionLimits(e *env) func() {
	ec := e.ec
	savedDeadline := ec.deadline
	savedIters := ec.maxLoopIters
	savedDecimalExpansion := ec.maxDecimalExpansion
	savedStringExpansion := ec.maxStringExpansion
	savedVars := ec.sandboxVars
	savedReadOnly := ec.readOnlyReason
	savedViolation := ec.readOnlyViolation

	// Дедлайн условия не должен ПРОДЛЕВАТЬ уже действующий: у отлаживаемого
	// запуска может быть свой предел, и условие не повод его отодвинуть.
	deadline := time.Now().Add(conditionDeadline)
	if !savedDeadline.IsZero() && savedDeadline.Before(deadline) {
		deadline = savedDeadline
	}
	ec.deadline = deadline
	// Условие не должно ослаблять более строгий лимит внешней песочницы.
	ec.maxLoopIters = conditionLoopIters
	if savedIters > 0 && savedIters < conditionLoopIters {
		ec.maxLoopIters = savedIters
	}
	ec.maxDecimalExpansion = defaultSandboxDecimalExpansion
	if savedDecimalExpansion > 0 && savedDecimalExpansion < ec.maxDecimalExpansion {
		ec.maxDecimalExpansion = savedDecimalExpansion
	}
	ec.maxStringExpansion = defaultSandboxStringExpansion
	if savedStringExpansion > 0 && savedStringExpansion < ec.maxStringExpansion {
		ec.maxStringExpansion = savedStringExpansion
	}
	ec.readOnlyReason = "условие точки останова вычисляется на каждом проходе строки и не должно менять данные"
	ec.readOnlyViolation = ""

	merged := make(map[string]any, len(savedVars)+32)
	for k, v := range savedVars {
		merged[k] = v
	}
	for k, v := range conditionOverlay() {
		merged[k] = v
	}
	ec.sandboxVars = merged

	return func() {
		ec.deadline = savedDeadline
		ec.maxLoopIters = savedIters
		ec.maxDecimalExpansion = savedDecimalExpansion
		ec.maxStringExpansion = savedStringExpansion
		ec.sandboxVars = savedVars
		ec.readOnlyReason = savedReadOnly
		ec.readOnlyViolation = savedViolation
	}
}
