package interpreter

import (
	"fmt"
	"strings"
)

// DSL-объект Утверждать (план 136) — встроенный набор ассертов для тестов
// уровня конфигурации. Единый модуль ассертов, чтобы каждый проект не
// переизобретал свой (как самодельные Проверить/РавныЛи). Инжектируется
// раннером тестов (`onebase test`) в прогон тест-обработки как переменная
// «Утверждать»:
//
//	Утверждать.Равно(НормализоватьТелефон("8 999 …"), "7999…", "8-формат → 7");
//	Утверждать.Истина(Условие, "описание");
//	Утверждать.Заполнено(Значение, "описание");
//
// Семантика — soft-assert: провал проверки НЕ прерывает тест-обработку, а
// помечает её проваленной и продолжает (чтобы одна обработка отчиталась сразу
// по нескольким проверкам). Каждый метод возвращает Булево (прошла ли
// проверка) — тест при желании может ветвиться по результату.

// AssertOutcome — результат одной проверки Утверждать.*.
type AssertOutcome struct {
	Passed bool
	Desc   string // описание проверки (последний строковый аргумент)
	Detail string // деталь расхождения для отчёта (пусто, если прошла)
}

// AssertRecorder принимает результаты проверок из объекта Утверждать.
// Реализуется раннером тестов.
type AssertRecorder interface {
	RecordAssert(o AssertOutcome)
}

// AccessChecker резолвит права роли для ассертов доступа. Инжектится раннером
// тестов (слой ui), чтобы ядро интерпретатора не зависело от пакетов auth/access.
// Вид/операция — пользовательские слова, реализация их нормализует. Ошибки
// (неизвестная роль/вид/объект) — чтобы ассерт провалился громко, а не молча.
type AccessChecker interface {
	// RoleAllows — матрица операций (план 112): разрешает ли роль op над (вид,объект).
	RoleAllows(roleName, kind, entity, op string) (allowed bool, err error)
	// FieldMask — полевой доступ (план 88): применяет маскирование поля при чтении.
	// hasPolicy=true, если поле маскируется/скрывается; masked — результат маски
	// на value (для точной проверки МаскаПоля).
	FieldMask(roleName, kind, entity, field string, value any) (masked any, hasPolicy bool, err error)
	// RowRestriction — строковый доступ (план 79): "denied" | "unrestricted" |
	// "restricted" для чтения/записи роли над объектом.
	RowRestriction(roleName, kind, entity, op string) (state string, err error)
}

// AssertRoot — корневой DSL-объект Утверждать.
type AssertRoot struct {
	rec    AssertRecorder
	access AccessChecker // nil вне `onebase test` — ассерты доступа тогда проваливаются с пояснением
}

// NewAssertRoot создаёт объект для инжекции как DSL-переменную «Утверждать».
func NewAssertRoot(rec AssertRecorder) *AssertRoot { return &AssertRoot{rec: rec} }

// SetAccessChecker включает ассерты доступа (РольМожет/ПолеМаскируется/
// СтрокиОграничены и т.п.), подставляя резолвер прав.
func (a *AssertRoot) SetAccessChecker(rc AccessChecker) { a.access = rc }

// This: у объекта нет доступных членов, только методы. Get/Set — безопасные no-op.
func (a *AssertRoot) Get(string) any  { return nil }
func (a *AssertRoot) Set(string, any) {}

