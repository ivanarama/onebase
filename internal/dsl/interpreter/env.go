package interpreter

// This is implemented by runtime.Object; defined here to avoid import cycles.
type This interface {
	Get(name string) any
	Set(name string, v any)
}

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
