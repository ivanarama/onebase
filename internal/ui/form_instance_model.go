package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

const (
	ManagedFormMaxNodes      = 256
	ManagedFormMaxDepth      = 16
	ManagedFormMaxOperations = 64
	ManagedFormMaxPatchBytes = 256 << 10
)

// ManagedFormOperation describes a model change, before HTML rendering. IDs are
// server assigned; snapshots and returned operations never alias the model.
type ManagedFormOperation struct {
	Op       string                `json:"op"`
	ID       string                `json:"id"`
	ParentID string                `json:"parentId,omitempty"`
	Position int                   `json:"position"`
	Property string                `json:"property,omitempty"`
	Value    any                   `json:"value,omitempty"`
	Element  *metadata.FormElement `json:"element,omitempty"`
}

type managedFormNode struct {
	el       *metadata.FormElement
	parent   *managedFormNode
	children []*managedFormNode
	frozen   bool
}

// ManagedFormRuntime is the isolated model from plan 172, slice A. It has no
// HTTP authority, lifetime or DOM contract; those belong to subsequent slices.
// Runs on one model serialize. Different models can share immutable metadata.
type ManagedFormRuntime struct {
	mu         sync.Mutex
	root       *managedFormNode
	paths      map[string]bool
	procedures map[string]*ast.ProcedureDecl
	prefix     string
	sequence   int
}

type managedFormTransaction struct {
	model    *ManagedFormRuntime
	root     *managedFormNode
	names    map[string]*managedFormNode
	sequence int
	ops      []ManagedFormOperation
	err      error
	active   bool
}

// NewManagedFormRuntime copies the complete element payload (including nested
// Props, options, filters and maps), and captures only declared data paths and
// AST procedures. The source tree must remain immutable after construction.
func NewManagedFormRuntime(form *metadata.FormModule, entity *metadata.Entity) (*ManagedFormRuntime, error) {
	if form == nil || form.LayoutKind != metadata.FormLayoutManaged {
		return nil, fmt.Errorf("runtime-структура требует управляемую форму")
	}
	m := &ManagedFormRuntime{paths: make(map[string]bool), procedures: make(map[string]*ast.ProcedureDecl), prefix: uuid.NewString()}
	if program, ok := form.ProgramAST.(*ast.Program); ok {
		for _, p := range program.Procedures {
			key := strings.ToLower(p.Name.Literal)
			if m.procedures[key] != nil {
				return nil, fmt.Errorf("неоднозначная процедура %q", p.Name.Literal)
			}
			m.procedures[key] = p // AST is immutable, just like registry code.
		}
	}
	addPath := func(path string) { m.paths[strings.ToLower(path)] = true }
	if entity != nil {
		for _, f := range entity.Fields {
			addPath("Объект." + f.Name)
		}
		for _, tp := range entity.TableParts {
			addPath("Объект." + tp.Name)
			for _, f := range tp.Fields {
				addPath("Объект." + tp.Name + "." + f.Name)
			}
		}
	}
	for _, attr := range form.Attributes {
		if attr == nil {
			continue
		}
		addPath(attr.Name)
		addPath("Форма." + attr.Name)
		for _, col := range attr.Columns {
			if col != nil {
				addPath(attr.Name + "." + col.Name)
				addPath("Форма." + attr.Name + "." + col.Name)
			}
		}
	}
	m.root = &managedFormNode{el: &metadata.FormElement{ID: "root", Name: "Корень", Kind: metadata.FormElementGroupBox}, frozen: true}
	seen := make(map[*metadata.FormElement]bool)
	names := map[string]bool{"корень": true, "root": true}
	count := 0
	var build func(*metadata.FormElement, *managedFormNode, int) (*managedFormNode, error)
	build = func(el *metadata.FormElement, parent *managedFormNode, depth int) (*managedFormNode, error) {
		count++
		if el == nil || seen[el] || count > ManagedFormMaxNodes || depth > ManagedFormMaxDepth {
			return nil, fmt.Errorf("неверное дерево или превышен лимит формы")
		}
		seen[el] = true
		if !metadata.IsKnownFormElementType(el.Kind) {
			return nil, fmt.Errorf("неизвестный вид %q", el.Kind)
		}
		if len(el.Children) > 0 && !el.IsContainer() {
			return nil, fmt.Errorf("элемент %q не контейнер", el.Name)
		}
		// Children are copied by the bounded tree walker, not recursively twice.
		payload := *el
		payload.Children = nil
		copy, err := cloneManagedElement(&payload)
		if err != nil {
			return nil, err
		}
		m.sequence++
		copy.ID = fmt.Sprintf("%s-%d", m.prefix, m.sequence)
		if copy.Name == "" {
			copy.Name = el.ID
		}
		if copy.Name == "" {
			copy.Name = copy.ID
		}
		key := strings.ToLower(copy.Name)
		if names[key] {
			return nil, fmt.Errorf("повтор имени %q", copy.Name)
		}
		names[key] = true
		n := &managedFormNode{el: copy, parent: parent, frozen: true}
		for _, child := range el.Children {
			c, err := build(child, n, depth+1)
			if err != nil {
				return nil, err
			}
			n.children = append(n.children, c)
		}
		return n, nil
	}
	for _, el := range form.Elements {
		n, err := build(el, m.root, 1)
		if err != nil {
			return nil, err
		}
		m.root.children = append(m.root.children, n)
	}
	return m, nil
}

