// Package xdto сериализует объекты конфигурации в XML формата
// СериализаторXDTO 1С:Предприятия и разбирает его обратно.
//
// Формат снят с живой базы 1С (ЗУП 3.1, платформа 8.5.1), а не восстановлен по
// документации. Его особенности, определяющие устройство пакета:
//
//   - корень называется «CatalogObject.<Имя>» / «DocumentObject.<Имя>», а
//     стандартные реквизиты идут английскими именами (Ref, DeletionMark,
//     Description, Code, Date, Number, Posted) при русских именах прикладных;
//   - ссылка пишется ГОЛЫМ UUID без типа — тип берётся из метаданных реквизита,
//     и в XML появляется (xsi:type) только там, где реквизит составного типа;
//   - пустые значения пишутся всегда: 1С различает «пусто» и «нет поля», и
//     типовой приёмник ждёт полный набор реквизитов;
//   - строки табличной части — повторяющиеся элементы с именем самой части,
//     контейнера-обёртки нет.
//
// Отсюда главное следствие для разбора: по одному UUID неизвестно, в какой
// справочник он смотрит, поэтому Read обязан идти от метаданных объекта, а не
// от текста.
package xdto

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Пространства имён формата. Объявляются на корне ровно в таком составе, как их
// пишет сама 1С: xs и xsi не используются в простом объекте, но типовой приёмник
// ждёт их на месте.
const (
	NSData = "http://v8.1c.ru/8.1/data/enterprise/current-config"
	NSXS   = "http://www.w3.org/2001/XMLSchema"
	NSXSI  = "http://www.w3.org/2001/XMLSchema-instance"
)

// Формат даты 1С: локальное время, без таймзоны и без «Z».
const dateLayout = "2006-01-02T15:04:05"

// EmptyDate — как 1С пишет незаполненную дату. Именно значение, а не пустой тег:
// пустой тег в этой позиции типовой приёмник читает как ошибку типа.
const EmptyDate = "0001-01-01T00:00:00"

// EmptyRef — незаполненная ссылка.
const EmptyRef = "00000000-0000-0000-0000-000000000000"

// Options — сведения об объекте, которых нет в самом runtime.Object: пометка
// удаления и проведённость живут не в реквизитах, а в состоянии записи.
type Options struct {
	DeletionMark bool
	Posted       bool
}

