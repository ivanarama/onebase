package onec_forms

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// WriteFormXML сериализует IRForm в Form.xml формата управляемой формы
// Enterprise-системы (default namespace http://v8.1c.ru/8.3/xcf/logform,
// version="2.20"). Использует OriginalID если задан, иначе генерирует
// числовые id ≥ 1000 для round-trip-стабильности.
//
// До вызова рекомендуется выполнить NormalizeForExport(form): тогда
// имена элементов уже будут в 1С-канонической нотации (InputField,
// UsualGroup, Pages, ...), типы реквизитов с xs:/cfg:/v8: префиксами,
// события с английскими именами (OnOpen, OnChange).
func WriteFormXML(form *IRForm, dstPath string) error {
	if form == nil {
		return fmt.Errorf("WriteFormXML: form nil")
	}

	gen := &idGenerator{next: 1000}

	var buf strings.Builder
	buf.WriteString(xml.Header) // <?xml version="1.0" encoding="UTF-8"?>

	// Корневой <Form> с namespaces. Не выгружаем все 16 namespace'ов из
	// «эталонной» УТ11-формы — только те, что реально используем в выводе.
	version := form.Version
	if version == "" {
		version = "2.20"
	}
	fmt.Fprintf(&buf, `<Form xmlns="http://v8.1c.ru/8.3/xcf/logform" xmlns:v8="http://v8.1c.ru/8.1/data/core" xmlns:cfg="http://v8.1c.ru/8.1/data/enterprise/current-config" xmlns:xr="http://v8.1c.ru/8.3/xcf/readable" xmlns:xs="http://www.w3.org/2001/XMLSchema" version="%s">`, escapeAttr(version))
	buf.WriteByte('\n')

	if form.AutoSaveDataInSettings {
		buf.WriteString("\t<AutoSaveDataInSettings>Use</AutoSaveDataInSettings>\n")
	}
	if form.VerticalScroll != "" {
		fmt.Fprintf(&buf, "\t<VerticalScroll>%s</VerticalScroll>\n", escapeText(form.VerticalScroll))
	}

	if form.AutoCommandBar != nil {
		writeCommandBar(&buf, "\t", form.AutoCommandBar, gen)
	}

	if len(form.Events) > 0 {
		buf.WriteString("\t<Events>\n")
		for name, proc := range form.Events {
			fmt.Fprintf(&buf, "\t\t<Event name=\"%s\">%s</Event>\n", escapeAttr(name), escapeText(proc))
		}
		buf.WriteString("\t</Events>\n")
	}

	if len(form.Elements) > 0 {
		buf.WriteString("\t<ChildItems>\n")
		for _, el := range form.Elements {
			writeElement(&buf, "\t\t", el, gen)
		}
		buf.WriteString("\t</ChildItems>\n")
	}

	if len(form.Attributes) > 0 {
		buf.WriteString("\t<Attributes>\n")
		for _, a := range form.Attributes {
			writeAttribute(&buf, "\t\t", a, gen)
		}
		buf.WriteString("\t</Attributes>\n")
	}

	if len(form.Commands) > 0 {
		buf.WriteString("\t<Commands>\n")
		for _, c := range form.Commands {
			writeCommand(&buf, "\t\t", c, gen)
		}
		buf.WriteString("\t</Commands>\n")
	}

	if len(form.Parameters) > 0 {
		buf.WriteString("\t<Parameters>\n")
		for _, p := range form.Parameters {
			fmt.Fprintf(&buf, "\t\t<Parameter name=\"%s\" id=\"%s\">", escapeAttr(p.Name), escapeAttr(idOrGen(p.OriginalID, gen)))
			if p.TypeRef != "" {
				fmt.Fprintf(&buf, "<Type><v8:Type>%s</v8:Type></Type>", escapeText(p.TypeRef))
			}
			buf.WriteString("</Parameter>\n")
		}
		buf.WriteString("\t</Parameters>\n")
	} else {
		buf.WriteString("\t<Parameters/>\n")
	}

	buf.WriteString("</Form>\n")

	return os.WriteFile(dstPath, []byte(buf.String()), 0o644)
}

// idGenerator — раздаёт стабильные числовые id для узлов без OriginalID.
type idGenerator struct{ next int }

func (g *idGenerator) Next() string {
	g.next++
	return strconv.Itoa(g.next)
}

func idOrGen(orig string, g *idGenerator) string {
	if orig != "" {
		return orig
	}
	return g.Next()
}

