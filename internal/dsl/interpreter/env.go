package interpreter

import (
	"strings"
	"time"
)

// This is implemented by runtime.Object; defined here to avoid import cycles.
type This interface {
	Get(name string) any
	Set(name string, v any)
}

// MethodCallable is implemented by objects that support obj.Method(args) calls.
type MethodCallable interface {
	CallMethod(method string, args []any) any
}

// MethodLister — необязательное дополнение к MethodCallable: объект называет
// свой тип и список методов, которые понимает.
//
// Нужен потому, что CallMethod не умеет сказать «такого метода нет»: он
// возвращает одно значение, и «не нашёл» неотличимо от «нашёл и вернул
// Неопределено». Из-за этого опечатка в имени метода у ~45 реализаций
// оставалась бесшумной — ровно тот дефект, что закрывали в #718 для Массива,
// Структуры и Соответствия правкой их собственных switch'ей.
//
// Реализовавшие интерфейс получают тот же честный отказ со списком доступных
// методов, что и встроенные коллекции, и остаются свободны от зависимости на
// этот пакет: список — это данные, а не вызов RaiseUserError.
type MethodLister interface {
	KnownMethods() (typeName string, methods []string)
}

// MapThis wraps map[string]any as a This (used for tablepart rows and register movement records).
type MapThis struct{ M map[string]any }

func (m *MapThis) Get(name string) any {
	low := strings.ToLower(name)
	for k, v := range m.M {
		if strings.ToLower(k) == low {
			return v
		}
	}
	return nil
}

func (m *MapThis) Set(name string, v any) {
	low := strings.ToLower(name)
	for k := range m.M {
		if strings.ToLower(k) == low {
			m.M[k] = v
			return
		}
	}
	m.M[low] = v
}

// execCtx — изменяемое состояние одного запуска DSL (Run/Call/RunWithResult/
// EvalExpr). Живёт в цепочке env конкретного вызова и разделяется всеми его
// кадрами, поэтому конкурентные запуски на одном *Interpreter не гонят по
// curFile/curLine и видят только свой debug hook (план 52).
type execCtx struct {
	curFile      string // last executed statement location (for error reporting)
	curLine      int
	evalDepth    int       // текущая глубина вложенных Вычислить/Eval
	debug        DebugHook // hook этого запуска; nil = без отладки, нулевые накладные
	deadline     time.Time // wall-clock запуска; zero = без лимита
	maxLoopIters int       // потолок итераций цикла; 0 = maxWhileIter
	moduleEnvs   map[string]*env
	// sandboxVars — неизменяемый overlay запретов одного sandbox-запуска.
	// Он отделён от пользовательских vars, поэтому присваивание, Перем,
	// module vars и временная публикация builtins не могут переоткрыть известное
	// capability-имя. Карта создаётся один раз в applySandboxVars и далее только
	// читается всеми кадрами, разделяющими execCtx.
	sandboxVars map[string]any
	// readOnlyReason включает fail-closed режим вычисления, в котором нельзя
	// вызывать переданные снаружи функции/фабрики и объектные методы, менять
	// объекты/коллекции/модульное состояние, а This-значения выдаются через
	// read-only membrane. Чистые вложенные DSL-функции разрешены.
	// Непустая строка одновременно служит объяснением пользователю.
	readOnlyReason    string
	readOnlyViolation string // sticky: fmt may recover a panic from String/Format
}

// loopLimit — действующий потолок итераций цикла для запуска.
func (ec *execCtx) loopLimit() int {
	if ec.maxLoopIters > 0 {
		return ec.maxLoopIters
	}
	return maxWhileIter
}

// checkDeadline жёстко останавливает запуск (dslStop, мимо Попытки), если
// исчерпан wall-clock. Дёшево, когда дедлайн не задан.
func (ec *execCtx) checkDeadline() {
	if !ec.deadline.IsZero() && time.Now().After(ec.deadline) {
		panic(dslStop{err: errSandboxTimeout})
	}
}

type env struct {
	vars       map[string]any
	parent     *env
	root       *env
	module     *env
	moduleVars map[string]bool
	this       This
	ec         *execCtx
	// sourceFile — identity текущего модуля. В отличие от ec.curFile она
	// лексически привязана к кадру процедуры и не меняется при вычислении
	// аргументов или динамического выражения Вычислить.
	sourceFile string
	// depth — глубина вызова процедур/функций (корень = 1). Растёт на каждый
	// callUserProc; используется стражем рекурсии (см. limits.go). O(1) и
	// потокобезопасно: счётчик живёт в цепочке env конкретного запуска.
	depth int
}

func newEnv(this This) *env {
	e := &env{vars: make(map[string]any), this: this, ec: &execCtx{}, depth: 1}
	e.root = e
	return e
}

func (e *env) frameWithModule(parent, module *env, depth int) *env {
	root := e.root
	if root == nil {
		root = e
	}
	return &env{
		vars:       make(map[string]any),
		parent:     parent,
		root:       root,
		module:     module,
		this:       e.this,
		ec:         e.ec,
		sourceFile: e.sourceFile,
		depth:      depth,
	}
}

func (e *env) rootEnv() *env {
	if e != nil && e.root != nil {
		return e.root
	}
	return e
}

func (e *env) get(name string) (any, bool) {
	low := strings.ToLower(name)
	if low == "this" || low == "этотобъект" {
		return protectReadOnly(e.ec, e.this), true
	}
	if e.ec != nil && e.ec.sandboxVars != nil {
		if v, ok := e.ec.sandboxVars[low]; ok {
			return protectReadOnly(e.ec, v), true
		}
	}
	name = low
	if v, ok := e.vars[name]; ok {
		return protectReadOnly(e.ec, v), true
	}
	if e.module != nil {
		if v, ok := e.module.vars[name]; ok {
			return protectReadOnly(e.ec, v), true
		}
	}
	if e.parent != nil {
		return e.parent.get(name)
	}
	return nil, false
}

func (e *env) set(name string, v any) {
	name = strings.ToLower(name)
	if e.module != nil && e.module.moduleVars[name] {
		if _, local := e.vars[name]; !local {
			refuseReadOnly(e.ec, "изменение модульной переменной «"+name+"»")
			e.module.vars[name] = v
			return
		}
	}
	e.vars[name] = v
}

func (e *env) setLocal(name string, v any) {
	name = strings.ToLower(name)
	e.vars[name] = v
}

func (e *env) declare(name string, v any) {
	name = strings.ToLower(name)
	e.vars[name] = v
}

func (e *env) declareModule(name string, v any) {
	name = strings.ToLower(name)
	if e.module != nil && e.module.moduleVars[name] {
		refuseReadOnly(e.ec, "изменение модульной переменной «"+name+"»")
		e.module.vars[name] = v
		return
	}
	e.vars[name] = v
}

// publishTemp временно записывает значения прямо в e.vars и возвращает
// функцию, восстанавливающую прежнее состояние этих ключей. Используется
// для служебных имён (ОписаниеОшибки), которые должны быть видны только
// внутри блока, но не должны протекать наружу как пользовательские
// переменные.
func publishTemp(e *env, vals map[string]any) func() {
	type prev struct {
		v       any
		existed bool
	}
	saved := make(map[string]prev, len(vals))
	for k, v := range vals {
		k = strings.ToLower(k)
		old, ok := e.vars[k]
		saved[k] = prev{old, ok}
		e.vars[k] = v
	}
	return func() {
		for k, p := range saved {
			if p.existed {
				e.vars[k] = p.v
			} else {
				delete(e.vars, k)
			}
		}
	}
}