// Write сериализует объект в XML формата СериализаторXDTO.
func Write(ent *metadata.Entity, obj *runtime.Object, opts Options) (string, error) {
	if ent == nil {
		return "", fmt.Errorf("xdto: метаданные объекта не заданы")
	}
	if obj == nil {
		return "", fmt.Errorf("xdto: объект не задан")
	}
	root := RootName(ent.Kind, ent.Name)
	if root == "" {
		return "", fmt.Errorf("xdto: вид объекта %q не сериализуется", ent.Kind)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<%s xmlns=%q xmlns:xs=%q xmlns:xsi=%q>\n", root, NSData, NSXS, NSXSI)

	if err := writeElem(&b, 1, "Ref", refText(obj.ID.String()), ""); err != nil {
		return "", fmt.Errorf("xdto: Ref: %w", err)
	}
	if err := writeElem(&b, 1, "DeletionMark", boolText(opts.DeletionMark), ""); err != nil {
		return "", fmt.Errorf("xdto: DeletionMark: %w", err)
	}
	if ent.Kind == metadata.KindDocument {
		if err := writeElem(&b, 1, "Date", dateText(objValue(obj, ent, stdDate)), ""); err != nil {
			return "", fmt.Errorf("xdto: Date: %w", err)
		}
		if err := writeElem(&b, 1, "Number", plainText(objValue(obj, ent, stdNumber)), ""); err != nil {
			return "", fmt.Errorf("xdto: Number: %w", err)
		}
		if err := writeElem(&b, 1, "Posted", boolText(opts.Posted), ""); err != nil {
			return "", fmt.Errorf("xdto: Posted: %w", err)
		}
	} else {
		if err := writeElem(&b, 1, "Description", plainText(objValue(obj, ent, stdDescription)), ""); err != nil {
			return "", fmt.Errorf("xdto: Description: %w", err)
		}
		if err := writeElem(&b, 1, "Code", plainText(objValue(obj, ent, stdCode)), ""); err != nil {
			return "", fmt.Errorf("xdto: Code: %w", err)
		}
	}

	for i := range ent.Fields {
		f := &ent.Fields[i]
		if standardName(ent.Kind, f.Name) != "" {
			continue // уже выведен выше под своим английским именем
		}
		if err := writeElem(&b, 1, f.Name, fieldText(f, obj.Get(f.Name)), ""); err != nil {
			return "", fmt.Errorf("xdto: %s: %w", f.Name, err)
		}
	}

	for i := range ent.TableParts {
		tp := &ent.TableParts[i]
		for _, row := range obj.TablePartRows[tp.Name] {
			fmt.Fprintf(&b, "\t<%s>\n", tp.Name)
			for j := range tp.Fields {
				f := &tp.Fields[j]
				if err := writeElem(&b, 2, f.Name, fieldText(f, row[f.Name]), ""); err != nil {
					return "", fmt.Errorf("xdto: %s.%s: %w", tp.Name, f.Name, err)
				}
			}
			fmt.Fprintf(&b, "\t</%s>\n", tp.Name)
		}
	}

	fmt.Fprintf(&b, "</%s>", root)
	return b.String(), nil
}

// Read разбирает XML формата СериализаторXDTO в объект. resolve отдаёт
// метаданные по имени сущности: без них разобрать нечего — тип ссылки в XML не
// записан, он известен только из описания реквизита.
func Read(text string, resolve func(name string) *metadata.Entity) (*runtime.Object, Options, error) {
	var opts Options
	dec := xml.NewDecoder(strings.NewReader(text))
	start, err := firstElement(dec)
	if err != nil {
		return nil, opts, err
	}
	kind, name := parseRootName(start.Name.Local)
	if name == "" {
		return nil, opts, fmt.Errorf("xdto: неизвестный корень %q, ожидается CatalogObject.X или DocumentObject.X", start.Name.Local)
	}
	if resolve == nil {
		return nil, opts, fmt.Errorf("xdto: не задан поиск метаданных, разбор %q невозможен", name)
	}
	ent := resolve(name)
	if ent == nil {
		return nil, opts, fmt.Errorf("xdto: объект %q не найден в конфигурации", name)
	}
	if ent.Kind != kind {
		return nil, opts, fmt.Errorf("xdto: %q в конфигурации имеет вид %q, а в документе — %q", name, ent.Kind, kind)
	}

	obj := runtime.NewObject(ent.Name, ent.Kind)
	obj.EnsureTableParts(ent)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, opts, fmt.Errorf("xdto: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		local := el.Name.Local

		if tp := findTablePart(ent, local); tp != nil {
			row, err := readRow(dec, tp.Fields, el)
			if err != nil {
				return nil, opts, err
			}
			obj.TablePartRows[tp.Name] = append(obj.TablePartRows[tp.Name], row)
			continue
		}

		val, err := readText(dec, el)
		if err != nil {
			return nil, opts, err
		}
		switch strings.ToLower(local) {
		case "ref":
			id, err := uuid.Parse(val)
			if err != nil {
				return nil, opts, fmt.Errorf("xdto: поле %q: неверный UUID %q: %w", local, val, err)
			}
			obj.ID = id
			continue
		case "deletionmark":
			parsed, err := parseXMLBool(val)
			if err != nil {
				return nil, opts, fmt.Errorf("xdto: поле %q: %w", local, err)
			}
			opts.DeletionMark = parsed
			continue
		case "posted":
			parsed, err := parseXMLBool(val)
			if err != nil {
				return nil, opts, fmt.Errorf("xdto: поле %q: %w", local, err)
			}
			opts.Posted = parsed
			continue
		}
		if f := fieldForXMLName(ent, local); f != nil {
			parsed, err := parseValue(f, val)
			if err != nil {
				return nil, opts, fmt.Errorf("xdto: поле %q: %w", local, err)
			}
			obj.Set(f.Name, parsed)
		}
	}
	return obj, opts, nil
}

// RootName — имя корневого элемента для вида объекта.
func RootName(kind metadata.Kind, name string) string {
	switch kind {
	case metadata.KindCatalog:
		return "CatalogObject." + name
	case metadata.KindDocument:
		return "DocumentObject." + name
	}
	return ""
}

func parseRootName(root string) (metadata.Kind, string) {
	switch {
	case strings.HasPrefix(root, "CatalogObject."):
		return metadata.KindCatalog, strings.TrimPrefix(root, "CatalogObject.")
	case strings.HasPrefix(root, "DocumentObject."):
		return metadata.KindDocument, strings.TrimPrefix(root, "DocumentObject.")
	}
	return "", ""
}

// Стандартные реквизиты. Платформа не заводит их отдельным видом — они
// объявляются обычными полями, и узнаются по имени, как это уже делает
// metadata.labelNames для представления.
type stdKind int

const (
	stdDescription stdKind = iota
	stdCode
	stdDate
	stdNumber
)

var stdAliases = map[stdKind][]string{
	stdDescription: {"наименование", "description"},
	stdCode:        {"код", "code"},
	stdDate:        {"дата", "date"},
	stdNumber:      {"номер", "number"},
}

// standardName возвращает английское имя стандартного реквизита для поля, либо
// пустую строку, если поле прикладное. Дата и Номер стандартны только у
// документа: у справочника поле с таким именем — обычный реквизит.
func standardName(kind metadata.Kind, field string) string {
	low := strings.ToLower(strings.TrimSpace(field))
	match := func(k stdKind) bool {
		for _, a := range stdAliases[k] {
			if a == low {
				return true
			}
		}
		return false
	}
	if kind == metadata.KindDocument {
		if match(stdDate) {
			return "Date"
		}
		if match(stdNumber) {
			return "Number"
		}
		return ""
	}
	if match(stdDescription) {
		return "Description"
	}
	if match(stdCode) {
		return "Code"
	}
	return ""
}

// objValue достаёт значение стандартного реквизита по любому из его имён.
func objValue(obj *runtime.Object, ent *metadata.Entity, k stdKind) any {
	for i := range ent.Fields {
		low := strings.ToLower(ent.Fields[i].Name)
		for _, a := range stdAliases[k] {
			if low == a {
				return obj.Get(ent.Fields[i].Name)
			}
		}
	}
	return nil
}

// fieldForXMLName ищет поле по имени из XML: сперва как есть, затем — если это
// английское имя стандартного реквизита — по его русскому синониму.
func fieldForXMLName(ent *metadata.Entity, xmlName string) *metadata.Field {
	for i := range ent.Fields {
		if strings.EqualFold(ent.Fields[i].Name, xmlName) {
			return &ent.Fields[i]
		}
	}
	var k stdKind
	switch strings.ToLower(xmlName) {
	case "description":
		k = stdDescription
	case "code":
		k = stdCode
	case "date":
		k = stdDate
	case "number":
		k = stdNumber
	default:
		return nil
	}
	for i := range ent.Fields {
		low := strings.ToLower(ent.Fields[i].Name)
		for _, a := range stdAliases[k] {
			if low == a {
				return &ent.Fields[i]
			}
		}
	}
	return nil
}

func findTablePart(ent *metadata.Entity, name string) *metadata.TablePart {
	for i := range ent.TableParts {
		if strings.EqualFold(ent.TableParts[i].Name, name) {
			return &ent.TableParts[i]
		}
	}
	return nil
}

func readRow(dec *xml.Decoder, fields []metadata.Field, start xml.StartElement) (map[string]any, error) {
	row := make(map[string]any, len(fields))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("xdto: строка %q: %w", start.Name.Local, err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			val, err := readText(dec, el)
			if err != nil {
				return nil, err
			}
			for i := range fields {
				if strings.EqualFold(fields[i].Name, el.Name.Local) {
					parsed, err := parseValue(&fields[i], val)
					if err != nil {
						return nil, fmt.Errorf("xdto: поле %q: %w", start.Name.Local+"."+el.Name.Local, err)
					}
					row[fields[i].Name] = parsed
					break
				}
			}
		case xml.EndElement:
			if el.Name.Local == start.Name.Local {
				return row, nil
			}
		}
	}
}

