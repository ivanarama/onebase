package interpreter

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
// Такое представление сохраняет структуру документа, но намеренно не моделирует
// пространства имён, смешанное содержимое, комментарии и инструкции обработки.
// Эти конструкции отклоняются, чтобы чтение никогда не теряло данные молча.
// Имена тегов живут в ЗНАЧЕНИИ поля Имя, а не в имени поля Структуры, — поэтому
// их регистр сохраняется: DSL регистронезависим и Struct.Set приводит имена полей
// к нижнему регистру.

const (
	xmlFieldName     = "Имя"
	xmlFieldAttrs    = "Атрибуты"
	xmlFieldText     = "Текст"
	xmlFieldChildren = "Элементы"

	// Ограничения защищают рекурсивное преобразование в коллекции DSL и обратно
	// от переполнения стека и документов, создающих чрезмерное число объектов.
	maxXMLDepth = 256
	maxXMLNodes = 100_000

	// Decimal ограничивается до любых операций, способных развернуть exponent
	// в строку. 10 000 цифр существенно больше прикладных numeric-значений, но
	// оставляет предсказуемый верхний предел памяти и размера XML.
	maxXMLDecimalLexicalLength = 10_000
	maxXMLDecimalDigits        = 10_000
	maxXMLDecimalOutputLength  = 10_000
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
	// UTF-8 BOM допустим только один раз и только в самом начале документа.
	// Убираем его до подсчёта сырых span'ов токенов.
	decoderInput := text
	if strings.HasPrefix(decoderInput, "\uFEFF") {
		decoderInput = strings.TrimPrefix(decoderInput, "\uFEFF")
	}
	dec := xml.NewDecoder(strings.NewReader(decoderInput))
	dec.Strict = true

	type frame struct {
		node *xmlNode
		text strings.Builder
	}

	var root *xmlNode
	var stack []*frame
	nodeCount := 0
	var previousOffset int64
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		currentOffset := dec.InputOffset()
		if currentOffset < previousOffset || currentOffset > int64(len(decoderInput)) {
			return nil, fmt.Errorf("некорректная позиция XML-декодера")
		}
		rawToken := decoderInput[int(previousOffset):int(currentOffset)]
		previousOffset = currentOffset
		switch t := tok.(type) {
		case xml.StartElement:
			depth := len(stack) + 1
			if depth > maxXMLDepth {
				return nil, fmt.Errorf("глубина XML превышает предел %d", maxXMLDepth)
			}
			nodeCount++
			if nodeCount > maxXMLNodes {
				return nil, fmt.Errorf("число элементов XML превышает предел %d", maxXMLNodes)
			}

			name, err := decodedXMLName(t.Name, "элемента")
			if err != nil {
				return nil, err
			}
			node := &xmlNode{Name: name}
			seenAttrs := make(map[string]struct{}, len(t.Attr))
			for _, a := range t.Attr {
				attrName, err := decodedXMLName(a.Name, "атрибута")
				if err != nil {
					return nil, err
				}
				if attrName == "xmlns" {
					return nil, fmt.Errorf("пространства имён XML не поддерживаются (атрибут xmlns)")
				}
				if _, exists := seenAttrs[attrName]; exists {
					return nil, fmt.Errorf("повторяющийся атрибут «%s» элемента «%s»", attrName, name)
				}
				seenAttrs[attrName] = struct{}{}
				node.Attrs = append(node.Attrs, [2]string{attrName, a.Value})
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1].node
				parent.Children = append(parent.Children, node)
			} else {
				if root != nil {
					return nil, fmt.Errorf("XML содержит более одного корневого элемента")
				}
				root = node
			}
			stack = append(stack, &frame{node: node})
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("закрывающий тег «%s» не имеет открывающего", t.Name.Local)
			}
			name, err := decodedXMLName(t.Name, "элемента")
			if err != nil {
				return nil, err
			}
			cur := stack[len(stack)-1]
			if cur.node.Name != name {
				return nil, fmt.Errorf("закрывающий тег «%s» не соответствует «%s»", name, cur.node.Name)
			}
			text := cur.text.String()
			if len(cur.node.Children) > 0 {
				if !isXMLWhitespaceOnly(text) {
					return nil, fmt.Errorf("смешанное содержимое элемента «%s» не поддерживается", cur.node.Name)
				}
				// Пробельное форматирование между дочерними элементами не является
				// данными дерева и намеренно не сохраняется.
				cur.node.Text = ""
			} else {
				// У текстового элемента сохраняем пробелы без TrimSpace.
				cur.node.Text = text
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				// Проверяем именно исходные байты токена. Decoder уже превратил бы
				// &#x20; или CDATA в пробельный CharData, хотя такие конструкции вне
				// корня запрещены грамматикой XML-документа.
				if !isXMLWhitespaceOnly(rawToken) {
					return nil, fmt.Errorf("текст вне корневого элемента XML")
				}
				continue
			}
			stack[len(stack)-1].text.Write([]byte(t))
		case xml.Comment:
			return nil, fmt.Errorf("комментарии XML не поддерживаются")
		case xml.Directive:
			return nil, fmt.Errorf("директивы XML не поддерживаются")
		case xml.ProcInst:
			return nil, fmt.Errorf("инструкции обработки XML не поддерживаются")
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("XML неожиданно завершён внутри элемента «%s»", stack[len(stack)-1].node.Name)
	}
	if root == nil {
		return nil, fmt.Errorf("документ не содержит корневого элемента")
	}
	return root, nil
}

