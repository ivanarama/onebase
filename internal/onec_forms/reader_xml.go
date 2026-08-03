package onec_forms

import (
	"encoding/xml"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"os"
	"strconv"
	"strings"
)

// ReadFormXML парсит Form.xml в IRForm. Возвращает «сырой» IR — без
// нормализации имён 1С → OneBase (это делает mapping_in.go).
//
// Стратегия: декодируем XML в обобщённое дерево xmlNode (Name + Attrs +
// Text + Children, с сохранением порядка), затем интерпретируем дерево.
// Это даёт устойчивость к произвольному XML и round-trip без потерь:
// нераспознанные узлы складываются в UnknownXML «как есть».
func ReadFormXML(path string) (*IRForm, []Warning, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer oblog.CloseQuiet("onec_forms", "файл", f)

	root, err := decodeXMLTree(f)
	if err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if root == nil {
		return nil, nil, fmt.Errorf("%s: пустой XML", path)
	}
	if root.Name.Local != "Form" {
		return nil, nil, fmt.Errorf("%s: корневой элемент %q, ожидается Form", path, root.Name.Local)
	}

	var warns Warnings
	form := parseFormNode(root, &warns)
	return form, []Warning(warns), nil
}

// xmlNode — простое DOM-представление XML. Используем своё дерево,
// потому что у нас «всеядный» парсер и сложно описать всю схему
// в типах для encoding/xml.
type xmlNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	Children []*xmlNode
}

// Attr возвращает значение атрибута по локальному имени.
func (n *xmlNode) Attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// Child возвращает первого ребёнка с указанным локальным именем.
func (n *xmlNode) Child(name string) *xmlNode {
	for _, c := range n.Children {
		if c.Name.Local == name {
			return c
		}
	}
	return nil
}

// Children-helpers: список детей с указанным именем, любого namespace.
func (n *xmlNode) ChildrenByName(name string) []*xmlNode {
	var out []*xmlNode
	for _, c := range n.Children {
		if c.Name.Local == name {
			out = append(out, c)
		}
	}
	return out
}

// decodeXMLTree читает поток XML в xmlNode-дерево.
func decodeXMLTree(r io.Reader) (*xmlNode, error) {
	dec := xml.NewDecoder(r)
	var stack []*xmlNode
	var root *xmlNode

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: t.Name, Attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) == 0 {
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				txt := strings.TrimSpace(string(t))
				if txt != "" {
					// если у узла уже есть дети — это текст между ними,
					// мы такие случаи в Form.xml не встречаем (значащего
					// смешанного содержимого нет); игнорируем.
					top := stack[len(stack)-1]
					if len(top.Children) == 0 {
						top.Text += txt
					}
				}
			}
		}
	}
	return root, nil
}

// parseFormNode превращает корневой <Form> в IRForm.
func parseFormNode(root *xmlNode, warns *Warnings) *IRForm {
	form := &IRForm{
		Version:        root.Attr("version"),
		VerticalScroll: childText(root, "VerticalScroll"),
		Events:         map[string]string{},
	}

	if v := childText(root, "AutoSaveDataInSettings"); v != "" {
		// 1С использует строки Use|Default|… — нам важно лишь "есть/нет".
		form.AutoSaveDataInSettings = !strings.EqualFold(v, "DontUse")
	}

	if cb := root.Child("AutoCommandBar"); cb != nil {
		form.AutoCommandBar = parseCommandBar(cb, warns)
	}

	if events := root.Child("Events"); events != nil {
		for _, e := range events.ChildrenByName("Event") {
			form.Events[e.Attr("name")] = strings.TrimSpace(e.Text)
		}
	}

	if children := root.Child("ChildItems"); children != nil {
		for _, c := range children.Children {
			if el := parseElement(c, warns); el != nil {
				form.Elements = append(form.Elements, el)
			}
		}
	}

	if attrs := root.Child("Attributes"); attrs != nil {
		for _, a := range attrs.ChildrenByName("Attribute") {
			form.Attributes = append(form.Attributes, parseAttribute(a, warns))
		}
	}

	if cmds := root.Child("Commands"); cmds != nil {
		for _, c := range cmds.ChildrenByName("Command") {
			form.Commands = append(form.Commands, parseCommand(c, warns))
		}
	}

	if params := root.Child("Parameters"); params != nil {
		for _, p := range params.ChildrenByName("Parameter") {
			form.Parameters = append(form.Parameters, parseParameter(p, warns))
		}
	}

	// Корневой OriginalID — для round-trip; в Form.xml корня обычно нет id,
	// но AutoCommandBar.id=-1 показывает что соглашения нет. Сохраним пусто.
	form.OriginalID = root.Attr("id")

	return form
}

