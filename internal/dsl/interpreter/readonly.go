package interpreter

import "fmt"

// readOnlyThis is an object-capability membrane used while evaluating an
// unattended debugger condition. It deliberately exposes Get but refuses Set
// and every method call. That closes writers already stored in local variables
// and mutating builtins such as FillPropertyValues without maintaining a list
// of object types or write-method aliases.
type readOnlyThis struct {
	ec    *execCtx
	inner This
}

type readOnlySetter struct{ ec *execCtx }

func protectReadOnly(ec *execCtx, value any) any {
	if ec == nil || ec.readOnlyReason == "" || value == nil {
		return value
	}
	if _, wrapped := value.(*readOnlyThis); wrapped {
		return value
	}
	if obj, ok := value.(This); ok {
		return &readOnlyThis{ec: ec, inner: obj}
	}
	// FillPropertyValues accepts a narrower Set-only destination. Cover it too,
	// otherwise a custom writer that omits Get would bypass the membrane.
	if _, ok := value.(interface{ Set(string, any) }); ok {
		return &readOnlySetter{ec: ec}
	}
	return value
}

func unwrapReadOnly(value any) any {
	if wrapped, ok := value.(*readOnlyThis); ok {
		return wrapped.inner
	}
	return value
}

func (r *readOnlyThis) Get(name string) any {
	if r == nil || r.inner == nil {
		return nil
	}
	return protectReadOnly(r.ec, r.inner.Get(name))
}

func (r *readOnlyThis) Set(name string, _ any) {
	r.refuse("изменение свойства «" + name + "»")
}

func (r *readOnlyThis) CallMethod(method string, _ []any) any {
	r.refuse("вызов метода «" + method + "»")
	return nil
}

// Fields preserves read-only use by helpers that inspect object fields. Set is
// still routed through the membrane, so accepting the object as a destination
// cannot mutate the stopped frame.
func (r *readOnlyThis) Fields() []string {
	if fields, ok := r.inner.(interface{ Fields() []string }); ok {
		return fields.Fields()
	}
	return nil
}

func (r *readOnlyThis) String() string {
	if r == nil {
		return ""
	}
	return fmt.Sprint(r.inner)
}

func (r *readOnlyThis) refuse(operation string) {
	reason := "вычисление только для чтения"
	if r != nil && r.ec != nil && r.ec.readOnlyReason != "" {
		reason = r.ec.readOnlyReason
	}
	RaiseUserError(operation + " недоступен: " + reason)
}

func (r *readOnlySetter) Set(name string, _ any) {
	reason := "вычисление только для чтения"
	if r != nil && r.ec != nil && r.ec.readOnlyReason != "" {
		reason = r.ec.readOnlyReason
	}
	RaiseUserError("изменение свойства «" + name + "» недоступно: " + reason)
}

func refuseReadOnly(ec *execCtx, operation string) {
	if ec == nil || ec.readOnlyReason == "" {
		return
	}
	RaiseUserError(operation + " недоступен: " + ec.readOnlyReason)
}