func (a *AssertRoot) CallMethod(method string, args []any) any {
	switch strings.ToLower(method) {
	case "равно", "equal":
		return a.equalAssert(args, true)
	case "неравно", "notequal":
		return a.equalAssert(args, false)
	case "истина", "true":
		return a.boolAssert(args, true)
	case "ложь", "false":
		return a.boolAssert(args, false)
	case "заполнено", "filled":
		return a.filledAssert(args)
	case "провалить", "fail":
		return a.failAssert(args)
	case "рольможет", "rolecan":
		return a.roleAssert(args, true)
	case "рольнеможет", "rolecannot":
		return a.roleAssert(args, false)
	case "полемаскируется", "fieldmasked":
		return a.fieldMaskedAssert(args, true)
	case "полевидно", "fieldvisible":
		return a.fieldMaskedAssert(args, false)
	case "маскаполя", "fieldmask":
		return a.maskValueAssert(args)
	case "строкиограничены", "rowsrestricted":
		return a.rowsAssert(args, true)
	case "строкинеограничены", "rowsunrestricted":
		return a.rowsAssert(args, false)
	}
	panic(userError{Msg: "Утверждать: неизвестный метод «" + method +
		"» (доступны Равно, НеРавно, Истина, Ложь, Заполнено, Провалить, РольМожет, РольНеМожет, " +
		"ПолеМаскируется, ПолеВидно, МаскаПоля, СтрокиОграничены, СтрокиНеОграничены)"})
}

// Равно(Факт, Ожидание, Описание) / НеРавно(Факт, Ожидание, Описание).
func (a *AssertRoot) equalAssert(args []any, wantEqual bool) any {
	fact := argAt(args, 0)
	expect := argAt(args, 1)
	desc := descAt(args, 2)
	passed := equal(fact, expect) == wantEqual
	detail := ""
	if !passed {
		if wantEqual {
			detail = fmt.Sprintf("ожидалось «%s», получено «%s»", assertStr(expect), assertStr(fact))
		} else {
			detail = fmt.Sprintf("ожидалось не равно «%s», а получено именно оно", assertStr(expect))
		}
	}
	return a.record(passed, desc, detail)
}

// Истина(Условие, Описание) / Ложь(Условие, Описание).
func (a *AssertRoot) boolAssert(args []any, want bool) any {
	cond := truthy(argAt(args, 0))
	desc := descAt(args, 1)
	passed := cond == want
	detail := ""
	if !passed {
		detail = fmt.Sprintf("ожидалось %v, получено %v", want, cond)
	}
	return a.record(passed, desc, detail)
}

// Заполнено(Значение, Описание) — значение не пустое (та же семантика, что и
// ЗначениеЗаполнено).
func (a *AssertRoot) filledAssert(args []any) any {
	passed := !isBlankVal(argAt(args, 0))
	desc := descAt(args, 1)
	detail := ""
	if !passed {
		detail = "значение не заполнено"
	}
	return a.record(passed, desc, detail)
}

// Провалить(Описание) — безусловный провал (для недостижимых веток).
func (a *AssertRoot) failAssert(args []any) any {
	return a.record(false, descAt(args, 0), "явный Провалить")
}

// РольМожет(Роль, Вид, Объект, Операция, Описание) /
// РольНеМожет(...) — проверка матрицы прав роли поверх настоящего движка
// (auth.PermissionHas). Вид: справочник|документ|регистр|регистрсведений|отчёт|
// обработка; Операция: read/write/post/unpost/delete/run и русские синонимы
// (провести, изменять, …). Источник ролей — roles/*.yaml проекта.
func (a *AssertRoot) roleAssert(args []any, want bool) any {
	role := assertStr(argAt(args, 0))
	kind := assertStr(argAt(args, 1))
	entity := assertStr(argAt(args, 2))
	op := assertStr(argAt(args, 3))
	desc := descAt(args, 4)
	if a.access == nil {
		return a.record(false, desc, "проверка ролей доступна только в onebase test")
	}
	allowed, err := a.access.RoleAllows(role, kind, entity, op)
	if err != nil {
		return a.record(false, desc, err.Error())
	}
	passed := allowed == want
	detail := ""
	if !passed {
		verb := "должна разрешать"
		if !want {
			verb = "не должна разрешать"
		}
		detail = fmt.Sprintf("роль «%s» %s %s «%s» операцию «%s»", role, verb, kind, entity, op)
	}
	return a.record(passed, desc, detail)
}