// parseElement рекурсивно превращает узел ChildItems-дерева в IRElement.
// На этом этапе сохраняем имя тега «как есть» (например, "InputField" или
// "UsualGroup"); нормализация в OneBase-имена выполняется в mapping_in.go.
func parseElement(n *xmlNode, warns *Warnings) *IRElement {
	if n == nil {
		return nil
	}
	el := &IRElement{
		Name:       n.Attr("name"),
		OriginalID: n.Attr("id"),
		Kind:       n.Name.Local,
		Events:     map[string]string{},
		Props:      map[string]any{},
		Visible:    true,
		Enabled:    true,
	}

	for _, c := range n.Children {
		switch c.Name.Local {
		case "Title":
			el.Title = parseTitle(c)
		case "ToolTip":
			el.Hint = parseTitle(c).Get("ru")
		case "DataPath":
			el.DataPath = strings.TrimSpace(c.Text)
		case "Picture":
			el.Picture = parsePictureRef(c)
		case "ValuesPicture":
			el.Values = parsePictureRef(c)
		case "ReadOnly":
			el.ReadOnly = parseBool(c.Text)
		case "Visible":
			el.Visible = parseBool(c.Text)
		case "Enabled":
			el.Enabled = parseBool(c.Text)
		case "Required":
			el.Required = parseBool(c.Text)
		case "Width":
			el.Width = parseInt(c.Text)
		case "Height":
			el.Height = parseInt(c.Text)
		case "Mask":
			el.Mask = strings.TrimSpace(c.Text)
		case "HorizontalStretch", "VerticalStretch":
			// сохраняем как props.<name>; нужно для дизайна, не для смысла
			el.Props[c.Name.Local] = parseBool(c.Text)
		case "Events":
			for _, ev := range c.ChildrenByName("Event") {
				el.Events[ev.Attr("name")] = strings.TrimSpace(ev.Text)
			}
		case "ChildItems":
			for _, cc := range c.Children {
				if child := parseElement(cc, warns); child != nil {
					el.Children = append(el.Children, child)
				}
			}
		case "Type":
			// для Button: <Type>CommandBarButton</Type>; для других —
			// специфические типы. Сохраняем в Props["Type"].
			if t := strings.TrimSpace(c.Text); t != "" {
				el.Props["Type"] = t
			}
		case "CommandName":
			el.Props["CommandName"] = strings.TrimSpace(c.Text)
		case "Representation":
			el.Props["Representation"] = strings.TrimSpace(c.Text)
		case "Group":
			el.Props["Group"] = strings.TrimSpace(c.Text)
		case "Behavior":
			el.Props["Behavior"] = strings.TrimSpace(c.Text)
		case "ShowTitle":
			el.Props["ShowTitle"] = parseBool(c.Text)
		case "PagesRepresentation":
			el.Props["PagesRepresentation"] = strings.TrimSpace(c.Text)
		case "TitleLocation":
			el.Props["TitleLocation"] = strings.TrimSpace(c.Text)
		case "EditMode":
			el.Props["EditMode"] = strings.TrimSpace(c.Text)
		case "ChoiceFoldersAndItems":
			el.Props["ChoiceFoldersAndItems"] = strings.TrimSpace(c.Text)
		case "AutoInsertNewRow":
			el.Props["AutoInsertNewRow"] = parseBool(c.Text)
		case "HeightInTableRows":
			el.Props["HeightInTableRows"] = parseInt(c.Text)
		case "ContextMenu", "ExtendedTooltip":
			// служебные дочерние узлы 1С. Сохраняем как props со ссылкой
			// на их name/id — потом восстановим при экспорте.
			el.Props[c.Name.Local+"_name"] = c.Attr("name")
			el.Props[c.Name.Local+"_id"] = c.Attr("id")
		case "ChoiceList":
			// сохраняем сериализованный XML, обработка — на этапе расширения
			el.Props["ChoiceList_xml"] = serializeNode(c)
		default:
			// неизвестные узлы — сохраняем как сырой XML в UnknownXML.
			// На большом файле УТ11 это будут ДКС, Conditional Appearance, и т.п.
			el.UnknownXML = append(el.UnknownXML, []byte(serializeNode(c))...)
			el.UnknownXML = append(el.UnknownXML, '\n')
			warns.Add(Warning{
				Severity: SeverityInfo,
				Code:     W011_UnsupportedProp,
				Element:  el.Name,
				Field:    c.Name.Local,
				Message:  fmt.Sprintf("свойство <%s> сохранено в UnknownXML для round-trip", c.Name.Local),
			})
		}
	}
	return el
}