// readText читает текстовое содержимое элемента и закрывает его.
func readText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("xdto: элемент %q: %w", start.Name.Local, err)
		}
		switch el := tok.(type) {
		case xml.CharData:
			sb.Write(el)
		case xml.EndElement:
			if el.Name.Local == start.Name.Local {
				return sb.String(), nil
			}
		}
	}
}

func firstElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return xml.StartElement{}, fmt.Errorf("xdto: пустой документ")
		}
		if err != nil {
			return xml.StartElement{}, fmt.Errorf("xdto: %w", err)
		}
		if el, ok := tok.(xml.StartElement); ok {
			return el, nil
		}
	}
}

// writeElem пишет элемент с отступом. Пустое значение выводится
// самозакрывающимся тегом — так же, как это делает 1С.
func writeElem(b *strings.Builder, depth int, name, value, xsiType string) error {
	b.WriteString(strings.Repeat("\t", depth))
	if value == "" {
		fmt.Fprintf(b, "<%s/>\n", name)
		return nil
	}
	if xsiType != "" {
		fmt.Fprintf(b, "<%s xsi:type=%q>", name, xsiType)
	} else {
		fmt.Fprintf(b, "<%s>", name)
	}
	if err := xml.EscapeText(b, []byte(value)); err != nil {
		return err
	}
	fmt.Fprintf(b, "</%s>\n", name)
	return nil
}