func decodedXMLName(name xml.Name, kind string) (string, error) {
	if name.Space != "" {
		return "", fmt.Errorf("пространства имён XML не поддерживаются (%s «%s»)", kind, name.Local)
	}
	if err := validateXMLName(name.Local); err != nil {
		return "", fmt.Errorf("недопустимое имя %s «%s»: %w", kind, name.Local, err)
	}
	return name.Local, nil
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
		name, ok := args[1].(string)
		if !ok {
			panic(userError{Msg: "ЗаписатьXML: имя корня должно быть строкой"})
		}
		rootName = name
	}
	if err := validateXMLName(rootName); err != nil {
		panic(userError{Msg: "ЗаписатьXML: недопустимое имя корня «" + rootName + "»: " + err.Error()})
	}

	var sb strings.Builder
	w := &xmlWriter{encoder: xml.NewEncoder(&sb)}
	if err := w.writeValue(rootName, args[0], 1); err != nil {
		panic(userError{Msg: "ЗаписатьXML: " + err.Error()})
	}
	if err := w.encoder.Flush(); err != nil {
		panic(userError{Msg: "ЗаписатьXML: " + err.Error()})
	}
	return sb.String(), nil
}

type xmlWriter struct {
	encoder *xml.Encoder
	nodes   int
}

// writeValue пишет значение как элемент name. Структура с полем «Имя» всегда
// считается узлом дерева. Если форма такого узла неверна, возвращается ошибка:
// молчаливый fallback к произвольной Структуре потерял бы часть данных.
func (w *xmlWriter) writeValue(name string, v any, depth int) error {
	budget := &xmlTreeBudget{}
	if node, isTree, err := asXMLTreeNode(v, depth, budget); err != nil {
		return err
	} else if isTree {
		return w.writeNode(node, depth)
	}

	start, err := w.startElement(name, nil, depth)
	if err != nil {
		return err
	}
	switch x := v.(type) {
	case *Struct:
		if x == nil {
			break
		}
		for _, k := range x.Fields() {
			if err := w.writeValue(k, x.Get(k), depth+1); err != nil {
				return err
			}
		}
	case *Map:
		if x == nil {
			break
		}
		for _, k := range x.Keys() {
			key, ok := k.(string)
			if !ok {
				return fmt.Errorf("имя элемента из ключа Соответствия должно быть строкой, получено %T", k)
			}
			if err := w.writeValue(key, x.Get(k), depth+1); err != nil {
				return err
			}
		}
	case *Array:
		if x == nil {
			break
		}
		for _, item := range x.Iterate() {
			if err := w.writeValue("Элемент", item, depth+1); err != nil {
				return err
			}
		}
	default:
		text, err := xmlStringOf(v)
		if err != nil {
			return fmt.Errorf("текст элемента «%s»: %w", name, err)
		}
		if err := validateXMLCharacters(text); err != nil {
			return fmt.Errorf("текст элемента «%s»: %w", name, err)
		}
		if text != "" {
			if err := w.encoder.EncodeToken(xml.CharData([]byte(text))); err != nil {
				return fmt.Errorf("текст элемента «%s»: %w", name, err)
			}
		}
	}
	if err := w.encoder.EncodeToken(start.End()); err != nil {
		return fmt.Errorf("закрытие элемента «%s»: %w", name, err)
	}
	return nil
}