// parseTitle распаковывает <Title>/<ToolTip>/<…> с локализацией.
// Структура: <v8:item><v8:lang>ru</v8:lang><v8:content>…</v8:content></v8:item>.
func parseTitle(n *xmlNode) IRTitle {
	if n == nil {
		return nil
	}
	title := IRTitle{}
	for _, item := range n.ChildrenByName("item") {
		var lang, content string
		for _, c := range item.Children {
			switch c.Name.Local {
			case "lang":
				lang = strings.TrimSpace(c.Text)
			case "content":
				content = c.Text
			}
		}
		if lang != "" {
			title[lang] = content
		}
	}
	return title
}

// parsePictureRef разбирает <Picture> или <ValuesPicture>.
// Возможные формы:
//
//	<Picture><xr:Ref>StdPicture.X</xr:Ref></Picture>  → "stdpic:X"
//	<Picture>... data ...</Picture>                   → "embedded:" + raw
//
// Для второго случая используем плейсхолдер — реальные данные приходят
// через ресурсы из Items/. Пустые Picture (с одним только LoadTransparent)
// дают пустую строку.
func parsePictureRef(n *xmlNode) string {
	if ref := n.Child("Ref"); ref != nil {
		v := strings.TrimSpace(ref.Text)
		if strings.HasPrefix(v, "StdPicture.") {
			return "stdpic:" + strings.TrimPrefix(v, "StdPicture.")
		}
		return v
	}
	return ""
}

// parseAttribute разбирает <Attribute>.
func parseAttribute(n *xmlNode, warns *Warnings) *IRAttribute {
	a := &IRAttribute{
		Name:       n.Attr("name"),
		OriginalID: n.Attr("id"),
		Title:      parseTitle(n.Child("Title")),
	}
	if t := n.Child("Type"); t != nil {
		a.TypeRef, a.Length, a.Precision, a.AllowedLength = parseTypeNode(t, warns, a.Name)
	}
	if mainAttr := n.Child("MainAttribute"); mainAttr != nil && parseBool(mainAttr.Text) {
		a.MainAttribute = true
	}
	if fill := n.Child("FillingValue"); fill != nil {
		a.FillingValue = strings.TrimSpace(fill.Text)
	}
	// поле Save — список сохраняемых полей; флаг достаточно булевый.
	if n.Child("Save") != nil {
		a.Save = true
	}
	// <Columns> — обёртка для <Column> у ValueTable-реквизита.
	if cols := n.Child("Columns"); cols != nil {
		for _, c := range cols.ChildrenByName("Column") {
			a.Columns = append(a.Columns, parseAttributeColumn(c, warns))
		}
	}
	return a
}

