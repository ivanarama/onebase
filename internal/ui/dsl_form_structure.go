package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

type managedFormProxy struct{ tx *managedFormTransaction }
type managedFormElementsProxy struct{ tx *managedFormTransaction }
type managedFormElementProxy struct {
	tx   *managedFormTransaction
	node *managedFormNode
}

func (f *managedFormProxy) Get(name string) any {
	f.tx.requireActive()
	switch strings.ToLower(name) {
	case "элементы", "elements":
		return &managedFormElementsProxy{f.tx}
	}
	return nil
}
func (f *managedFormProxy) Set(name string, _ any) {
	f.tx.fail("свойство формы " + name + " недоступно для записи")
}

func (c *managedFormElementsProxy) proxy(n *managedFormNode) any {
	if n == nil {
		return nil
	}
	return &managedFormElementProxy{c.tx, n}
}
func (c *managedFormElementsProxy) Get(name string) any {
	c.tx.requireActive()
	if strings.EqualFold(name, "Корень") || strings.EqualFold(name, "Root") {
		return c.proxy(c.tx.root)
	}
	return c.proxy(c.tx.names[strings.ToLower(name)])
}
func (c *managedFormElementsProxy) Set(name string, _ any) {
	c.tx.fail("коллекция элементов доступна только для чтения: " + name)
}

func (c *managedFormElementsProxy) element(value any) *managedFormNode {
	p, ok := value.(*managedFormElementProxy)
	if !ok || p.tx != c.tx {
		c.tx.fail("элемент принадлежит другому экземпляру или обработчику")
	}
	p.live()
	return p.node
}

func formStructureString(tx *managedFormTransaction, value any) string {
	s, ok := value.(string)
	if !ok {
		tx.fail("ожидалась строка")
	}
	return s
}

