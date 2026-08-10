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
	debug        DebugHook       // hook этого запуска; nil = без отладки, нулевые накладные
	deadline     time.Time       // wall-clock запуска; zero = без лимита
	deadlineDone <-chan struct{} // закрывается при исчерпании общего wall-clock запуска
	maxLoopIters int             // потолок итераций цикла; 0 = maxWhileIter
	moduleEnvs   map[string]*env
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
	if ec.deadlineDone != nil {
		select {
		case <-ec.deadlineDone:
			panic(dslStop{err: errSandboxTimeout})
		default:
		}
	}
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
	return &env{vars: make(map[string]any), parent: parent, root: root, module: module, this: e.this, ec: e.ec, depth: depth}
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
		return e.this, true
	}
	name = low
	if v, ok := e.vars[name]; ok {
		return v, true
	}
	if e.module != nil {
		if v, ok := e.module.vars[name]; ok {
			return v, true
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