// parseAttributeColumn разбирает колонку реквизита-таблицы.
func parseAttributeColumn(n *xmlNode, warns *Warnings) *IRAttributeColumn {
	col := &IRAttributeColumn{
		Name:       n.Attr("name"),
		OriginalID: n.Attr("id"),
		Title:      parseTitle(n.Child("Title")),
	}
	if t := n.Child("Type"); t != nil {
		col.TypeRef, col.Length, col.Precision, _ = parseTypeNode(t, warns, col.Name)
	}
	return col
}

// parseTypeNode разбирает <Type> с вложенными <v8:Type> + qualifiers.
// Возвращает (typeRef в нейтральной нотации, length, precision, allowedLength).
//
// При обнаружении нескольких <v8:Type> (композитный тип) эмитит W020 и
// возвращает первый из них как fallback.
func parseTypeNode(t *xmlNode, warns *Warnings, ownerName string) (string, int, int, string) {
	types := t.ChildrenByName("Type")
	if len(types) == 0 {
		warns.Add(Warning{Severity: SeverityWarn, Code: W022_UnknownType, Element: ownerName, Message: "элемент <Type> без вложенных <v8:Type>"})
		return "", 0, 0, ""
	}
	if len(types) > 1 {
		warns.Add(Warning{
			Severity: SeverityWarn, Code: W020_CompositeType, Element: ownerName,
			Message: fmt.Sprintf("композитный тип (%d вариантов) свернут к первому", len(types)),
			Suggest: "проверьте использование реквизита и при необходимости укажите явный тип",
		})
	}

	primaryType := strings.TrimSpace(types[0].Text)
	var length, precision int
	var allowed string

	if sq := t.Child("StringQualifiers"); sq != nil {
		length = parseInt(childText(sq, "Length"))
		allowed = childText(sq, "AllowedLength")
	}
	if nq := t.Child("NumberQualifiers"); nq != nil {
		length = parseInt(childText(nq, "Digits"))
		precision = parseInt(childText(nq, "FractionDigits"))
	}

	neutral := Type1CToOneBase(primaryType, length, precision, allowed)
	if neutral == primaryType && !strings.Contains(primaryType, "Ref") {
		warns.Add(Warning{Severity: SeverityInfo, Code: W022_UnknownType, Element: ownerName, Message: "неизвестный тип " + primaryType + " — оставлен без нормализации"})
	}
	return neutral, length, precision, allowed
}

// parseCommand разбирает <Command>.
func parseCommand(n *xmlNode, warns *Warnings) *IRCommand {
	cmd := &IRCommand{
		Name:       n.Attr("name"),
		OriginalID: n.Attr("id"),
		Title:      parseTitle(n.Child("Title")),
		Action:     childText(n, "Action"),
		Group:      childText(n, "Group"),
	}
	if pic := n.Child("Picture"); pic != nil {
		cmd.Picture = parsePictureRef(pic)
	}
	return cmd
}

// parseParameter разбирает <Parameter>.
func parseParameter(n *xmlNode, warns *Warnings) *IRParameter {
	p := &IRParameter{
		Name:         n.Attr("name"),
		OriginalID:   n.Attr("id"),
		KeyParameter: parseBool(childText(n, "KeyParameter")),
	}
	if t := n.Child("Type"); t != nil {
		p.TypeRef, _, _, _ = parseTypeNode(t, warns, p.Name)
	}
	return p
}