func formStructurePosition(tx *managedFormTransaction, args []any, offset, length int) int {
	if len(args) == offset {
		return length
	}
	var value float64
	switch v := args[offset].(type) {
	case decimal.Decimal:
		// Do not round a fractional DSL decimal to an integer through float64.
		if !v.Equal(v.Truncate(0)) || v.IsNegative() || v.GreaterThan(decimal.NewFromInt(int64(length))) {
			tx.fail("позиция вне границ коллекции или не целая")
		}
		return int(v.IntPart())
	case float64:
		value = v
	case int:
		value = float64(v)
	case int64:
		value = float64(v)
	default:
		tx.fail("позиция должна быть целым числом")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value < 0 || value > float64(length) {
		tx.fail("позиция вне границ коллекции")
	}
	return int(value)
}

func detachManagedNode(n *managedFormNode) {
	for i, c := range n.parent.children {
		if c == n {
			n.parent.children = append(n.parent.children[:i], n.parent.children[i+1:]...)
			return
		}
	}
}
func insertManagedNode(parent, n *managedFormNode, pos int) {
	parent.children = append(parent.children, nil)
	copy(parent.children[pos+1:], parent.children[pos:])
	parent.children[pos] = n
	n.parent = parent
}

func (c *managedFormElementsProxy) CallMethod(method string, args []any) any {
	c.tx.requireActive()
	switch strings.ToLower(method) {
	case "найти", "find":
		if len(args) != 1 {
			c.tx.fail("Найти требует имя")
		}
		return c.proxy(c.tx.names[strings.ToLower(formStructureString(c.tx, args[0]))])
	case "добавить", "add":
		if len(args) != 3 && len(args) != 4 {
			c.tx.fail("Добавить требует имя, вид, родителя и необязательную позицию")
		}
		name := formStructureString(c.tx, args[0])
		kind := metadata.FormElementType(formStructureString(c.tx, args[1]))
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || c.tx.names[strings.ToLower(name)] != nil || strings.EqualFold(name, "root") {
			c.tx.fail("пустое или повторное имя элемента")
		}
		if kind == "" || !metadata.IsKnownFormElementType(kind) {
			c.tx.fail("неизвестный вид элемента")
		}
		parent := c.element(args[2])
		if !parent.el.IsContainer() {
			c.tx.fail("родитель не контейнер")
		}
		pos := formStructurePosition(c.tx, args, 3, len(parent.children))
		c.tx.sequence++
		n := &managedFormNode{el: &metadata.FormElement{ID: fmt.Sprintf("%s-%d", c.tx.model.prefix, c.tx.sequence), Name: name, Kind: kind, Visible: true, Enabled: true}}
		insertManagedNode(parent, n, pos)
		c.tx.reindex()
		payload, err := cloneManagedElement(n.el)
		if err != nil {
			c.tx.fail(err.Error())
		}
		c.tx.operation(ManagedFormOperation{Op: "insert", ID: n.el.ID, ParentID: parent.el.ID, Position: pos, Element: payload})
		return c.proxy(n)
	case "удалить", "remove":
		if len(args) != 1 {
			c.tx.fail("Удалить требует элемент")
		}
		n := c.element(args[0])
		if n == c.tx.root {
			c.tx.fail("нельзя удалить корень")
		}
		detachManagedNode(n)
		c.tx.reindex()
		c.tx.operation(ManagedFormOperation{Op: "remove", ID: n.el.ID})
		return nil
	case "переместить", "move":
		if len(args) != 2 && len(args) != 3 {
			c.tx.fail("Переместить требует элемент, родителя и необязательную позицию")
		}
		n := c.element(args[0])
		parent := c.element(args[1])
		if n == c.tx.root || !parent.el.IsContainer() {
			c.tx.fail("нельзя переместить корень или выбрать не контейнер")
		}
		for ancestor := parent; ancestor != nil; ancestor = ancestor.parent {
			if ancestor == n {
				c.tx.fail("цикл в дереве формы")
			}
		}
		length := len(parent.children)
		if n.parent == parent {
			length--
		}
		pos := formStructurePosition(c.tx, args, 2, length)
		detachManagedNode(n)
		insertManagedNode(parent, n, pos)
		c.tx.operation(ManagedFormOperation{Op: "move", ID: n.el.ID, ParentID: parent.el.ID, Position: pos})
		return nil
	}
	c.tx.fail("неизвестный метод коллекции: " + method)
	return nil
}

func (p *managedFormElementProxy) live() {
	p.tx.requireActive()
	if p.tx.names[strings.ToLower(p.node.el.Name)] != p.node {
		p.tx.fail("элемент уже удалён")
	}
}
func (p *managedFormElementProxy) Get(name string) any {
	p.live()
	el := p.node.el
	switch strings.ToLower(name) {
	case "имя", "name":
		return el.Name
	case "вид", "kind":
		return string(el.Kind)
	case "родитель", "parent":
		if p.node.parent != nil {
			return &managedFormElementProxy{p.tx, p.node.parent}
		}
		return nil
	case "дочерние", "children":
		items := make([]any, 0, len(p.node.children))
		for _, n := range p.node.children {
			items = append(items, &managedFormElementProxy{p.tx, n})
		}
		return interpreter.NewArray(items)
	case "заголовок", "title":
		return el.Title
	case "путькданным", "datapath":
		return el.DataPath
	case "видимость", "visible":
		return el.Visible
	case "доступность", "enabled":
		return el.Enabled
	case "толькопросмотр", "readonly":
		return el.ReadOnly
	}
	return nil
}

func (p *managedFormElementProxy) Set(name string, value any) {
	p.live()
	el := p.node.el
	if p.node == p.tx.root {
		p.tx.fail("свойства корня неизменяемы")
	}
	property := strings.ToLower(name)
	switch property {
	case "имя", "name":
		if p.node.frozen {
			p.tx.fail("имя зафиксированного элемента неизменяемо")
		}
		s := formStructureString(p.tx, value)
		other := p.tx.names[strings.ToLower(s)]
		if strings.TrimSpace(s) == "" || s != strings.TrimSpace(s) || (other != nil && other != p.node) || strings.EqualFold(s, "root") {
			p.tx.fail("пустое или повторное имя")
		}
		el.Name = s
		p.tx.reindex()
		property = "name"
	case "вид", "kind":
		if p.node.frozen {
			p.tx.fail("вид зафиксированного элемента неизменяем")
		}
		kind := metadata.FormElementType(formStructureString(p.tx, value))
		if kind == "" || !metadata.IsKnownFormElementType(kind) {
			p.tx.fail("неизвестный вид")
		}
		el.Kind = kind
		property = "kind"
	case "путькданным", "datapath":
		if p.node.frozen {
			p.tx.fail("путь зафиксированного элемента неизменяем")
		}
		path := formStructureString(p.tx, value)
		if path != "" && !p.tx.model.paths[strings.ToLower(path)] {
			p.tx.fail("путь не объявлен в метаданных")
		}
		el.DataPath = path
		property = "dataPath"
	case "заголовок", "title":
		el.Title = formStructureString(p.tx, value)
		if el.TitleMap == nil {
			el.TitleMap = make(map[string]string)
		}
		el.TitleMap["ru"] = el.Title
		property = "title"
	case "видимость", "visible", "доступность", "enabled", "толькопросмотр", "readonly":
		v, ok := value.(bool)
		if !ok {
			p.tx.fail("свойство требует булево значение")
		}
		switch property {
		case "видимость", "visible":
			el.Visible = v
			property = "visible"
		case "доступность", "enabled":
			el.Enabled = v
			property = "enabled"
		default:
			el.ReadOnly = v
			property = "readOnly"
		}
	default:
		p.tx.fail("свойство недоступно для записи: " + name)
	}
	p.tx.operation(ManagedFormOperation{Op: "set", ID: el.ID, Property: property, Value: value})
}

func (p *managedFormElementProxy) CallMethod(method string, args []any) any {
	p.live()
	if !strings.EqualFold(method, "УстановитьДействие") && !strings.EqualFold(method, "SetAction") {
		p.tx.fail("неизвестный метод элемента: " + method)
	}
	if len(args) != 2 {
		p.tx.fail("УстановитьДействие требует событие и процедуру")
	}
	event := metadata.FormEventType(formStructureString(p.tx, args[0]))
	proc := formStructureString(p.tx, args[1])
	if !metadata.IsKnownFormEventType(event) {
		p.tx.fail("неизвестное событие")
	}
	decl := p.tx.model.procedures[strings.ToLower(proc)]
	if decl == nil {
		p.tx.fail("процедура отсутствует в AST модуля формы")
	}
	if p.node.el.Handlers == nil {
		p.node.el.Handlers = make(map[metadata.FormEventType]string)
	}
	p.node.el.Handlers[event] = decl.Name.Literal
	p.tx.operation(ManagedFormOperation{Op: "set", ID: p.node.el.ID, Property: "handler:" + string(event), Value: decl.Name.Literal})
	return nil
}