func (w *xmlWriter) writeNode(n *xmlNode, depth int) error {
	start, err := w.startElement(n.Name, n.Attrs, depth)
	if err != nil {
		return err
	}
	if n.Text != "" {
		if err := w.encoder.EncodeToken(xml.CharData([]byte(n.Text))); err != nil {
			return fmt.Errorf("текст элемента «%s»: %w", n.Name, err)
		}
	}
	for _, c := range n.Children {
		if err := w.writeNode(c, depth+1); err != nil {
			return err
		}
	}
	if err := w.encoder.EncodeToken(start.End()); err != nil {
		return fmt.Errorf("закрытие элемента «%s»: %w", n.Name, err)
	}
	return nil
}

func (w *xmlWriter) startElement(name string, attrs [][2]string, depth int) (xml.StartElement, error) {
	if depth > maxXMLDepth {
		return xml.StartElement{}, fmt.Errorf("глубина XML превышает предел %d", maxXMLDepth)
	}
	w.nodes++
	if w.nodes > maxXMLNodes {
		return xml.StartElement{}, fmt.Errorf("число элементов XML превышает предел %d", maxXMLNodes)
	}
	if err := validateXMLName(name); err != nil {
		return xml.StartElement{}, fmt.Errorf("недопустимое имя элемента «%s»: %w", name, err)
	}

	start := xml.StartElement{Name: xml.Name{Local: name}}
	seen := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		if err := validateXMLAttributeName(attr[0]); err != nil {
			return xml.StartElement{}, err
		}
		if _, exists := seen[attr[0]]; exists {
			return xml.StartElement{}, fmt.Errorf("повторяющийся атрибут «%s» элемента «%s»", attr[0], name)
		}
		seen[attr[0]] = struct{}{}
		if err := validateXMLCharacters(attr[1]); err != nil {
			return xml.StartElement{}, fmt.Errorf("значение атрибута «%s»: %w", attr[0], err)
		}
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: attr[0]}, Value: attr[1]})
	}
	if err := w.encoder.EncodeToken(start); err != nil {
		return xml.StartElement{}, fmt.Errorf("открытие элемента «%s»: %w", name, err)
	}
	return start, nil
}

type xmlTreeBudget struct {
	nodes int
}

// asXMLTreeNode распознаёт Структуру, полученную из ПрочитатьXML. Наличие
// поля Имя однозначно выбирает формат дерева; ошибки остальных полей не должны
// превращать значение в произвольную структуру.
func asXMLTreeNode(v any, depth int, budget *xmlTreeBudget) (*xmlNode, bool, error) {
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return nil, false, nil
	}
	nameVal, hasName := xmlStructField(s, xmlFieldName)
	if !hasName {
		return nil, false, nil
	}
	if depth > maxXMLDepth {
		return nil, true, fmt.Errorf("глубина XML превышает предел %d", maxXMLDepth)
	}
	budget.nodes++
	if budget.nodes > maxXMLNodes {
		return nil, true, fmt.Errorf("число элементов XML превышает предел %d", maxXMLNodes)
	}

	name, ok := nameVal.(string)
	if !ok {
		return nil, true, fmt.Errorf("поле «%s» узла XML должно быть строкой", xmlFieldName)
	}
	if err := validateXMLName(name); err != nil {
		return nil, true, fmt.Errorf("недопустимое имя элемента «%s»: %w", name, err)
	}

	allowedFields := map[string]struct{}{
		strings.ToLower(xmlFieldName):     {},
		strings.ToLower(xmlFieldAttrs):    {},
		strings.ToLower(xmlFieldText):     {},
		strings.ToLower(xmlFieldChildren): {},
	}
	for _, field := range s.Fields() {
		if _, allowed := allowedFields[strings.ToLower(field)]; !allowed {
			return nil, true, fmt.Errorf("неизвестное поле «%s» узла XML «%s»", field, name)
		}
	}

	node := &xmlNode{Name: name}
	if attrsVal, exists := xmlStructField(s, xmlFieldAttrs); exists && attrsVal != nil {
		attrs, ok := attrsVal.(*Map)
		if !ok || attrs == nil {
			return nil, true, fmt.Errorf("поле «%s» узла XML должно быть Соответствием", xmlFieldAttrs)
		}
		if len(attrs.keys) != len(attrs.vals) {
			return nil, true, fmt.Errorf("поле «%s» узла XML повреждено", xmlFieldAttrs)
		}
		seen := make(map[string]struct{}, len(attrs.keys))
		for i, keyVal := range attrs.keys {
			key, ok := keyVal.(string)
			if !ok {
				return nil, true, fmt.Errorf("имя атрибута узла XML должно быть строкой, получено %T", keyVal)
			}
			if err := validateXMLAttributeName(key); err != nil {
				return nil, true, err
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, true, fmt.Errorf("повторяющийся атрибут «%s» элемента «%s»", key, name)
			}
			seen[key] = struct{}{}
			value, ok := attrs.vals[i].(string)
			if !ok {
				return nil, true, fmt.Errorf("значение атрибута «%s» должно быть строкой", key)
			}
			if err := validateXMLCharacters(value); err != nil {
				return nil, true, fmt.Errorf("значение атрибута «%s»: %w", key, err)
			}
			node.Attrs = append(node.Attrs, [2]string{key, value})
		}
	}
	if textVal, exists := xmlStructField(s, xmlFieldText); exists && textVal != nil {
		text, ok := textVal.(string)
		if !ok {
			return nil, true, fmt.Errorf("поле «%s» узла XML должно быть строкой", xmlFieldText)
		}
		if err := validateXMLCharacters(text); err != nil {
			return nil, true, fmt.Errorf("текст элемента «%s»: %w", name, err)
		}
		node.Text = text
	}
	if childrenVal, exists := xmlStructField(s, xmlFieldChildren); exists && childrenVal != nil {
		children, ok := childrenVal.(*Array)
		if !ok || children == nil {
			return nil, true, fmt.Errorf("поле «%s» узла XML должно быть Массивом", xmlFieldChildren)
		}
		for i, childVal := range children.Iterate() {
			child, isTree, err := asXMLTreeNode(childVal, depth+1, budget)
			if err != nil {
				return nil, true, fmt.Errorf("%s[%d]: %w", xmlFieldChildren, i, err)
			}
			if !isTree {
				return nil, true, fmt.Errorf("%s[%d] не является узлом XML с полем «%s»", xmlFieldChildren, i, xmlFieldName)
			}
			node.Children = append(node.Children, child)
		}
	}
	if len(node.Children) > 0 && node.Text != "" {
		return nil, true, fmt.Errorf("смешанное содержимое элемента «%s» не поддерживается", name)
	}
	return node, true, nil
}

