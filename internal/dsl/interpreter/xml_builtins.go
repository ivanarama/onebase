package interpreter

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
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
// смешанное содержимое, комментарии и инструкции обработки. Эти конструкции
// отклоняются, чтобы чтение никогда не теряло данные молча.
// Имена тегов живут в ЗНАЧЕНИИ поля Имя, а не в имени поля Структуры, — поэтому
// их регистр сохраняется: DSL регистронезависим и Struct.Set приводит имена полей
// к нижнему регистру.
//
// Пространства имён сохраняются ЛЕКСИЧЕСКИ: в поле Имя лежит квалифицированное
// имя как оно записано в документе («v8msg:Body»), а объявления xmlns остаются
// обычными атрибутами. Префиксы не разрешаются в URI, поэтому два документа,
// эквивалентных по namespace-модели, но записанных разными префиксами, дают
// разные деревья. Взамен чтение и запись ничего не теряют и не переименовывают,
// а обмен 1С, где тип объекта передаётся атрибутом xsi:type, читается как есть.

const (
	xmlFieldName     = "Имя"
	xmlFieldAttrs    = "Атрибуты"
	xmlFieldText     = "Текст"
	xmlFieldChildren = "Элементы"

	// Неопределено не является XSD-типом, поэтому для замкнутой пары
	// XMLТипЗнч/XMLЗначение используется явное расширение OneBase.
	xmlUndefinedTypeName = "undefined"

	// Ограничения защищают рекурсивное преобразование в коллекции DSL и обратно
	// от переполнения стека и документов, создающих чрезмерное число объектов.
	maxXMLDepth                = 256
	maxXMLNodes                = 100_000
	maxXMLAttributesPerElement = 10_000
	maxXMLAttributesTotal      = 100_000
	maxXMLDocumentBytes        = 16 << 20
	maxXMLTextBytes            = 16 << 20

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

type xmlLexicalToken struct {
	end   bool
	name  string
	attrs []string
}

func builtinReadXML(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		panic(userError{Msg: "ПрочитатьXML: ожидается 1 аргумент"})
	}
	text := requireXMLStringArg(args, 0, "ПрочитатьXML", "текст")
	node, err := parseXMLDocument(text)
	if err != nil {
		panic(userError{Msg: "ПрочитатьXML: " + err.Error()})
	}
	return xmlNodeToDSL(node), nil
}

// requireXMLStringArg не использует общий strArg: тот форматирует произвольные
// значения через fmt.Stringer. В частности, Decimal с огромной положительной
// экспонентой мог развернуться в гигантскую строку ещё до XML-лимитов.
func requireXMLStringArg(args []any, index int, functionName, parameterName string) string {
	if index >= len(args) {
		panic(userError{Msg: functionName + ": отсутствует аргумент «" + parameterName + "»"})
	}
	value, ok := args[index].(string)
	if !ok {
		panic(userError{Msg: fmt.Sprintf("%s: аргумент «%s» должен быть строкой, получено %T", functionName, parameterName, args[index])})
	}
	return value
}

