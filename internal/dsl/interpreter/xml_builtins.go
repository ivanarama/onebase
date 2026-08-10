package interpreter

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// XML в DSL сделан по образцу JSON (json_builtins.go): пара «прочитать/записать»
// работает со строкой, а не с потоком, и возвращает обычные коллекции DSL.
//
// Дерево элемента представлено Структурой с полями:
//
//	Имя       — имя тега (строка)
//	Атрибуты  — Соответствие «имя атрибута → значение» (строки)
//	Текст     — текстовое содержимое элемента (строка, без дочерних)
//	Элементы  — Массив вложенных элементов (таких же Структур)
//
// Такое представление обратимо: ЗаписатьXML(ПрочитатьXML(Текст)) даёт исходный
// документ с точностью до форматирования. Имена тегов живут в ЗНАЧЕНИИ поля Имя,
// а не в имени поля Структуры, — поэтому их регистр сохраняется: DSL
// регистронезависим и Struct.Set приводит имена полей к нижнему регистру.

const (
	xmlFieldName     = "Имя"
	xmlFieldAttrs    = "Атрибуты"
	xmlFieldText     = "Текст"
	xmlFieldChildren = "Элементы"
)

// xmlNode — промежуточное представление между encoding/xml и коллекциями DSL.
type xmlNode struct {
	Name     string
	Attrs    [][2]string
	Text     string
	Children []*xmlNode
}

func builtinReadXML(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		panic(userError{Msg: "ПрочитатьXML: ожидается 1 аргумент"})
	}
	text := strArg(args, 0)
	node, err := parseXMLDocument(text)
	if err != nil {
		panic(userError{Msg: "ПрочитатьXML: " + err.Error()})
	}
	return xmlNodeToDSL(node), nil
}

func parseXMLDocument(text string) (*xmlNode, error) {
	dec := xml.NewDecoder(strings.NewReader(text))
	dec.Strict = false
	var root *xmlNode
	var stack []*xmlNode
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: t.Name.Local}
			for _, a := range t.Attr {
				name := a.Name.Local
				if a.Name.Space != "" {
					name = a.Name.Space + ":" + a.Name.Local
				}
				node.Attrs = append(node.Attrs, [2]string{name, a.Value})
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			} else if root == nil {
				root = node
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			chunk := strings.TrimSpace(string(t))
			if chunk == "" {
				continue
			}
			cur := stack[len(stack)-1]
			cur.Text += chunk
		}
	}
	if root == nil {
		return nil, fmt.Errorf("документ не содержит корневого элемента")
	}
	return root, nil
}

func xmlNodeToDSL(n *xmlNode) any {
	s := &Struct{vals: map[string]any{}}
	s.Set(xmlFieldName, n.Name)

	attrs := &Map{}
	for _, kv := range n.Attrs {
		attrs.CallMethod("вставить", []any{kv[0], kv[1]})
	}
	s.Set(xmlFieldAttrs, attrs)
	s.Set(xmlFieldText, n.Text)

	children := &Array{}
	for _, c := range n.Children {
		children.items = append(children.items, xmlNodeToDSL(c))
	}
	s.Set(xmlFieldChildren, children)
	return s
}

func builtinWriteXML(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		panic(userError{Msg: "ЗаписатьXML: ожидается минимум 1 аргумент"})
	}
	rootName := "Элемент"
	if len(args) > 1 && args[1] != nil {
		if name := strings.TrimSpace(strArg(args, 1)); name != "" {
			rootName = name
		}
	}
	var sb strings.Builder
	if err := writeXMLValue(&sb, rootName, args[0]); err != nil {
		panic(userError{Msg: "ЗаписатьXML: " + err.Error()})
	}
	return sb.String(), nil
}

// writeXMLValue пишет значение как элемент name. Структура с полем «Имя»
// трактуется как узел дерева (обратный ход к ПрочитатьXML), остальное —
// как произвольные данные: поля становятся вложенными элементами.
func writeXMLValue(sb *strings.Builder, name string, v any) error {
	if node, ok := asXMLTreeNode(v); ok {
		return writeXMLNode(sb, node)
	}
	switch x := v.(type) {
	case *Struct:
		sb.WriteString("<" + name + ">")
		for _, k := range x.Fields() {
			if err := writeXMLValue(sb, k, x.Get(k)); err != nil {
				return err
			}
		}
		sb.WriteString("</" + name + ">")
	case *Map:
		sb.WriteString("<" + name + ">")
		for _, k := range x.Keys() {
			key := fmt.Sprintf("%v", k)
			if err := writeXMLValue(sb, key, x.Get(k)); err != nil {
				return err
			}
		}
		sb.WriteString("</" + name + ">")
	case *Array:
		sb.WriteString("<" + name + ">")
		for _, item := range x.Iterate() {
			if err := writeXMLValue(sb, "Элемент", item); err != nil {
				return err
			}
		}
		sb.WriteString("</" + name + ">")
	default:
		sb.WriteString("<" + name + ">")
		sb.WriteString(escapeXMLText(xmlStringOf(v)))
		sb.WriteString("</" + name + ">")
	}
	return nil
}