// fieldText приводит значение реквизита к тексту формата.
func fieldText(f *metadata.Field, v any) string {
	if f.RefEntity != "" {
		return refText(scalarString(v))
	}
	switch f.Type {
	case metadata.FieldTypeBool:
		return boolText(asBool(v))
	case metadata.FieldTypeDate:
		return dateText(v)
	case metadata.FieldTypeNumber:
		if v == nil {
			return "0"
		}
		return scalarString(v)
	default:
		return plainText(v)
	}
}

func plainText(v any) string {
	if v == nil {
		return ""
	}
	return scalarString(v)
}

func refText(v string) string {
	if v == "" {
		return EmptyRef
	}
	if id, err := uuid.Parse(v); err == nil {
		return id.String()
	}
	return v
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func dateText(v any) string {
	t, ok := asTime(v)
	if !ok || t.IsZero() {
		return EmptyDate
	}
	return t.In(time.Local).Format(dateLayout)
}

func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case *time.Time:
		if t != nil {
			return *t, true
		}
	case string:
		if parsed, err := time.ParseInLocation(dateLayout, t, time.Local); err == nil {
			return parsed, true
		}
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t == "true" || t == "1" || strings.EqualFold(t, "Истина")
	}
	return false
}

// scalarString — текст значения. Числа приходят как decimal.Decimal, чей
// Stringer даёт точную строку без потери разрядов; ссылки — как uuid.UUID либо
// объект со ссылкой.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case uuid.UUID:
		return t.String()
	case *uuid.UUID:
		if t == nil {
			return ""
		}
		return t.String()
	case interface{ GetRefUUID() string }:
		return t.GetRefUUID()
	case time.Time:
		return t.Format(dateLayout)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// parseValue разбирает текст в значение реквизита по его типу. Типизированное
// значение либо разбирается целиком, либо отклоняется: обмен не должен молча
// подменять повреждённую дату/ссылку/булево пустым или ложным значением.
func parseValue(f *metadata.Field, text string) (any, error) {
	if f.RefEntity != "" {
		if text == "" || text == EmptyRef {
			return nil, nil
		}
		id, err := uuid.Parse(text)
		if err != nil {
			return nil, fmt.Errorf("неверный UUID %q: %w", text, err)
		}
		return id.String(), nil
	}
	switch f.Type {
	case metadata.FieldTypeBool:
		return parseXMLBool(text)
	case metadata.FieldTypeDate:
		if text == "" || text == EmptyDate {
			return nil, nil
		}
		if t, ok := asTime(text); ok {
			return t, nil
		}
		return nil, fmt.Errorf("неверная дата %q, ожидается %s", text, dateLayout)
	default:
		return text, nil
	}
}

func parseXMLBool(text string) (bool, error) {
	switch text {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("неверное логическое значение %q, ожидается true, false, 1 или 0", text)
	}
}