// writeElement рекурсивно сериализует IRElement в XML.
// На входе el.Kind ожидается в 1С-форме (InputField, UsualGroup, …);
// если в нём остался OneBase-канон, writer пишет «как есть» — открытый XML
// не зайдёт в configurator 1С, но round-trip сохранится.
func writeElement(buf *strings.Builder, indent string, el *IRElement, gen *idGenerator) {
	tagName := el.Kind
	if tagName == "" {
		tagName = "InputField" // fallback — самый частый тип
	}

	fmt.Fprintf(buf, "%s<%s name=\"%s\" id=\"%s\">\n", indent, tagName, escapeAttr(el.Name), escapeAttr(idOrGen(el.OriginalID, gen)))

	inner := indent + "\t"
	writeTitle(buf, inner, "Title", el.Title)
	if el.Hint != "" {
		writeTitle(buf, inner, "ToolTip", IRTitle{"ru": el.Hint})
	}
	if el.DataPath != "" {
		fmt.Fprintf(buf, "%s<DataPath>%s</DataPath>\n", inner, escapeText(el.DataPath))
	}
	if el.Picture != "" {
		writePicture(buf, inner, "Picture", el.Picture)
	}
	if el.Values != "" {
		writePicture(buf, inner, "ValuesPicture", el.Values)
	}
	if el.ReadOnly {
		fmt.Fprintf(buf, "%s<ReadOnly>true</ReadOnly>\n", inner)
	}
	if el.Required {
		fmt.Fprintf(buf, "%s<Required>true</Required>\n", inner)
	}
	if el.Width > 0 {
		fmt.Fprintf(buf, "%s<Width>%d</Width>\n", inner, el.Width)
	}
	if el.Height > 0 {
		fmt.Fprintf(buf, "%s<Height>%d</Height>\n", inner, el.Height)
	}
	if el.Mask != "" {
		fmt.Fprintf(buf, "%s<Mask>%s</Mask>\n", inner, escapeText(el.Mask))
	}

	// Props: пишем известные подэлементы (Type, CommandName, Group, и т.д.)
	if el.Props != nil {
		writeKnownProps(buf, inner, el.Props)
	}

	if len(el.Events) > 0 {
		fmt.Fprintf(buf, "%s<Events>\n", inner)
		for name, proc := range el.Events {
			fmt.Fprintf(buf, "%s\t<Event name=\"%s\">%s</Event>\n", inner, escapeAttr(name), escapeText(proc))
		}
		fmt.Fprintf(buf, "%s</Events>\n", inner)
	}

	if len(el.Children) > 0 {
		fmt.Fprintf(buf, "%s<ChildItems>\n", inner)
		for _, c := range el.Children {
			writeElement(buf, inner+"\t", c, gen)
		}
		fmt.Fprintf(buf, "%s</ChildItems>\n", inner)
	}

	// Сохранённый сырой XML — вставляем как есть.
	if len(el.UnknownXML) > 0 {
		buf.WriteString(inner)
		buf.Write(el.UnknownXML)
		buf.WriteByte('\n')
	}

	fmt.Fprintf(buf, "%s</%s>\n", indent, tagName)
}

// writeKnownProps выводит известные «простые» props (Type, CommandName,
// Group, Behavior, …) как XML-элементы. Неизвестные props игнорируются —
// они либо появятся при следующем round-trip в UnknownXML, либо просто
// неактуальны для целевого формата.
func writeKnownProps(buf *strings.Builder, indent string, props map[string]any) {
	// Перечислим в фиксированном порядке для стабильного diff'а.
	keys := []string{
		"Type", "Representation", "CommandName",
		"Group", "Behavior", "ShowTitle",
		"PagesRepresentation", "TitleLocation", "EditMode",
		"ChoiceFoldersAndItems", "AutoInsertNewRow", "HeightInTableRows",
		"HorizontalStretch", "VerticalStretch",
	}
	for _, k := range keys {
		v, ok := props[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case bool:
			fmt.Fprintf(buf, "%s<%s>%t</%s>\n", indent, k, x, k)
		case int, int64:
			fmt.Fprintf(buf, "%s<%s>%v</%s>\n", indent, k, x, k)
		case string:
			if x == "" {
				continue
			}
			fmt.Fprintf(buf, "%s<%s>%s</%s>\n", indent, k, escapeText(x), k)
		default:
			fmt.Fprintf(buf, "%s<%s>%v</%s>\n", indent, k, x, k)
		}
	}
}