func xmlStructField(s *Struct, name string) (any, bool) {
	v, ok := s.vals[strings.ToLower(name)]
	return v, ok
}

func validateXMLAttributeName(name string) error {
	if name == "xmlns" {
		return fmt.Errorf("пространства имён XML не поддерживаются (атрибут xmlns)")
	}
	if err := validateXMLName(name); err != nil {
		return fmt.Errorf("недопустимое имя атрибута «%s»: %w", name, err)
	}
	return nil
}

func validateXMLName(name string) error {
	if name == "" {
		return fmt.Errorf("имя пусто")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("имя содержит некорректную UTF-8 последовательность")
	}
	for i, r := range []rune(name) {
		if r == ':' {
			return fmt.Errorf("пространства имён и символ ':' не поддерживаются")
		}
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return fmt.Errorf("первый символ должен быть буквой или '_'")
			}
			continue
		}
		if r != '_' && r != '-' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
			return fmt.Errorf("символ %q недопустим", r)
		}
	}
	return nil
}

func validateXMLCharacters(text string) error {
	if pos := firstDisallowedXMLCharacter(text); pos != 0 {
		return fmt.Errorf("недопустимый символ XML в позиции %d", pos)
	}
	return nil
}

func isXMLWhitespaceOnly(text string) bool {
	for _, r := range text {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			return false
		}
	}
	return true
}

func builtinXMLString(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return "", nil
	}
	text, err := xmlStringOf(args[0])
	if err != nil {
		panic(userError{Msg: "XMLСтрока: " + err.Error()})
	}
	return text, nil
}