func parseXMLDocument(text string) (*xmlNode, error) {
	// UTF-8 BOM допустим только один раз и только в самом начале документа.
	// Убираем его до подсчёта сырых span'ов токенов.
	decoderInput := strings.TrimPrefix(text, "\uFEFF")
	if len(decoderInput) > maxXMLDocumentBytes {
		return nil, fmt.Errorf("размер XML превышает предел %d байт", maxXMLDocumentBytes)
	}
	decoderInput, err := stripXMLDeclaration(decoderInput)
	if err != nil {
		return nil, err
	}
	preparedInput, lexicalTokens, err := prepareXMLDecoderInput(decoderInput)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(preparedInput))
	dec.Strict = true

	type frame struct {
		node *xmlNode
		text strings.Builder
	}

	var root *xmlNode
	var stack []*frame
	nodeCount := 0
	attributeCount := 0
	textBytes := 0
	lexicalIndex := 0
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
			if lexicalIndex >= len(lexicalTokens) || lexicalTokens[lexicalIndex].end {
				return nil, fmt.Errorf("несогласованная лексическая структура XML")
			}
			lexical := lexicalTokens[lexicalIndex]
			lexicalIndex++
			if len(t.Attr) != len(lexical.attrs) {
				return nil, fmt.Errorf("несогласованный список атрибутов XML")
			}
			t.Name = xml.Name{Local: lexical.name}
			for i := range t.Attr {
				t.Attr[i].Name = xml.Name{Local: lexical.attrs[i]}
			}
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
			if len(t.Attr) > maxXMLAttributesPerElement {
				return nil, fmt.Errorf("число атрибутов элемента «%s» превышает предел %d", name, maxXMLAttributesPerElement)
			}
			if attributeCount > maxXMLAttributesTotal-len(t.Attr) {
				return nil, fmt.Errorf("общее число атрибутов XML превышает предел %d", maxXMLAttributesTotal)
			}
			attributeCount += len(t.Attr)
			if err := addXMLTextBytes(&textBytes, len(name)); err != nil {
				return nil, err
			}
			node := &xmlNode{Name: name}
			seenAttrs := make(map[string]struct{}, len(t.Attr))
			for _, a := range t.Attr {
				attrName, err := decodedXMLName(a.Name, "атрибута")
				if err != nil {
					return nil, err
				}
				if _, exists := seenAttrs[attrName]; exists {
					return nil, fmt.Errorf("повторяющийся атрибут «%s» элемента «%s»", attrName, name)
				}
				if err := addXMLTextBytes(&textBytes, len(attrName), len(a.Value)); err != nil {
					return nil, err
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
			if lexicalIndex >= len(lexicalTokens) || !lexicalTokens[lexicalIndex].end {
				return nil, fmt.Errorf("несогласованная лексическая структура XML")
			}
			t.Name = xml.Name{Local: lexicalTokens[lexicalIndex].name}
			lexicalIndex++
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
			if err := addXMLTextBytes(&textBytes, len(t)); err != nil {
				return nil, err
			}
			stack[len(stack)-1].text.Write([]byte(t))
		case xml.Comment:
			// Пропускаем молча — см. prepareXMLDecoderInput (#962, Н6).
			continue
		case xml.Directive:
			// DTD остаётся запрещённой: именно она открывает XXE и
			// «billion laughs».
			return nil, fmt.Errorf("директивы XML не поддерживаются")
		case xml.ProcInst:
			continue
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("XML неожиданно завершён внутри элемента «%s»", stack[len(stack)-1].node.Name)
	}
	if root == nil {
		return nil, fmt.Errorf("документ не содержит корневого элемента")
	}
	if lexicalIndex != len(lexicalTokens) {
		return nil, fmt.Errorf("несогласованная лексическая структура XML")
	}
	return root, nil
}

// stripXMLDeclaration убирает объявление XML в начале документа. Оно описывает
// сам документ, а не дерево, и в модели не представлено: ЗаписатьXML его не
// восстанавливает. Остальные инструкции обработки по-прежнему отклоняются, в том
// числе «<?xmlfoo?>» — по грамматике это обычная PI, а не объявление.
//
// Объявление вырезается из строки до prepareXMLDecoderInput, поэтому смещения
// декодера и исходного текста остаются согласованными.
func stripXMLDeclaration(text string) (string, error) {
	const opening = "<?xml"
	if !strings.HasPrefix(text, opening) {
		return text, nil
	}
	if len(text) > len(opening) && !isXMLWhitespaceByte(text[len(opening)]) {
		return text, nil
	}
	end := strings.Index(text, "?>")
	if end < 0 {
		return "", fmt.Errorf("незавершённое объявление XML")
	}
	if err := validateXMLDeclaration(text[len(opening):end]); err != nil {
		return "", err
	}
	return text[end+len("?>"):], nil
}