// ПолеМаскируется(Роль, Вид, Объект, Поле, Описание) / ПолеВидно(...) — маскируется
// ли поле при чтении ролью (полевой доступ, план 88, поверх access.FieldDecisions).
func (a *AssertRoot) fieldMaskedAssert(args []any, wantMasked bool) any {
	role := assertStr(argAt(args, 0))
	kind := assertStr(argAt(args, 1))
	entity := assertStr(argAt(args, 2))
	field := assertStr(argAt(args, 3))
	desc := descAt(args, 4)
	if a.access == nil {
		return a.record(false, desc, "проверка доступа доступна только в onebase test")
	}
	_, hasPolicy, err := a.access.FieldMask(role, kind, entity, field, nil)
	if err != nil {
		return a.record(false, desc, err.Error())
	}
	passed := hasPolicy == wantMasked
	detail := ""
	if !passed {
		if wantMasked {
			detail = fmt.Sprintf("поле «%s.%s» для роли «%s» видно полностью, ожидалась маска", entity, field, role)
		} else {
			detail = fmt.Sprintf("поле «%s.%s» для роли «%s» маскируется, ожидалось полное значение", entity, field, role)
		}
	}
	return a.record(passed, desc, detail)
}

// МаскаПоля(Роль, Вид, Объект, Поле, Значение, Ожидание, Описание) — точный
// результат маскирования значения (проверяет сам алгоритм маски).
func (a *AssertRoot) maskValueAssert(args []any) any {
	role := assertStr(argAt(args, 0))
	kind := assertStr(argAt(args, 1))
	entity := assertStr(argAt(args, 2))
	field := assertStr(argAt(args, 3))
	value := argAt(args, 4)
	expect := argAt(args, 5)
	desc := descAt(args, 6)
	if a.access == nil {
		return a.record(false, desc, "проверка доступа доступна только в onebase test")
	}
	masked, _, err := a.access.FieldMask(role, kind, entity, field, value)
	if err != nil {
		return a.record(false, desc, err.Error())
	}
	passed := equal(masked, expect)
	detail := ""
	if !passed {
		detail = fmt.Sprintf("ожидалась маска «%s», получено «%s»", assertStr(expect), assertStr(masked))
	}
	return a.record(passed, desc, detail)
}

// СтрокиОграничены(Роль, Вид, Объект, Операция, Описание) / СтрокиНеОграничены(...)
// — применяется ли строковый фильтр RLS (план 79, поверх access.DecideWithLookup).
func (a *AssertRoot) rowsAssert(args []any, wantRestricted bool) any {
	role := assertStr(argAt(args, 0))
	kind := assertStr(argAt(args, 1))
	entity := assertStr(argAt(args, 2))
	op := assertStr(argAt(args, 3))
	desc := descAt(args, 4)
	if a.access == nil {
		return a.record(false, desc, "проверка доступа доступна только в onebase test")
	}
	state, err := a.access.RowRestriction(role, kind, entity, op)
	if err != nil {
		return a.record(false, desc, err.Error())
	}
	if state == "denied" {
		return a.record(false, desc, fmt.Sprintf("роль «%s» не имеет операции «%s» на «%s» — сначала РольМожет", role, op, entity))
	}
	want := "unrestricted"
	if wantRestricted {
		want = "restricted"
	}
	passed := state == want
	detail := ""
	if !passed {
		if wantRestricted {
			detail = fmt.Sprintf("роль «%s» видит все строки «%s», ожидался строковый фильтр", role, entity)
		} else {
			detail = fmt.Sprintf("строки «%s» для роли «%s» ограничены фильтром, ожидались все", entity, role)
		}
	}
	return a.record(passed, desc, detail)
}

func (a *AssertRoot) record(passed bool, desc, detail string) any {
	if a.rec != nil {
		a.rec.RecordAssert(AssertOutcome{Passed: passed, Desc: desc, Detail: detail})
	}
	return passed
}

func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

func descAt(args []any, i int) string {
	if i < len(args) {
		return assertStr(args[i])
	}
	return ""
}

func assertStr(v any) string {
	if s, err := builtinToString([]any{v}, "", 0); err == nil {
		if str, ok := s.(string); ok {
			return str
		}
	}
	return fmt.Sprintf("%v", v)
}