// writeTitle сериализует локализованный заголовок:
//
//	<Title>
//	  <v8:item><v8:lang>ru</v8:lang><v8:content>X</v8:content></v8:item>
//	</Title>
func writeTitle(buf *strings.Builder, indent, tagName string, t IRTitle) {
	if len(t) == 0 {
		return
	}
	// Пропускаем пустые
	any := false
	for _, v := range t {
		if v != "" {
			any = true
			break
		}
	}
	if !any {
		return
	}
	fmt.Fprintf(buf, "%s<%s>\n", indent, tagName)
	for lang, content := range t {
		if content == "" {
			continue
		}
		fmt.Fprintf(buf, "%s\t<v8:item><v8:lang>%s</v8:lang><v8:content>%s</v8:content></v8:item>\n",
			indent, escapeText(lang), escapeText(content))
	}
	fmt.Fprintf(buf, "%s</%s>\n", indent, tagName)
}

// writePicture сериализует ссылку на стандартную иконку (stdpic:X)
// или относительный путь.
func writePicture(buf *strings.Builder, indent, tagName, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(buf, "%s<%s>\n", indent, tagName)
	if strings.HasPrefix(value, "stdpic:") {
		stdName := strings.TrimPrefix(value, "stdpic:")
		fmt.Fprintf(buf, "%s\t<xr:Ref>StdPicture.%s</xr:Ref>\n", indent, escapeText(stdName))
	} else {
		// Бинарный ресурс — для него отдельно создаётся файл в Items/.
		// В Form.xml пишем заглушку <xr:Ref> с пустым значением (1С это
		// потерпит при условии что Items/ есть). В будущем — генерация
		// нормального blob-блока base64.
		fmt.Fprintf(buf, "%s\t<!-- ресурс: %s -->\n", indent, escapeText(value))
	}
	fmt.Fprintf(buf, "%s</%s>\n", indent, tagName)
}

// writeAttribute сериализует реквизит формы.
func writeAttribute(buf *strings.Builder, indent string, a *IRAttribute, gen *idGenerator) {
	fmt.Fprintf(buf, "%s<Attribute name=\"%s\" id=\"%s\">\n", indent, escapeAttr(a.Name), escapeAttr(idOrGen(a.OriginalID, gen)))
	inner := indent + "\t"
	writeTitle(buf, inner, "Title", a.Title)
	if a.TypeRef != "" {
		writeAttrType(buf, inner, a.TypeRef, a.Length, a.Precision, a.AllowedLength)
	}
	if a.MainAttribute {
		fmt.Fprintf(buf, "%s<MainAttribute>true</MainAttribute>\n", inner)
	}
	if a.FillingValue != "" {
		fmt.Fprintf(buf, "%s<FillingValue>%s</FillingValue>\n", inner, escapeText(a.FillingValue))
	}
	if a.Save {
		// Признак сохранения — пустой <Save/> для краткости. Реальные пути
		// к сохраняемым полям мы не восстанавливаем (теряем при импорте,
		// но обычно конфигуратор 1С обрабатывает пустой <Save/> корректно).
		fmt.Fprintf(buf, "%s<Save/>\n", inner)
	}
	if len(a.Columns) > 0 {
		fmt.Fprintf(buf, "%s<Columns>\n", inner)
		for _, c := range a.Columns {
			fmt.Fprintf(buf, "%s\t<Column name=\"%s\" id=\"%s\">\n", inner, escapeAttr(c.Name), escapeAttr(idOrGen(c.OriginalID, gen)))
			writeTitle(buf, inner+"\t\t", "Title", c.Title)
			if c.TypeRef != "" {
				writeAttrType(buf, inner+"\t\t", c.TypeRef, c.Length, c.Precision, "")
			}
			fmt.Fprintf(buf, "%s\t</Column>\n", inner)
		}
		fmt.Fprintf(buf, "%s</Columns>\n", inner)
	}
	fmt.Fprintf(buf, "%s</Attribute>\n", indent)
}

