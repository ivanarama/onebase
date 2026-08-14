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
// Граница здесь та же, что у SandboxProfile: закрываются ИЗВЕСТНЫЕ глобальные
// имена, а не строится object-capability membrane. Значение, уже лежащее в
// переменной остановленного кадра (например, полученный ранее объект
// документа), условие по-прежнему видит — это его окружение, ради которого
// условие и пишут.

// conditionDeadline — предел на одно вычисление условия. Секунды здесь много:
// условие проверяется на каждом проходе строки, и «медленное условие»
// превращает отладку в зависание. Останов по дедлайну виден как ошибка
// условия — точка останавливается и показывает причину.
const conditionDeadline = 2 * time.Second

// conditionLoopIters — потолок итераций цикла внутри условия.
const conditionLoopIters = 100_000

// mutatingGlobals — имена, через которые условие может ИЗМЕНИТЬ данные:
// менеджеры объектов (Создать/Записать/Удалить/Провести), регистры и коллектор
// движений. Оба языка имён, потому что оба инжектируются в vars.
// Имена перечислены так, как их пишет человек: в ошибке показывается
// написание из списка, а не нормализованный ключ overlay.
var mutatingGlobals = []string{
	"Справочники", "Catalogs",
	"Документы", "Documents",
	"РегистрыНакопления", "AccumulationRegisters",
	"РегистрыСведений", "InformationRegisters",
	"Движения", "Movements",
	"БлокировкаДанных", "DataLock",
	"ЗаписатьСобытиеАудита", "WriteAuditDecision",
	"СохранитьКартинку", "PutImage",
	"ВебСокет", "WebSocket",
}

// deniedInCondition — значение-заглушка на месте запрещённого имени. Любое
// обращение к нему — чтение свойства или вызов метода — поднимает ошибку с
// именем, чтобы человек увидел, ЧТО именно запрещено, а не «неизвестная
// переменная».
type deniedInCondition struct{ name string }

func (d deniedInCondition) refuse() {
	RaiseUserError("«" + d.name + "» недоступно в условии точки останова: " +
		"условие вычисляется на каждом проходе строки и не должно менять данные")
}

func (d deniedInCondition) Get(string) any { d.refuse(); return nil }

func (d deniedInCondition) Set(string, any) { d.refuse() }

func (d deniedInCondition) CallMethod(string, []any) any { d.refuse(); return nil }

// conditionOverlay строит immutable overlay запретов для условия: запреты
// профиля (сеть, файлы, запуск процессов) плюс изменяющие данные глобалы.
func conditionOverlay() map[string]any {
	p := SandboxProfile{DenyNet: true, DenyFile: true, DenyExec: true}
	overlay := make(map[string]any, len(mutatingGlobals)+16)
	for k, v := range p.Vars() {
		overlay[strings.ToLower(k)] = v
	}
	for _, name := range mutatingGlobals {
		overlay[strings.ToLower(name)] = deniedInCondition{name: name}
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
	savedVars := ec.sandboxVars

	// Дедлайн условия не должен ПРОДЛЕВАТЬ уже действующий: у отлаживаемого
	// запуска может быть свой предел, и условие не повод его отодвинуть.
	deadline := time.Now().Add(conditionDeadline)
	if !savedDeadline.IsZero() && savedDeadline.Before(deadline) {
		deadline = savedDeadline
	}
	ec.deadline = deadline
	ec.maxLoopIters = conditionLoopIters

	merged := make(map[string]any, len(savedVars)+len(mutatingGlobals)+16)
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
		ec.sandboxVars = savedVars
	}
}