func writeXMLNode(sb *strings.Builder, n *xmlNode) error {
	sb.WriteString("<" + n.Name)
	for _, kv := range n.Attrs {
		sb.WriteString(" " + kv[0] + `="` + escapeXMLAttr(kv[1]) + `"`)
	}
	if n.Text == "" && len(n.Children) == 0 {
		sb.WriteString("/>")
		return nil
	}
	sb.WriteString(">")
	sb.WriteString(escapeXMLText(n.Text))
	for _, c := range n.Children {
		if err := writeXMLNode(sb, c); err != nil {
			return err
		}
	}
	sb.WriteString("</" + n.Name + ">")
	return nil
}

// asXMLTreeNode распознаёт Структуру, полученную из ПрочитатьXML.
func asXMLTreeNode(v any) (*xmlNode, bool) {
	s, ok := v.(*Struct)
	if !ok {
		return nil, false
	}
	nameVal := s.Get(xmlFieldName)
	if nameVal == nil {
		return nil, false
	}
	name := strings.TrimSpace(fmt.Sprintf("%v", nameVal))
	if name == "" {
		return nil, false
	}
	node := &xmlNode{Name: name}
	if attrs, ok := s.Get(xmlFieldAttrs).(*Map); ok {
		for _, k := range attrs.Keys() {
			node.Attrs = append(node.Attrs, [2]string{
				fmt.Sprintf("%v", k),
				xmlStringOf(attrs.Get(k)),
			})
		}
	}
	if txt := s.Get(xmlFieldText); txt != nil {
		node.Text = xmlStringOf(txt)
	}
	if children, ok := s.Get(xmlFieldChildren).(*Array); ok {
		for _, c := range children.Iterate() {
			child, ok := asXMLTreeNode(c)
			if !ok {
				return nil, false
			}
			node.Children = append(node.Children, child)
		}
	}
	return node, true
}

func builtinXMLString(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return "", nil
	}
	return xmlStringOf(args[0]), nil
}

// xmlStringOf — XML-представление примитива: числа без экспоненты, даты по
// ISO 8601, булево как true/false (а не «Да»/«Нет»).
func xmlStringOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case time.Time:
		return x.Format("2006-01-02T15:04:05")
	case decimal.Decimal:
		return x.String()
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// builtinXMLTypeOf — имя XSD-типа значения. Дополняет пару XMLСтрока/XMLЗначение:
// сериализуя значение, обычно надо сообщить приёмнику и его тип.
func builtinXMLTypeOf(args []any, file string, line int) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return "", nil
	}
	switch args[0].(type) {
	case string:
		return "string", nil
	case bool:
		return "boolean", nil
	case time.Time:
		return "dateTime", nil
	case decimal.Decimal, int64, int, float64:
		return "decimal", nil
	default:
		return "string", nil
	}
}

func builtinXMLValue(args []any, file string, line int) (any, error) {
	if len(args) < 2 {
		panic(userError{Msg: "XMLЗначение: ожидается 2 аргумента — имя типа и строка"})
	}
	typeName := strings.ToLower(strings.TrimSpace(strArg(args, 0)))
	text := strings.TrimSpace(strArg(args, 1))
	switch typeName {
	case "строка", "string":
		return strArg(args, 1), nil
	case "число", "number":
		d, err := decimal.NewFromString(text)
		if err != nil {
			panic(userError{Msg: "XMLЗначение: не число: " + text})
		}
		return d, nil
	case "булево", "boolean", "bool":
		switch strings.ToLower(text) {
		case "true", "1", "истина", "да":
			return true, nil
		case "false", "0", "ложь", "нет":
			return false, nil
		}
		panic(userError{Msg: "XMLЗначение: не булево: " + text})
	case "дата", "date":
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "20060102150405", "20060102"} {
			if t, err := time.Parse(layout, text); err == nil {
				return t, nil
			}
		}
		panic(userError{Msg: "XMLЗначение: не дата: " + text})
	default:
		panic(userError{Msg: "XMLЗначение: неизвестный тип «" + strArg(args, 0) + "» (ожидается Строка, Число, Дата или Булево)"})
	}
}

func builtinFindDisallowedXMLChars(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return int64(0), nil
	}
	text := strArg(args, 0)
	for i, r := range []rune(text) {
		if !isAllowedXMLRune(r) {
			return int64(i + 1), nil // 1-based, как принято в строковых функциях
		}
	}
	return int64(0), nil
}

// isAllowedXMLRune — диапазоны символов, допустимых в XML 1.0 (раздел 2.2).
func isAllowedXMLRune(r rune) bool {
	switch {
	case r == 0x09 || r == 0x0A || r == 0x0D:
		return true
	case r >= 0x20 && r <= 0xD7FF:
		return true
	case r >= 0xE000 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0x10FFFF:
		return true
	default:
		return false
	}
}

func escapeXMLText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeXMLAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