// writeAttrType пишет <Type><v8:Type>X</v8:Type><Qualifiers>...</></Type>.
// На вход — каноническое имя типа в 1С-формате (xs:string, cfg:CatalogRef.X, v8:ValueTable).
// Если префикса нет — добавляем по эвристике.
func writeAttrType(buf *strings.Builder, indent, typeRef string, length, precision int, allowed string) {
	fmt.Fprintf(buf, "%s<Type>\n", indent)
	primary := typeRef
	if !strings.HasPrefix(primary, "xs:") && !strings.HasPrefix(primary, "cfg:") && !strings.HasPrefix(primary, "v8:") {
		t, l, p, al := TypeOneBaseTo1C(primary)
		primary = t
		if length == 0 {
			length = l
		}
		if precision == 0 {
			precision = p
		}
		if allowed == "" {
			allowed = al
		}
	}
	fmt.Fprintf(buf, "%s\t<v8:Type>%s</v8:Type>\n", indent, escapeText(primary))
	if strings.HasSuffix(primary, "string") && length > 0 {
		fmt.Fprintf(buf, "%s\t<StringQualifiers>\n%s\t\t<Length>%d</Length>\n", indent, indent, length)
		if allowed != "" {
			fmt.Fprintf(buf, "%s\t\t<AllowedLength>%s</AllowedLength>\n", indent, escapeText(allowed))
		}
		fmt.Fprintf(buf, "%s\t</StringQualifiers>\n", indent)
	}
	if strings.HasSuffix(primary, "decimal") && length > 0 {
		fmt.Fprintf(buf, "%s\t<NumberQualifiers>\n%s\t\t<Digits>%d</Digits>\n", indent, indent, length)
		if precision > 0 {
			fmt.Fprintf(buf, "%s\t\t<FractionDigits>%d</FractionDigits>\n", indent, precision)
		}
		fmt.Fprintf(buf, "%s\t</NumberQualifiers>\n", indent)
	}
	fmt.Fprintf(buf, "%s</Type>\n", indent)
}

// writeCommand сериализует команду формы.
func writeCommand(buf *strings.Builder, indent string, c *IRCommand, gen *idGenerator) {
	fmt.Fprintf(buf, "%s<Command name=\"%s\" id=\"%s\">\n", indent, escapeAttr(c.Name), escapeAttr(idOrGen(c.OriginalID, gen)))
	inner := indent + "\t"
	writeTitle(buf, inner, "Title", c.Title)
	if c.Group != "" {
		fmt.Fprintf(buf, "%s<Group>%s</Group>\n", inner, escapeText(c.Group))
	}
	if c.Action != "" {
		fmt.Fprintf(buf, "%s<Action>%s</Action>\n", inner, escapeText(c.Action))
	}
	if c.Picture != "" {
		writePicture(buf, inner, "Picture", c.Picture)
	}
	fmt.Fprintf(buf, "%s</Command>\n", indent)
}

// writeCommandBar сериализует AutoCommandBar (или CommandBar).
func writeCommandBar(buf *strings.Builder, indent string, b *IRCommandBar, gen *idGenerator) {
	tag := "AutoCommandBar"
	fmt.Fprintf(buf, "%s<%s name=\"%s\" id=\"%s\">\n", indent, tag, escapeAttr(b.Name), escapeAttr(idOrGen(b.OriginalID, gen)))
	inner := indent + "\t"
	if len(b.Buttons) > 0 {
		fmt.Fprintf(buf, "%s<ChildItems>\n", inner)
		for _, btn := range b.Buttons {
			writeCommandBarButton(buf, inner+"\t", btn, gen)
		}
		fmt.Fprintf(buf, "%s</ChildItems>\n", inner)
	}
	fmt.Fprintf(buf, "%s</%s>\n", indent, tag)
}

func writeCommandBarButton(buf *strings.Builder, indent string, btn *IRCommandBarButton, gen *idGenerator) {
	fmt.Fprintf(buf, "%s<Button name=\"%s\" id=\"%s\">\n", indent, escapeAttr(btn.Name), escapeAttr(idOrGen(btn.OriginalID, gen)))
	inner := indent + "\t"
	fmt.Fprintf(buf, "%s<Type>CommandBarButton</Type>\n", inner)
	if btn.Representation != "" {
		fmt.Fprintf(buf, "%s<Representation>%s</Representation>\n", inner, escapeText(btn.Representation))
	}
	if btn.CommandName != "" {
		fmt.Fprintf(buf, "%s<CommandName>%s</CommandName>\n", inner, escapeText(btn.CommandName))
	}
	if btn.Picture != "" {
		writePicture(buf, inner, "Picture", btn.Picture)
	}
	writeTitle(buf, inner, "Title", btn.Title)
	fmt.Fprintf(buf, "%s</Button>\n", indent)
}

// ── XML-escape helpers ──────────────────────────────────────────────────────

func escapeText(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}

func escapeAttr(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	).Replace(s)
}
