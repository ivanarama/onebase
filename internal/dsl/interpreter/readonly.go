package interpreter

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/shopspring/decimal"
)

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

// readOnlyOpaque hides a host value whose formatting or serialization is an
// arbitrary Go callback. Keeping the original value out of the wrapper is
// deliberate: reflection-based serializers must not be able to reach it.
type readOnlyOpaque struct{ ec *execCtx }

type readOnlyStringValue interface {
	stringValue() string
}

const (
	maxReadOnlyTraversalNodes = 4096
	maxReadOnlyTraversalDepth = 64
)

func protectReadOnly(ec *execCtx, value any) any {
	return protectReadOnlySeen(ec, value, &readOnlyTraversal{}, 0)
}

type readOnlyVisit struct {
	kind byte
	ptr  uintptr
	len  int
}

type readOnlyTraversal struct {
	seen  map[readOnlyVisit]any
	nodes int
}

func (t *readOnlyTraversal) enter(ec *execCtx, depth int) {
	if depth > maxReadOnlyTraversalDepth {
		RaiseUserError(fmt.Sprintf("глубина значения в условии точки останова превышает предел %d", maxReadOnlyTraversalDepth))
	}
	if t.nodes >= maxReadOnlyTraversalNodes {
		RaiseUserError(fmt.Sprintf("размер значения в условии точки останова превышает предел %d узлов", maxReadOnlyTraversalNodes))
	}
	t.nodes++
	if depth == 0 || t.nodes%64 == 0 {
		ec.checkDeadline()
	}
}

// allowChildren rejects a flat attacker-controlled collection before make
// allocates its full length. Nested values are charged individually by enter.
func (t *readOnlyTraversal) allowChildren(count int) {
	if count < 0 || count > maxReadOnlyTraversalNodes-t.nodes {
		RaiseUserError(fmt.Sprintf("размер коллекции в условии точки останова превышает предел %d узлов", maxReadOnlyTraversalNodes))
	}
}

