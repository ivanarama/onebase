package entityservice

import (
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"strings"
	"sync/atomic"
)

// InstallServerAssignedFieldQuery creates a readonly view limited to this
// OnWrite invocation. Retaining the closure cannot carry authority past finish.
// Nested Save calls install their own view, including an empty one for REST.
func InstallServerAssignedFieldQuery(vars map[string]any, fields map[string]bool) func() {
	var active atomic.Bool
	active.Store(true)
	fn := interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
		if !active.Load() || len(args) != 1 {
			return false, nil
		}
		name, ok := args[0].(string)
		return ok && fields[strings.ToLower(strings.TrimSpace(name))], nil
	})
	vars["ПолеНазначеноСервером"] = fn
	vars["ServerAssignedField"] = fn
	return func() { active.Store(false) }
}