// Run executes an actual form-module procedure with ЭтаФорма/ThisForm. All
// structural changes commit together only after successful DSL execution and
// validation. Object/data side effects retain the caller's existing semantics.
// The caller supplies the same interpreter, This and sandbox policy as its
// ordinary form execution; the immutable interpreter is copied before lookup.
func (m *ManagedFormRuntime) Run(name string, interp *interpreter.Interpreter, this interpreter.This, profile interpreter.SandboxProfile, vars map[string]any) ([]ManagedFormOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proc := m.procedures[strings.ToLower(name)]
	if proc == nil || interp == nil {
		return nil, fmt.Errorf("процедура формы %q не найдена", name)
	}
	root, err := cloneManagedNode(m.root, nil)
	if err != nil {
		return nil, err
	}
	tx := &managedFormTransaction{model: m, root: root, names: make(map[string]*managedFormNode), sequence: m.sequence, active: true}
	defer func() { tx.active = false }()
	tx.reindex()
	proxy := &managedFormProxy{tx: tx}
	extra := make(map[string]any, len(vars)+3)
	for key, value := range vars {
		if !strings.EqualFold(key, "ЭтаФорма") && !strings.EqualFold(key, "ThisForm") && !strings.EqualFold(key, "__form_procs__") {
			extra[key] = value
		}
	}
	extra["ЭтаФорма"] = proxy
	extra["ThisForm"] = proxy
	// The interpreter restricts this table to calls from the form source file.
	// Keep the caller's sibling/global resolvers intact for other modules.
	extra["__form_procs__"] = m.procedures
	local := *interp
	previousGuard := local.ValidateObjectAccess
	local.ValidateObjectAccess = func(object any) {
		// Check immutable transaction identity before touching the other proxy's
		// mutable state. A live foreign handler may run concurrently with this one.
		var owner *managedFormTransaction
		switch p := object.(type) {
		case *managedFormProxy:
			owner = p.tx
		case *managedFormElementsProxy:
			owner = p.tx
		case *managedFormElementProxy:
			owner = p.tx
		}
		if owner != nil {
			if owner != tx {
				// Poison this transaction, not the foreign/expired transaction: catching
				// the DSL exception must still discard our entire structural diff.
				tx.fail("элемент принадлежит другому экземпляру или обработчику")
			}
			tx.requireActive()
		}
		if previousGuard != nil {
			previousGuard(object)
		}
	}
	if err := local.RunSandboxed(proc, this, profile, nil, extra); err != nil {
		return nil, err
	}
	if tx.err != nil {
		return nil, tx.err
	}
	if profile.Context != nil {
		if err := profile.Context.Err(); err != nil {
			return nil, err
		}
	}
	if err := tx.checkTree(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(tx.ops)
	if err != nil || len(data) > ManagedFormMaxPatchBytes {
		return nil, fmt.Errorf("превышен лимит patch формы")
	}
	// Re-decode to sever any aliases held by the interpreter or operation values.
	var result []ManagedFormOperation
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var freeze func(*managedFormNode)
	freeze = func(n *managedFormNode) {
		n.frozen = true
		for _, c := range n.children {
			freeze(c)
		}
	}
	freeze(root)
	m.root = root
	m.sequence = tx.sequence
	return result, nil
}

// Snapshot returns a deep independent element tree, suitable for later render
// and authority integration. It exposes no mutable pointer into registry/model.
func (m *ManagedFormRuntime) Snapshot() ([]*metadata.FormElement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var snapshot func(*managedFormNode) (*metadata.FormElement, error)
	snapshot = func(n *managedFormNode) (*metadata.FormElement, error) {
		el, err := cloneManagedElement(n.el)
		if err != nil {
			return nil, err
		}
		for _, child := range n.children {
			c, err := snapshot(child)
			if err != nil {
				return nil, err
			}
			el.Children = append(el.Children, c)
		}
		return el, nil
	}
	var out []*metadata.FormElement
	for _, n := range m.root.children {
		el, err := snapshot(n)
		if err != nil {
			return nil, err
		}
		out = append(out, el)
	}
	return out, nil
}

func cloneManagedNode(n, parent *managedFormNode) (*managedFormNode, error) {
	el, err := cloneManagedElement(n.el)
	if err != nil {
		return nil, err
	}
	out := &managedFormNode{el: el, parent: parent, frozen: n.frozen}
	for _, child := range n.children {
		c, err := cloneManagedNode(child, out)
		if err != nil {
			return nil, err
		}
		out.children = append(out.children, c)
	}
	return out, nil
}

// Metadata property values are data: opaque references/functions and cycles
// are rejected rather than silently shared with another user's instance.
func cloneManagedElement(el *metadata.FormElement) (*metadata.FormElement, error) {
	value, err := cloneManagedValue(reflect.ValueOf(el), make(map[any]bool), 0)
	if err != nil {
		return nil, err
	}
	return value.Interface().(*metadata.FormElement), nil
}

func cloneManagedValue(v reflect.Value, active map[any]bool, depth int) (reflect.Value, error) {
	if depth > 64 {
		return reflect.Value{}, fmt.Errorf("слишком глубокие свойства формы")
	}
	if !v.IsValid() {
		return v, nil
	}
	out := reflect.New(v.Type()).Elem()
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Map || v.Kind() == reflect.Slice) && !v.IsNil() {
		key := struct {
			kind reflect.Kind
			ptr  uintptr
		}{v.Kind(), uintptr(v.UnsafePointer())}
		if active[key] {
			return out, fmt.Errorf("цикл в свойствах формы")
		}
		active[key] = true
		defer delete(active, key)
	}
	copyValue := func(value reflect.Value) (reflect.Value, error) { return cloneManagedValue(value, active, depth+1) }
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return out, nil
		}
		c, err := copyValue(v.Elem())
		if err != nil {
			return out, err
		}
		if v.Kind() == reflect.Pointer {
			out.Set(reflect.New(v.Type().Elem()))
			out.Elem().Set(c)
		} else {
			out.Set(c)
		}
	case reflect.Map:
		if v.IsNil() {
			return out, nil
		}
		out.Set(reflect.MakeMapWithSize(v.Type(), v.Len()))
		iter := v.MapRange()
		for iter.Next() {
			if iter.Key().Kind() != reflect.String {
				return out, fmt.Errorf("ключ свойства формы должен быть строкой")
			}
			c, err := copyValue(iter.Value())
			if err != nil {
				return out, err
			}
			out.SetMapIndex(iter.Key(), c)
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return out, nil
			}
			out.Set(reflect.MakeSlice(v.Type(), v.Len(), v.Len()))
		}
		for i := 0; i < v.Len(); i++ {
			c, err := copyValue(v.Index(i))
			if err != nil {
				return out, err
			}
			out.Index(i).Set(c)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !out.Field(i).CanSet() {
				return out, fmt.Errorf("непрозрачное свойство формы %s", v.Type())
			}
			c, err := copyValue(v.Field(i))
			if err != nil {
				return out, err
			}
			out.Field(i).Set(c)
		}
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		out.Set(v)
	default:
		return out, fmt.Errorf("неподдерживаемое свойство формы %s", v.Kind())
	}
	return out, nil
}