// parseCommandBar разбирает <AutoCommandBar> / <CommandBar>.
func parseCommandBar(n *xmlNode, warns *Warnings) *IRCommandBar {
	bar := &IRCommandBar{
		Name:       n.Attr("name"),
		OriginalID: n.Attr("id"),
		Visible:    true,
	}
	if children := n.Child("ChildItems"); children != nil {
		for _, c := range children.ChildrenByName("Button") {
			bar.Buttons = append(bar.Buttons, parseCommandBarButton(c))
		}
	}
	return bar
}

func parseCommandBarButton(n *xmlNode) *IRCommandBarButton {
	b := &IRCommandBarButton{
		Name:           n.Attr("name"),
		OriginalID:     n.Attr("id"),
		Title:          parseTitle(n.Child("Title")),
		CommandName:    childText(n, "CommandName"),
		Representation: childText(n, "Representation"),
	}
	if pic := n.Child("Picture"); pic != nil {
		b.Picture = parsePictureRef(pic)
	}
	return b
}

// childText возвращает trimmed-текст первого дочернего узла с указанным именем.
func childText(n *xmlNode, name string) string {
	c := n.Child(name)
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Text)
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "истина":
		return true
	}
	return false
}

func parseInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// serializeNode возвращает XML-сериализацию узла. Используется для
// сохранения unknown-узлов в UnknownXML и ChoiceList.
func serializeNode(n *xmlNode) string {
	var sb strings.Builder
	writeNode(&sb, n)
	return sb.String()
}

func writeNode(sb *strings.Builder, n *xmlNode) {
	sb.WriteByte('<')
	writeXMLName(sb, n.Name)
	for _, a := range n.Attrs {
		sb.WriteByte(' ')
		writeXMLName(sb, a.Name)
		sb.WriteString(`="`)
		// EscapeText пишет в bytes.Buffer — он не может вернуть ошибку
		// записи, поэтому проверять здесь нечего.
		_ = xml.EscapeText(sb, []byte(a.Value))
		sb.WriteByte('"')
	}
	if len(n.Children) == 0 && n.Text == "" {
		sb.WriteString("/>")
		return
	}
	sb.WriteByte('>')
	if n.Text != "" {
		// EscapeText пишет в bytes.Buffer — он не может вернуть ошибку
		// записи, поэтому проверять здесь нечего.
		_ = xml.EscapeText(sb, []byte(n.Text))
	}
	for _, c := range n.Children {
		writeNode(sb, c)
	}
	sb.WriteString("</")
	writeXMLName(sb, n.Name)
	sb.WriteByte('>')
}

func writeXMLName(sb *strings.Builder, name xml.Name) {
	// сохраняем неймспейс через namespace alias, если он был задан как
	// префикс в атрибутах сериализуемого фрагмента — иначе пишем local.
	// Полная поддержка namespaces возможна потом; для целей UnknownXML
	// достаточно сохранить локальное имя.
	if name.Space != "" {
		// Form.xml использует префиксы v8:, xr:, cfg: и т.д. — но
		// xml.Decoder заменяет их на URI namespace. Восстанавливаем
		// короткие префиксы по эвристике для самых частых.
		prefix := wellKnownPrefix(name.Space)
		if prefix != "" {
			sb.WriteString(prefix)
			sb.WriteByte(':')
		}
	}
	sb.WriteString(name.Local)
}

// wellKnownPrefix — обратный маппинг наиболее частых namespace URI
// в их короткие префиксы (как в исходном Form.xml).
func wellKnownPrefix(uri string) string {
	switch uri {
	case "http://v8.1c.ru/8.1/data/core":
		return "v8"
	case "http://v8.1c.ru/8.3/xcf/readable":
		return "xr"
	case "http://v8.1c.ru/8.1/data/enterprise/current-config":
		return "cfg"
	case "http://www.w3.org/2001/XMLSchema":
		return "xs"
	case "http://www.w3.org/2001/XMLSchema-instance":
		return "xsi"
	case "http://v8.1c.ru/8.2/managed-application/logform":
		return "lf"
	}
	return ""
}