// validateXMLDeclaration разбирает псевдоатрибуты version, encoding и standalone.
// Порядок фиксирован грамматикой (XML 1.0, раздел 2.8), повторы недопустимы.
// Кодировка проверяется отдельно: на вход ПрочитатьXML приходит уже готовая
// строка Go в UTF-8, поэтому объявленная windows-1251 означала бы, что документ
// прочитан не так, как записан, — молча принять это нельзя.
func validateXMLDeclaration(declaration string) error {
	pseudoAttributes := [...]string{"version", "encoding", "standalone"}
	next := 0
	rest := declaration
	for {
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		if trimmed == "" {
			break
		}
		if len(trimmed) == len(rest) {
			return fmt.Errorf("объявление XML: перед псевдоатрибутом ожидался пробел")
		}
		equals := strings.IndexByte(trimmed, '=')
		if equals < 0 {
			return fmt.Errorf("объявление XML: ожидался символ '='")
		}
		name := strings.TrimRight(trimmed[:equals], " \t\r\n")
		value := strings.TrimLeft(trimmed[equals+1:], " \t\r\n")
		if value == "" || value[0] != '"' && value[0] != '\'' {
			return fmt.Errorf("объявление XML: значение «%s» должно быть заключено в кавычки", name)
		}
		closing := strings.IndexByte(value[1:], value[0])
		if closing < 0 {
			return fmt.Errorf("объявление XML: незавершённое значение «%s»", name)
		}
		index := -1
		for i := next; i < len(pseudoAttributes); i++ {
			if pseudoAttributes[i] == name {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("объявление XML: неизвестный, повторный или переставленный псевдоатрибут «%s»", name)
		}
		if index > 0 && next == 0 {
			return fmt.Errorf("объявление XML: отсутствует обязательный псевдоатрибут version")
		}
		next = index + 1

		content := value[1 : 1+closing]
		switch name {
		case "version":
			if content != "1.0" {
				return fmt.Errorf("версия XML «%s» не поддерживается, ожидается 1.0", content)
			}
		case "encoding":
			if !strings.EqualFold(content, "utf-8") {
				return fmt.Errorf("кодировка XML «%s» не поддерживается, ожидается UTF-8", content)
			}
		case "standalone":
			if content != "yes" && content != "no" {
				return fmt.Errorf("объявление XML: standalone должно быть yes или no")
			}
		}
		rest = value[1+closing+1:]
	}
	if next == 0 {
		return fmt.Errorf("объявление XML: отсутствует обязательный псевдоатрибут version")
	}
	return nil
}

// xmlProcInstTarget возвращает имя инструкции обработки — всё до первого
// пробельного символа.
func xmlProcInstTarget(body string) string {
	if idx := strings.IndexAny(body, xmlWhitespaceChars); idx >= 0 {
		return body[:idx]
	}
	return body
}

// xmlWhitespaceChars — пробельные символы XML (пробел, табуляция, CR, LF).
const xmlWhitespaceChars = " \t\r\n"

// prepareXMLDecoderInput обходит ограничение encoding/xml: стандартный decoder
// проверяет имена по устаревшим Unicode-категориям, а не по полным диапазонам
// XML 1.0 Fifth Edition. Валидные имена временно заменяются ASCII-именами той
// же байтовой длины; поэтому InputOffset по-прежнему указывает в исходную строку.
// Исходные имена возвращаются из lexicalTokens, а соответствие start/end
// проверяется до замены, так что одинаковые placeholders не ослабляют strictness.
func prepareXMLDecoderInput(text string) ([]byte, []xmlLexicalToken, error) {
	source := text
	prepared := []byte(text)
	lexicalTokens := make([]xmlLexicalToken, 0)
	stack := make([]string, 0, maxXMLDepth)
	nodeCount := 0
	attributeCount := 0

	for i := 0; i < len(source); {
		if source[i] != '<' {
			i++
			continue
		}
		if strings.HasPrefix(source[i:], "<!--") {
			// Комментарий пропускается целиком: ничего не расширяет и ничего
			// не объявляет, поэтому запрещать его незачем — а встречается он
			// в обычных выгрузках 1С, SOAP-ответах и вообще где угодно.
			// Раньше модуль, читающий чужой файл, падал на ровном месте
			// (#962, находка Н6). Отклонение DTD остаётся как было.
			end := strings.Index(source[i+len("<!--"):], "-->")
			if end < 0 {
				return nil, nil, fmt.Errorf("незавершённый комментарий XML")
			}
			i += len("<!--") + end + len("-->")
			continue
		}
		if strings.HasPrefix(source[i:], "<![CDATA[") {
			end := strings.Index(source[i+len("<![CDATA["):], "]]>")
			if end < 0 {
				return nil, nil, fmt.Errorf("незавершённая секция CDATA")
			}
			i += len("<![CDATA[") + end + len("]]>")
			continue
		}
		if strings.HasPrefix(source[i:], "<!") {
			return nil, nil, fmt.Errorf("директивы XML не поддерживаются")
		}
		if strings.HasPrefix(source[i:], "<?") {
			// Инструкция обработки (<?xml-stylesheet …?>) тоже пропускается:
			// на содержимое документа она не влияет. Объявление <?xml …?>
			// снимается раньше, в stripXMLDeclaration.
			end := strings.Index(source[i+len("<?"):], "?>")
			if end < 0 {
				return nil, nil, fmt.Errorf("незавершённая инструкция обработки XML")
			}
			body := source[i+len("<?") : i+len("<?")+end]
			// Цель «xml» зарезервирована: это объявление документа, и вне
			// начала файла оно означает битый XML, а не безобидную инструкцию.
			if strings.EqualFold(xmlProcInstTarget(body), "xml") {
				return nil, nil, fmt.Errorf("объявление XML допустимо только в начале документа")
			}
			i += len("<?") + end + len("?>")
			continue
		}

		if i+1 < len(source) && source[i+1] == '/' {
			nameStart := i + 2
			nameEnd := scanXMLElementNameEnd(source, nameStart, true)
			if nameEnd == nameStart {
				return nil, nil, fmt.Errorf("закрывающий тег не содержит имени")
			}
			name := text[nameStart:nameEnd]
			if err := validateXMLName(name); err != nil {
				return nil, nil, fmt.Errorf("недопустимое имя закрывающего элемента: %w", err)
			}
			replaceXMLNameWithPlaceholder(prepared, nameStart, nameEnd)
			cursor := skipXMLWhitespaceBytes(source, nameEnd)
			if cursor >= len(source) || source[cursor] != '>' {
				return nil, nil, fmt.Errorf("после имени закрывающего тега ожидался символ '>'")
			}
			if len(stack) == 0 {
				return nil, nil, fmt.Errorf("закрывающий тег «%s» не имеет открывающего", name)
			}
			openName := stack[len(stack)-1]
			if openName != name {
				return nil, nil, fmt.Errorf("закрывающий тег «%s» не соответствует «%s»", name, openName)
			}
			stack = stack[:len(stack)-1]
			lexicalTokens = append(lexicalTokens, xmlLexicalToken{end: true, name: name})
			i = cursor + 1
			continue
		}

		nameStart := i + 1
		nameEnd := scanXMLElementNameEnd(source, nameStart, false)
		if nameEnd == nameStart {
			return nil, nil, fmt.Errorf("открывающий тег не содержит имени")
		}
		name := text[nameStart:nameEnd]
		if err := validateXMLName(name); err != nil {
			return nil, nil, fmt.Errorf("недопустимое имя открывающего элемента: %w", err)
		}
		nodeCount++
		if nodeCount > maxXMLNodes {
			return nil, nil, fmt.Errorf("число элементов XML превышает предел %d", maxXMLNodes)
		}
		if len(stack)+1 > maxXMLDepth {
			return nil, nil, fmt.Errorf("глубина XML превышает предел %d", maxXMLDepth)
		}
		replaceXMLNameWithPlaceholder(prepared, nameStart, nameEnd)

		cursor := nameEnd
		attrs := make([]string, 0)
		for {
			beforeWhitespace := cursor
			cursor = skipXMLWhitespaceBytes(source, cursor)
			if cursor >= len(source) {
				return nil, nil, fmt.Errorf("незавершённый открывающий тег «%s»", name)
			}
			if source[cursor] == '>' {
				lexicalTokens = append(lexicalTokens, xmlLexicalToken{name: name, attrs: attrs})
				stack = append(stack, name)
				i = cursor + 1
				break
			}
			if source[cursor] == '/' && cursor+1 < len(source) && source[cursor+1] == '>' {
				lexicalTokens = append(lexicalTokens,
					xmlLexicalToken{name: name, attrs: attrs},
					xmlLexicalToken{end: true, name: name},
				)
				i = cursor + 2
				break
			}
			if cursor == beforeWhitespace {
				return nil, nil, fmt.Errorf("перед атрибутом элемента «%s» ожидался пробел", name)
			}

			attrStart := cursor
			attrEnd := scanXMLAttributeNameEnd(source, attrStart)
			if attrEnd == attrStart {
				return nil, nil, fmt.Errorf("элемент «%s»: ожидалось имя атрибута", name)
			}
			attrName := text[attrStart:attrEnd]
			if err := validateXMLAttributeName(attrName); err != nil {
				return nil, nil, err
			}
			if len(attrs) >= maxXMLAttributesPerElement {
				return nil, nil, fmt.Errorf("число атрибутов элемента «%s» превышает предел %d", name, maxXMLAttributesPerElement)
			}
			if attributeCount >= maxXMLAttributesTotal {
				return nil, nil, fmt.Errorf("общее число атрибутов XML превышает предел %d", maxXMLAttributesTotal)
			}
			attributeCount++
			attrs = append(attrs, attrName)
			replaceXMLNameWithPlaceholder(prepared, attrStart, attrEnd)

			cursor = skipXMLWhitespaceBytes(source, attrEnd)
			if cursor >= len(source) || source[cursor] != '=' {
				return nil, nil, fmt.Errorf("после имени атрибута ожидался символ '='")
			}
			cursor = skipXMLWhitespaceBytes(source, cursor+1)
			if cursor >= len(source) || source[cursor] != '\'' && source[cursor] != '"' {
				return nil, nil, fmt.Errorf("значение атрибута должно быть заключено в кавычки")
			}
			quote := source[cursor]
			cursor++
			for cursor < len(source) && source[cursor] != quote {
				cursor++
			}
			if cursor >= len(source) {
				return nil, nil, fmt.Errorf("незавершённое значение атрибута")
			}
			cursor++
		}
	}
	if len(stack) != 0 {
		return nil, nil, fmt.Errorf("XML неожиданно завершён внутри элемента «%s»", stack[len(stack)-1])
	}
	return prepared, lexicalTokens, nil
}

func scanXMLElementNameEnd(source string, start int, closing bool) int {
	end := start
	for end < len(source) {
		b := source[end]
		if isXMLWhitespaceByte(b) || b == '>' || !closing && b == '/' {
			break
		}
		end++
	}
	return end
}

func scanXMLAttributeNameEnd(source string, start int) int {
	end := start
	for end < len(source) {
		b := source[end]
		if isXMLWhitespaceByte(b) || b == '=' || b == '/' || b == '>' {
			break
		}
		end++
	}
	return end
}

func skipXMLWhitespaceBytes(source string, index int) int {
	for index < len(source) && isXMLWhitespaceByte(source[index]) {
		index++
	}
	return index
}

func isXMLWhitespaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func replaceXMLNameWithPlaceholder(prepared []byte, start, end int) {
	for i := start; i < end; i++ {
		prepared[i] = 'x'
	}
}

// decodedXMLName ожидает имя без Space: prepareXMLDecoderInput заменяет все
// имена ASCII-заглушками, поэтому encoding/xml не видит ни двоеточий, ни
// объявлений xmlns и не выполняет разрешение префиксов. Проверка Space оставлена
// как страховка на случай изменения этой подготовки.
func decodedXMLName(name xml.Name, kind string) (string, error) {
	if name.Space != "" {
		return "", fmt.Errorf("неожиданное разрешение пространства имён XML (%s «%s»)", kind, name.Local)
	}
	if err := validateXMLName(name.Local); err != nil {
		return "", fmt.Errorf("недопустимое имя %s «%s»: %w", kind, name.Local, err)
	}
	return name.Local, nil
}

func xmlNodeToDSL(n *xmlNode) any {
	s := &Struct{vals: map[string]any{}}
	s.Set(xmlFieldName, n.Name)

	attrs := &Map{
		keys: make([]any, len(n.Attrs)),
		vals: make([]any, len(n.Attrs)),
	}
	for i, kv := range n.Attrs {
		attrs.keys[i] = kv[0]
		attrs.vals[i] = kv[1]
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
	rootNameBytes := 0
	if err := addXMLTextBytes(&rootNameBytes, len(rootName)); err != nil {
		panic(userError{Msg: "ЗаписатьXML: " + err.Error()})
	}
	if err := validateXMLName(rootName); err != nil {
		panic(userError{Msg: "ЗаписатьXML: недопустимое имя корня «" + rootName + "»: " + err.Error()})
	}

	var sb strings.Builder
	documentWriter := &xmlDocumentWriter{dst: &sb}
	w := &xmlWriter{encoder: xml.NewEncoder(documentWriter)}
	if err := w.writeValue(rootName, args[0], 1); err != nil {
		panic(userError{Msg: "ЗаписатьXML: " + err.Error()})
	}
	if err := w.encoder.Flush(); err != nil {
		panic(userError{Msg: "ЗаписатьXML: " + err.Error()})
	}
	return sb.String(), nil
}

// xmlDocumentWriter ограничивает именно готовую XML-строку после экранирования
// и добавления разметки. Лимита логического текста недостаточно: например,
// каждый символ '&' кодировщик разворачивает в пять байт "&amp;".
type xmlDocumentWriter struct {
	dst     *strings.Builder
	written int
}

func (w *xmlDocumentWriter) Write(p []byte) (int, error) {
	if w.written < 0 || w.written > maxXMLDocumentBytes || len(p) > maxXMLDocumentBytes-w.written {
		return 0, fmt.Errorf("размер XML превышает предел %d байт", maxXMLDocumentBytes)
	}
	n, err := w.dst.Write(p)
	w.written += n
	return n, err
}

type xmlWriter struct {
	encoder    *xml.Encoder
	nodes      int
	attributes int
	textBytes  int
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
		if len(x.keys) != len(x.vals) {
			return fmt.Errorf("соответствие повреждено: число ключей и значений различается")
		}
		for i, k := range x.keys {
			key, ok := k.(string)
			if !ok {
				return fmt.Errorf("имя элемента из ключа Соответствия должно быть строкой, получено %T", k)
			}
			if err := w.writeValue(key, x.vals[i], depth+1); err != nil {
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
		if err := addXMLTextBytes(&w.textBytes, len(text)); err != nil {
			return err
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
		if err := addXMLTextBytes(&w.textBytes, len(n.Text)); err != nil {
			return err
		}
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
	// Бюджет проверяется до любой линейной валидации. Это особенно важно для
	// разделяемых DSL-узлов: одна большая строка имени может встречаться в дереве
	// много раз, но суммарная работа всё равно остаётся ограниченной 16 МиБ.
	if err := addXMLTextBytes(&w.textBytes, len(name)); err != nil {
		return xml.StartElement{}, err
	}
	if len(attrs) > maxXMLAttributesPerElement {
		return xml.StartElement{}, fmt.Errorf("число атрибутов элемента «%s» превышает предел %d", name, maxXMLAttributesPerElement)
	}
	if w.attributes > maxXMLAttributesTotal-len(attrs) {
		return xml.StartElement{}, fmt.Errorf("общее число атрибутов XML превышает предел %d", maxXMLAttributesTotal)
	}
	w.attributes += len(attrs)
	// Сначала учитываем все строки элемента. Валидация и заполнение xml.Attr
	// начинаются только после того, как совокупный budget гарантированно пройден.
	for _, attr := range attrs {
		if err := addXMLTextBytes(&w.textBytes, len(attr[0]), len(attr[1])); err != nil {
			return xml.StartElement{}, err
		}
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
	nodes                   int
	attributes              int
	textBytes               int
	validatedNames          map[string]error
	validatedAttributeNames map[string]error
	validatedCharacters     map[string]error
}

// validateCached не сканирует повторно одну и ту же неизменяемую строку в
// разделяемых поддеревьях. Byte-budget по-прежнему учитывается до вызова этого
// метода для каждого вхождения, поэтому кеш не ослабляет ограничения ресурсов.
func validateCached(cache *map[string]error, value string, validate func(string) error) error {
	// Для коротких строк проверка дешевле записи в map. Порог также ограничивает
	// число записей кеша совокупным text-budget примерно четырьмя тысячами.
	const minCacheBytes = 4 << 10
	if len(value) < minCacheBytes {
		return validate(value)
	}
	if *cache == nil {
		*cache = make(map[string]error)
	}
	if err, ok := (*cache)[value]; ok {
		return err
	}
	err := validate(value)
	(*cache)[value] = err
	return err
}

func (b *xmlTreeBudget) validateName(value string) error {
	return validateCached(&b.validatedNames, value, validateXMLName)
}

func (b *xmlTreeBudget) validateAttributeName(value string) error {
	return validateCached(&b.validatedAttributeNames, value, validateXMLAttributeName)
}

func (b *xmlTreeBudget) validateCharacters(value string) error {
	return validateCached(&b.validatedCharacters, value, validateXMLCharacters)
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
	if err := addXMLTextBytes(&budget.textBytes, len(name)); err != nil {
		return nil, true, err
	}
	if err := budget.validateName(name); err != nil {
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
		if len(attrs.keys) > maxXMLAttributesPerElement {
			return nil, true, fmt.Errorf("число атрибутов элемента «%s» превышает предел %d", name, maxXMLAttributesPerElement)
		}
		// Проверяем общий предел до копирования атрибутов в xmlNode. Один и тот
		// же узел DSL может многократно встречаться в Элементы; без этой проверки
		// дешёвое дерево с разделяемым поддеревом раздувалось до гигантской копии ещё до
		// аналогичной проверки в xmlWriter.startElement.
		if budget.attributes > maxXMLAttributesTotal-len(attrs.keys) {
			return nil, true, fmt.Errorf("общее число атрибутов XML превышает предел %d", maxXMLAttributesTotal)
		}
		budget.attributes += len(attrs.keys)
		seen := make(map[string]struct{}, len(attrs.keys))
		for i, keyVal := range attrs.keys {
			key, ok := keyVal.(string)
			if !ok {
				return nil, true, fmt.Errorf("имя атрибута узла XML должно быть строкой, получено %T", keyVal)
			}
			if err := addXMLTextBytes(&budget.textBytes, len(key)); err != nil {
				return nil, true, err
			}
			value, ok := attrs.vals[i].(string)
			if !ok {
				return nil, true, fmt.Errorf("значение атрибута должно быть строкой, получено %T", attrs.vals[i])
			}
			if err := addXMLTextBytes(&budget.textBytes, len(value)); err != nil {
				return nil, true, err
			}
			if err := budget.validateAttributeName(key); err != nil {
				return nil, true, err
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, true, fmt.Errorf("повторяющийся атрибут «%s» элемента «%s»", key, name)
			}
			seen[key] = struct{}{}
			if err := budget.validateCharacters(value); err != nil {
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
		if err := addXMLTextBytes(&budget.textBytes, len(text)); err != nil {
			return nil, true, err
		}
		if err := budget.validateCharacters(text); err != nil {
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
	if err := validateXMLName(name); err != nil {
		return fmt.Errorf("недопустимое имя атрибута «%s»: %w", name, err)
	}
	return nil
}

// validateXMLName проверяет имя элемента или атрибута как QName (Namespaces in
// XML 1.0, раздел 3): либо NCName, либо «префикс:локальное имя». Одно двоеточие
// разрешено, потому что префикс — часть имени в лексической модели дерева.
func validateXMLName(name string) error {
	prefix, local, qualified := strings.Cut(name, ":")
	if !qualified {
		return validateXMLNCName(name)
	}
	if strings.Contains(local, ":") {
		return fmt.Errorf("имя содержит более одного символа ':'")
	}
	if err := validateXMLNCName(prefix); err != nil {
		return fmt.Errorf("префикс «%s»: %w", prefix, err)
	}
	if err := validateXMLNCName(local); err != nil {
		return fmt.Errorf("локальное имя «%s»: %w", local, err)
	}
	return nil
}

func validateXMLNCName(name string) error {
	if name == "" {
		return fmt.Errorf("имя пусто")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("имя содержит некорректную UTF-8 последовательность")
	}
	first := true
	for _, r := range name {
		if first {
			first = false
			if !isXMLNameStartRune(r) {
				return fmt.Errorf("первый символ не входит в XML NameStartChar")
			}
			continue
		}
		if !isXMLNameRune(r) {
			return fmt.Errorf("символ %q недопустим", r)
		}
	}
	return nil
}

// isXMLNameStartRune и isXMLNameRune реализуют productions XML 1.0 Fifth
// Edition для NCName. Двоеточие из NameStartChar исключено намеренно: его
// разбирает validateXMLName, чтобы префикс и локальное имя проверялись отдельно.
func isXMLNameStartRune(r rune) bool {
	switch {
	case r == '_':
		return true
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF:
		return true
	case r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF:
		return true
	case r >= 0x200C && r <= 0x200D, r >= 0x2070 && r <= 0x218F:
		return true
	case r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF:
		return true
	case r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD:
		return true
	case r >= 0x10000 && r <= 0xEFFFF:
		return true
	default:
		return false
	}
}

func isXMLNameRune(r rune) bool {
	if isXMLNameStartRune(r) {
		return true
	}
	switch {
	case r == '-' || r == '.' || r >= '0' && r <= '9' || r == 0xB7:
		return true
	case r >= 0x300 && r <= 0x36F, r >= 0x203F && r <= 0x2040:
		return true
	default:
		return false
	}
}

func validateXMLCharacters(text string) error {
	if pos := firstDisallowedXMLCharacter(text); pos != 0 {
		return fmt.Errorf("недопустимый символ XML в позиции %d", pos)
	}
	return nil
}

func addXMLTextBytes(total *int, sizes ...int) error {
	for _, size := range sizes {
		if size < 0 || *total > maxXMLTextBytes-size {
			return fmt.Errorf("объём текстовых данных XML превышает предел %d байт", maxXMLTextBytes)
		}
		*total += size
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
		return safeXMLDateTimeString(x)
	case *time.Time:
		if x == nil {
			return "", nil
		}
		return safeXMLDateTimeString(*x)
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
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float32:
		value := float64(x)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("число должно быть конечным")
		}
		return strconv.FormatFloat(value, 'f', -1, 32), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("число должно быть конечным")
		}
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		// Не вызываем fmt.Stringer: пользовательская реализация String может
		// выделить неограниченную память до проверки XML-бюджета.
		return "", fmt.Errorf("тип %T не поддерживается как XML-примитив", v)
	}
}

func validateXMLDateTime(value time.Time) error {
	year := value.Year()
	if year < 0 || year > 9999 {
		return fmt.Errorf("год даты должен быть в диапазоне 0000..9999")
	}
	_, offset := value.Zone()
	if offset%60 != 0 {
		return fmt.Errorf("смещение часового пояса должно быть кратно минуте")
	}
	const maxXSDTimezoneOffset = 14 * 60 * 60
	if offset < -maxXSDTimezoneOffset || offset > maxXSDTimezoneOffset {
		return fmt.Errorf("смещение часового пояса должно быть в диапазоне -14:00..+14:00")
	}
	return nil
}

func safeXMLDateTimeString(value time.Time) (string, error) {
	if err := validateXMLDateTime(value); err != nil {
		return "", err
	}
	return value.Format(time.RFC3339Nano), nil
}

// validateXSDDateTimeTimezoneOffset дополняет time.Parse строгой проверкой
// сырой лексической записи. Стандартный парсер Go принимает +24:00 и минуты 60,
// а XSD ограничивает смещение диапазоном -14:00..+14:00.
func validateXSDDateTimeTimezoneOffset(text string) error {
	if strings.HasSuffix(text, "Z") {
		return nil
	}
	if len(text) < 6 {
		return fmt.Errorf("смещение часового пояса должно иметь вид ±HH:MM")
	}
	offset := text[len(text)-6:]
	if (offset[0] != '+' && offset[0] != '-') || offset[3] != ':' ||
		offset[1] < '0' || offset[1] > '9' || offset[2] < '0' || offset[2] > '9' ||
		offset[4] < '0' || offset[4] > '9' || offset[5] < '0' || offset[5] > '9' {
		return fmt.Errorf("смещение часового пояса должно иметь вид ±HH:MM")
	}
	hour := int(offset[1]-'0')*10 + int(offset[2]-'0')
	minute := int(offset[4]-'0')*10 + int(offset[5]-'0')
	if minute > 59 {
		return fmt.Errorf("минуты смещения часового пояса должны быть в диапазоне 00..59")
	}
	if hour > 14 || hour == 14 && minute != 0 {
		return fmt.Errorf("смещение часового пояса должно быть в диапазоне -14:00..+14:00")
	}
	return nil
}

// validateXMLDateTimeFraction не даёт time.Parse молча усечь точность после
// девятой цифры. Формат time.Time хранит наносекунды, поэтому более точную XSD
// дробь безопаснее отклонить, чем принять с потерей данных. XSD использует '.'.
func validateXMLDateTimeFraction(text string) error {
	if len(text) <= len("2006-01-02T15:04:05") {
		return nil
	}
	if text[4] != '-' || text[7] != '-' || (text[10] != 'T' && text[10] != ' ') ||
		text[13] != ':' || text[16] != ':' {
		return nil
	}
	separator := text[19]
	if separator == ',' {
		return fmt.Errorf("дробная часть секунды должна отделяться символом '.'")
	}
	if separator != '.' {
		return nil
	}
	digits := 0
	for i := 20; i < len(text) && text[i] >= '0' && text[i] <= '9'; i++ {
		digits++
		if digits > 9 {
			return fmt.Errorf("дробная часть секунды должна содержать не более 9 цифр")
		}
	}
	if digits == 0 {
		return fmt.Errorf("после десятичной точки ожидаются цифры")
	}
	return nil
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

// builtinXMLTypeOf — имя XSD-типа значения или явного расширения OneBase для
// Неопределено. Дополняет пару XMLСтрока/XMLЗначение: сериализуя значение,
// обычно надо сообщить приёмнику и его тип.
func builtinXMLTypeOf(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		panic(userError{Msg: "XMLТипЗнч: ожидается 1 аргумент"})
	}
	if args[0] == nil {
		return xmlUndefinedTypeName, nil
	}
	switch value := args[0].(type) {
	case string:
		return "string", nil
	case bool:
		return "boolean", nil
	case time.Time:
		if err := validateXMLDateTime(value); err != nil {
			panic(userError{Msg: "XMLТипЗнч: " + err.Error()})
		}
		return "dateTime", nil
	case *time.Time:
		if value == nil {
			return xmlUndefinedTypeName, nil
		}
		if err := validateXMLDateTime(*value); err != nil {
			panic(userError{Msg: "XMLТипЗнч: " + err.Error()})
		}
		return "dateTime", nil
	case float32:
		if number := float64(value); math.IsNaN(number) || math.IsInf(number, 0) {
			panic(userError{Msg: "XMLТипЗнч: число должно быть конечным"})
		}
		return "decimal", nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			panic(userError{Msg: "XMLТипЗнч: число должно быть конечным"})
		}
		return "decimal", nil
	case *decimal.Decimal:
		if value == nil {
			return xmlUndefinedTypeName, nil
		}
		if _, err := safeXMLDecimalString(*value); err != nil {
			panic(userError{Msg: "XMLТипЗнч: " + err.Error()})
		}
		return "decimal", nil
	case decimal.Decimal:
		if _, err := safeXMLDecimalString(value); err != nil {
			panic(userError{Msg: "XMLТипЗнч: " + err.Error()})
		}
		return "decimal", nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "decimal", nil
	default:
		panic(userError{Msg: fmt.Sprintf("XMLТипЗнч: тип %T не поддерживается как XML-примитив", value)})
	}
}

func builtinXMLValue(args []any, file string, line int) (any, error) {
	if len(args) < 2 {
		panic(userError{Msg: "XMLЗначение: ожидается 2 аргумента — имя типа и строка"})
	}
	rawTypeName := requireXMLStringArg(args, 0, "XMLЗначение", "имя типа")
	rawText := requireXMLStringArg(args, 1, "XMLЗначение", "текст")
	typeName := strings.ToLower(strings.TrimSpace(rawTypeName))
	text := strings.TrimSpace(rawText)
	switch typeName {
	case "неопределено", xmlUndefinedTypeName, "nil":
		if rawText != "" {
			panic(userError{Msg: "XMLЗначение: для типа undefined ожидается пустая строка"})
		}
		return nil, nil
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
		panic(userError{Msg: "XMLЗначение: некорректное булево значение"})
	case "дата", "date", "datetime":
		if err := validateXMLDateTimeFraction(text); err != nil {
			panic(userError{Msg: "XMLЗначение: некорректное dateTime: " + err.Error()})
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", "20060102150405", "20060102"} {
			if t, err := time.Parse(layout, text); err == nil {
				if layout == time.RFC3339Nano {
					if err := validateXSDDateTimeTimezoneOffset(text); err != nil {
						panic(userError{Msg: "XMLЗначение: некорректное dateTime: " + err.Error()})
					}
				}
				if err := validateXMLDateTime(t); err != nil {
					panic(userError{Msg: "XMLЗначение: некорректное dateTime: " + err.Error()})
				}
				return t, nil
			}
		}
		panic(userError{Msg: "XMLЗначение: некорректная дата"})
	default:
		panic(userError{Msg: "XMLЗначение: неизвестный тип (ожидается Неопределено, Строка, Число, Дата или Булево)"})
	}
}

func builtinFindDisallowedXMLChars(args []any, file string, line int) (any, error) {
	if len(args) == 0 {
		return int64(0), nil
	}
	return int64(firstDisallowedXMLCharacter(requireXMLStringArg(args, 0, "НайтиНедопустимыеСимволыXML", "текст"))), nil
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