func protectReadOnlySeen(ec *execCtx, value any, traversal *readOnlyTraversal, depth int) any {
	if ec == nil || ec.readOnlyReason == "" || value == nil {
		return value
	}
	traversal.enter(ec, depth)
	if _, wrapped := value.(*readOnlyThis); wrapped {
		return value
	}
	if _, wrapped := value.(*readOnlyOpaque); wrapped {
		return value
	}
	// Native collections stay usable for indexing and pure builtins, but get a
	// per-evaluation copy whose nested host callbacks are protected as well.
	// The visited map preserves cycles instead of recursing forever.
	switch collection := value.(type) {
	case *Array:
		if collection == nil {
			return collection
		}
		traversal.allowChildren(len(collection.items))
		ec.checkDeadline()
		if traversal.seen == nil {
			traversal.seen = make(map[readOnlyVisit]any)
		}
		visit := readOnlyVisit{kind: 'a', ptr: reflect.ValueOf(collection).Pointer()}
		if protected, ok := traversal.seen[visit]; ok {
			return protected
		}
		copy := &Array{items: make([]any, len(collection.items))}
		traversal.seen[visit] = copy
		for index, item := range collection.items {
			copy.items[index] = protectReadOnlySeen(ec, item, traversal, depth+1)
		}
		return copy
	case *Map:
		if collection == nil {
			return collection
		}
		remaining := maxReadOnlyTraversalNodes - traversal.nodes
		if len(collection.keys) > remaining || len(collection.vals) > remaining-len(collection.keys) {
			traversal.allowChildren(maxReadOnlyTraversalNodes)
		}
		traversal.allowChildren(len(collection.keys) + len(collection.vals))
		ec.checkDeadline()
		if traversal.seen == nil {
			traversal.seen = make(map[readOnlyVisit]any)
		}
		visit := readOnlyVisit{kind: 'm', ptr: reflect.ValueOf(collection).Pointer()}
		if protected, ok := traversal.seen[visit]; ok {
			return protected
		}
		copy := &Map{keys: make([]any, len(collection.keys)), vals: make([]any, len(collection.vals))}
		traversal.seen[visit] = copy
		for index, key := range collection.keys {
			copy.keys[index] = protectReadOnlySeen(ec, key, traversal, depth+1)
		}
		for index, item := range collection.vals {
			copy.vals[index] = protectReadOnlySeen(ec, item, traversal, depth+1)
		}
		return copy
	case *Struct:
		if collection == nil {
			return collection
		}
		traversal.allowChildren(len(collection.keys))
		ec.checkDeadline()
		if traversal.seen == nil {
			traversal.seen = make(map[readOnlyVisit]any)
		}
		visit := readOnlyVisit{kind: 't', ptr: reflect.ValueOf(collection).Pointer()}
		if protected, ok := traversal.seen[visit]; ok {
			return protected
		}
		copy := &Struct{keys: append([]string(nil), collection.keys...), vals: make(map[string]any, len(collection.vals))}
		wrapped := &readOnlyThis{ec: ec, inner: copy}
		traversal.seen[visit] = wrapped
		for _, name := range collection.keys {
			copy.vals[name] = protectReadOnlySeen(ec, collection.vals[name], traversal, depth+1)
		}
		return wrapped
	case []any:
		traversal.allowChildren(len(collection))
		ec.checkDeadline()
		if traversal.seen == nil {
			traversal.seen = make(map[readOnlyVisit]any)
		}
		// Pointer alone is not a slice identity: overlapping views can start at
		// the same element while having different lengths. Reusing the shorter
		// snapshot for the longer view would silently drop values (and vice
		// versa) during formatting/JSON conversion.
		visit := readOnlyVisit{kind: 's', ptr: reflect.ValueOf(collection).Pointer(), len: len(collection)}
		if protected, ok := traversal.seen[visit]; ok {
			return protected
		}
		copy := make([]any, len(collection))
		traversal.seen[visit] = copy
		for index, item := range collection {
			copy[index] = protectReadOnlySeen(ec, item, traversal, depth+1)
		}
		return copy
	case map[string]any:
		traversal.allowChildren(len(collection))
		ec.checkDeadline()
		if traversal.seen == nil {
			traversal.seen = make(map[readOnlyVisit]any)
		}
		visit := readOnlyVisit{kind: 'r', ptr: reflect.ValueOf(collection).Pointer()}
		if protected, ok := traversal.seen[visit]; ok {
			return protected
		}
		copy := make(map[string]any, len(collection))
		traversal.seen[visit] = copy
		for name, item := range collection {
			copy[name] = protectReadOnlySeen(ec, item, traversal, depth+1)
		}
		return copy
	}
	// References and DSL errors are data wrappers around host capabilities.
	// Keep their inert display fields but deliberately strip Manager,
	// AttrResolver and the wrapped Err before reflection-based serializers can
	// reach an arbitrary callback through an exported interface field.
	switch value := value.(type) {
	case *Ref:
		if value == nil {
			return value
		}
		return &Ref{UUID: value.UUID, Name: value.Name, Type: value.Type}
	case *DSLError:
		if value == nil {
			return value
		}
		return &DSLError{File: value.File, Line: value.Line, Msg: value.Msg}
	}
	if obj, ok := value.(This); ok {
		return &readOnlyThis{ec: ec, inner: obj}
	}
	// FillPropertyValues accepts a narrower Set-only destination. Cover it too,
	// otherwise a custom writer that omits Get would bypass the membrane.
	if _, ok := value.(interface{ Set(string, any) }); ok {
		return &readOnlySetter{ec: ec}
	}
	if trustedReadOnlyCallbackValue(value) {
		return value
	}
	if hasReadOnlyCallback(value) || isReadOnlyComposite(value) {
		return &readOnlyOpaque{ec: ec}
	}
	return value
}

// trustedReadOnlyCallbackValue lists DSL-native values whose String/JSON
// methods are inert data conversions. Everything else that advertises a
// formatting or serialization callback stays behind readOnlyOpaque.
func trustedReadOnlyCallbackValue(value any) bool {
	switch value.(type) {
	case time.Time, *time.Time, time.Duration,
		decimal.Decimal, *decimal.Decimal,
		json.Number,
		[]byte:
		return true
	default:
		return false
	}
}