// xmlStringOf — XML-представление примитива: числа без экспоненты, даты по
// ISO 8601, булево как true/false (а не «Да»/«Нет»).
func xmlStringOf(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case time.Time:
		return x.Format(time.RFC3339Nano), nil
	case decimal.Decimal:
		return safeXMLDecimalString(x)
	case *decimal.Decimal:
		if x == nil {
			return "", nil
		}
		return safeXMLDecimalString(*x)
	case int64:
		return strconv.FormatInt(x, 10), nil
	case int:
		return strconv.Itoa(x), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("число должно быть конечным")
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func safeXMLDecimalString(value decimal.Decimal) (string, error) {
	if value.IsZero() {
		return "0", nil
	}

	digits := int64(value.NumDigits())
	if digits <= 0 || digits > maxXMLDecimalDigits {
		return "", fmt.Errorf("decimal содержит больше %d цифр", maxXMLDecimalDigits)
	}

	signLength := int64(0)
	if value.IsNegative() {
		signLength = 1
	}
	exponent := int64(value.Exponent())
	var outputLength int64
	if exponent >= 0 {
		outputLength = signLength + digits + exponent
	} else {
		fractionalPlaces := -exponent
		if fractionalPlaces < digits {
			// Цифры, десятичная точка и, возможно, знак.
			outputLength = signLength + digits + 1
		} else {
			// 0., ведущие нули дробной части, цифры коэффициента и знак.
			outputLength = signLength + 2 + fractionalPlaces
		}
	}
	if outputLength > maxXMLDecimalOutputLength {
		return "", fmt.Errorf("decimal требует больше %d символов при записи", maxXMLDecimalOutputLength)
	}

	// Decimal.String разворачивает exponent в нули. Вызывать его безопасно
	// только после проверки верхней оценки результата выше.
	result := value.String()
	if len(result) > maxXMLDecimalOutputLength {
		return "", fmt.Errorf("decimal требует больше %d символов при записи", maxXMLDecimalOutputLength)
	}
	return result, nil
}

func parseXSDDecimal(text string) (decimal.Decimal, error) {
	text = strings.Trim(text, " \t\r\n")
	if text == "" {
		return decimal.Decimal{}, fmt.Errorf("пустое значение")
	}
	if len(text) > maxXMLDecimalLexicalLength {
		return decimal.Decimal{}, fmt.Errorf("лексическая запись длиннее %d символов", maxXMLDecimalLexicalLength)
	}

	i := 0
	if text[i] == '+' || text[i] == '-' {
		i++
		if i == len(text) {
			return decimal.Decimal{}, fmt.Errorf("после знака ожидаются цифры")
		}
	}
	digitCount := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		digitCount++
		i++
	}
	if i < len(text) && text[i] == '.' {
		i++
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			digitCount++
			i++
		}
	}
	if digitCount == 0 {
		return decimal.Decimal{}, fmt.Errorf("ожидается хотя бы одна цифра")
	}
	if i != len(text) {
		return decimal.Decimal{}, fmt.Errorf("допустимы только знак, цифры и десятичная точка без экспоненты")
	}
	if digitCount > maxXMLDecimalDigits {
		return decimal.Decimal{}, fmt.Errorf("decimal содержит больше %d цифр", maxXMLDecimalDigits)
	}

	value, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("некорректная запись decimal")
	}
	if _, err := safeXMLDecimalString(value); err != nil {
		return decimal.Decimal{}, err
	}
	return value, nil
}

// builtinXMLTypeOf — имя XSD-типа значения. Дополняет пару XMLСтрока/XMLЗначение:
// сериализуя значение, обычно надо сообщить приёмнику и его тип.
func builtinXMLTypeOf(args []any, file string, line int) (any, error) {
	if len(args) == 0 || args[0] == nil {
		return "", nil
	}
	switch value := args[0].(type) {
	case string:
		return "string", nil
	case bool:
		return "boolean", nil
	case time.Time:
		return "dateTime", nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			panic(userError{Msg: "XMLТипЗнч: число должно быть конечным"})
		}
		return "decimal", nil
	case *decimal.Decimal:
		if value == nil {
			return "", nil
		}
		return "decimal", nil
	case decimal.Decimal, int64, int:
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
	rawText := strArg(args, 1)
	text := strings.TrimSpace(rawText)
	switch typeName {
	case "строка", "string":
		return rawText, nil
	case "число", "number", "decimal":
		d, err := parseXSDDecimal(rawText)
		if err != nil {
			panic(userError{Msg: "XMLЗначение: некорректное decimal: " + err.Error()})
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
	case "дата", "date", "datetime":
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "20060102150405", "20060102"} {
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
	return int64(firstDisallowedXMLCharacter(strArg(args, 0))), nil
}

// firstDisallowedXMLCharacter возвращает 1-based позицию в символах. Отдельная
// проверка RuneError с размером 1 отличает повреждённую UTF-8 от допустимого
// литерального символа U+FFFD.
func firstDisallowedXMLCharacter(text string) int {
	position := 0
	for len(text) > 0 {
		position++
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 1 {
			return position
		}
		if !isAllowedXMLRune(r) {
			return position
		}
		text = text[size:]
	}
	return 0
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
