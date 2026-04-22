package interpreter

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

func (m *MapThis) Get(name string) any { return m.M[name] }
func (m *MapThis) Set(name string, v any) { m.M[name] = v }

type env struct {
	vars   map[string]any
	parent *env
	this   This
}

func newEnv(this This) *env {
	return &env{vars: make(map[string]any), this: this}
}

func (e *env) child() *env {
	return &env{vars: make(map[string]any), parent: e, this: e.this}
}

func (e *env) get(name string) (any, bool) {
	if name == "this" {
		return e.this, true
	}
	if v, ok := e.vars[name]; ok {
		return v, true
	}
	if e.parent != nil {
		return e.parent.get(name)
	}
	return nil, false
}

func (e *env) set(name string, v any) {
	e.vars[name] = v
}