// isReadOnlyComposite fails closed for host composites that are not one of
// the native collections snapshotted above. fmt and encoding/json recurse
// through structs, pointers, arrays, slices and maps and may invoke a nested
// Stringer/Formatter/Marshaler even when the outer value advertises no
// callback itself. A type-preserving generic copy cannot replace a concrete
// callback-typed field with readOnlyOpaque, so the whole host value stays
// opaque. Plain scalar values remain available.
func isReadOnlyComposite(value any) bool {
	switch reflect.ValueOf(value).Kind() {
	case reflect.Array, reflect.Map, reflect.Pointer, reflect.Slice, reflect.Struct:
		return true
	default:
		return false
	}
}

func hasReadOnlyCallback(value any) bool {
	switch value.(type) {
	case fmt.Formatter, fmt.GoStringer, fmt.Stringer, error,
		json.Marshaler, encoding.TextMarshaler:
		return true
	default:
		return false
	}
}

func formatReadOnlyValue(value any) string {
	if protected, ok := value.(readOnlyStringValue); ok {
		return protected.stringValue()
	}
	return fmt.Sprintf("%v", value)
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
	return r.stringValue()
}

// stringValue preserves the inert built-in representations but never invokes
// an arbitrary Go String/Stringer callback behind the read-only membrane.
// fmt deliberately recovers panics raised by String methods, so callers that
// need a fail-closed error call this helper directly before entering fmt.
func (r *readOnlyThis) stringValue() string {
	if r == nil || r.inner == nil {
		return ""
	}
	switch value := r.inner.(type) {
	case *Struct:
		return value.String()
	case *ValueTable:
		return value.String()
	case *MapThis:
		for _, field := range []string{"наименование", "name"} {
			if name, ok := value.Get(field).(string); ok {
				return name
			}
		}
		return ""
	}
	// This.Get is already the explicit read-only contract used by member
	// expressions. Preserve the common Строка(Объект) display-name case without
	// falling through to an unconstrained fmt.Stringer implementation.
	for _, field := range []string{"наименование", "name", "номер", "number"} {
		if name, ok := r.inner.Get(field).(string); ok {
			return name
		}
	}
	r.refuse("преобразование внешнего объекта в строку")
	return ""
}

func (r *readOnlyOpaque) String() string { return r.stringValue() }

func (r *readOnlyOpaque) stringValue() string {
	refuseReadOnly(r.ec, "преобразование внешнего значения в строку")
	return ""
}

func (r *readOnlyOpaque) MarshalJSON() ([]byte, error) {
	refuseReadOnly(r.ec, "сериализация внешнего значения")
	return nil, nil
}

func (r *readOnlyOpaque) IterateRows() []map[string]any {
	refuseReadOnly(r.ec, "итерация внешней коллекции")
	return nil
}

func (r *readOnlyOpaque) IterateThis() []This {
	refuseReadOnly(r.ec, "итерация внешней коллекции")
	return nil
}

func (r *readOnlyThis) refuse(operation string) {
	if r == nil || r.ec == nil || r.ec.readOnlyReason == "" {
		RaiseUserError(operation + " недоступен: вычисление только для чтения")
	}
	refuseReadOnly(r.ec, operation)
}

func (r *readOnlySetter) Set(name string, _ any) {
	operation := "изменение свойства «" + name + "»"
	if r == nil || r.ec == nil || r.ec.readOnlyReason == "" {
		RaiseUserError(operation + " недоступно: вычисление только для чтения")
	}
	refuseReadOnly(r.ec, operation)
}

func refuseReadOnly(ec *execCtx, operation string) {
	if ec == nil || ec.readOnlyReason == "" {
		return
	}
	if ec.readOnlyViolation == "" {
		ec.readOnlyViolation = operation
	}
	RaiseUserError(operation + " недоступен: " + ec.readOnlyReason)
}

// checkReadOnlyViolation closes a subtle fmt behavior: fmt recovers panics
// raised by String/Format and returns a %!v(PANIC=...) marker. The violation is
// therefore sticky on execCtx and is re-raised at the enclosing expression
// boundary even when a formatter swallowed the original panic.
func (ec *execCtx) checkReadOnlyViolation() {
	if ec == nil || ec.readOnlyViolation == "" {
		return
	}
	reason := ec.readOnlyReason
	if reason == "" {
		reason = "вычисление только для чтения"
	}
	RaiseUserError(ec.readOnlyViolation + " недоступен: " + reason)
}