func (tx *managedFormTransaction) reindex() {
	clear(tx.names)
	var walk func(*managedFormNode)
	walk = func(n *managedFormNode) {
		tx.names[strings.ToLower(n.el.Name)] = n
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(tx.root)
}

func (tx *managedFormTransaction) checkTree() error {
	count := 0
	seen := make(map[*managedFormNode]bool)
	names := make(map[string]bool)
	var walk func(*managedFormNode, int) error
	walk = func(n *managedFormNode, depth int) error {
		if seen[n] || depth > ManagedFormMaxDepth {
			return fmt.Errorf("цикл или превышен лимит глубины формы")
		}
		seen[n] = true
		if n != tx.root {
			count++
			if count > ManagedFormMaxNodes {
				return fmt.Errorf("превышен лимит элементов формы")
			}
		}
		key := strings.ToLower(n.el.Name)
		if names[key] {
			return fmt.Errorf("повтор имени элемента формы")
		}
		names[key] = true
		if len(n.children) > 0 && !n.el.IsContainer() {
			return fmt.Errorf("родитель не контейнер")
		}
		for _, c := range n.children {
			if c.parent != n {
				return fmt.Errorf("неверный родитель")
			}
			if err := walk(c, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(tx.root, 0)
}

func (tx *managedFormTransaction) fail(message string) {
	if tx.err == nil {
		tx.err = fmt.Errorf("структура формы: %s", message)
	}
	interpreter.RaiseUserError(tx.err.Error())
}

func (tx *managedFormTransaction) requireActive() {
	if !tx.active {
		interpreter.RaiseUserError("ссылка на завершённый обработчик формы")
	}
	if tx.err != nil {
		interpreter.RaiseUserError(tx.err.Error())
	}
}

func (tx *managedFormTransaction) operation(op ManagedFormOperation) {
	tx.requireActive()
	if len(tx.ops) >= ManagedFormMaxOperations {
		tx.fail("превышен лимит операций")
	}
	tx.ops = append(tx.ops, op)
	if err := tx.checkTree(); err != nil {
		tx.fail(err.Error())
	}
	data, err := json.Marshal(tx.ops)
	if err != nil || len(data) > ManagedFormMaxPatchBytes {
		tx.fail("превышен лимит patch")
	}
}
