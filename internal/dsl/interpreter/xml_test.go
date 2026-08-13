package interpreter

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestReadXML_TreeShape(t *testing.T) {
	doc := `<Заказ Номер="7" Дата="2026-08-10">
		<Строка Код="1">Стол</Строка>
		<Строка Код="2">Стул</Строка>
	</Заказ>`
	v, err := builtinReadXML([]any{doc}, "", 0)
	if err != nil {
		t.Fatalf("ПрочитатьXML: %v", err)
	}
	root, ok := v.(*Struct)
	if !ok {
		t.Fatalf("ожидалась Структура, получено %T", v)
	}
	if got := root.Get(xmlFieldName); got != "Заказ" {
		t.Errorf("имя корня = %v, ожидалось Заказ", got)
	}
	attrs, ok := root.Get(xmlFieldAttrs).(*Map)
	if !ok {
		t.Fatalf("Атрибуты: ожидалось Соответствие, получено %T", root.Get(xmlFieldAttrs))
	}
	if got := attrs.Get("Номер"); got != "7" {
		t.Errorf("атрибут Номер = %v, ожидалось 7", got)
	}
	children, ok := root.Get(xmlFieldChildren).(*Array)
	if !ok {
		t.Fatalf("Элементы: ожидался Массив, получено %T", root.Get(xmlFieldChildren))
	}
	if len(children.Iterate()) != 2 {
		t.Fatalf("вложенных элементов %d, ожидалось 2", len(children.Iterate()))
	}
	first := children.Index(0).(*Struct)
	if got := first.Get(xmlFieldText); got != "Стол" {
		t.Errorf("текст первой строки = %v, ожидалось Стол", got)
	}
}

// Главное свойство пары: документ переживает круг чтение → запись без потери
// структуры. encoding/xml канонизирует пустой тег в отдельные открывающий и
// закрывающий теги, поэтому байтовое равенство здесь не является контрактом.
func TestXML_RoundTrip(t *testing.T) {
	doc := `<Заказ Номер="7"><Строка Код="1">Стол</Строка><Пустой/></Заказ>`
	tree, err := builtinReadXML([]any{doc}, "", 0)
	if err != nil {
		t.Fatalf("ПрочитатьXML: %v", err)
	}
	out, err := builtinWriteXML([]any{tree}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	want := `<Заказ Номер="7"><Строка Код="1">Стол</Строка><Пустой></Пустой></Заказ>`
	if out != want {
		t.Errorf("круг не сошёлся:\n получено %s\n ожидалось %s", out, want)
	}
}

func requireXMLUserError(t *testing.T, contains string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ожидалась пользовательская ошибка")
		}
		e, ok := r.(userError)
		if !ok {
			t.Fatalf("ожидалась userError, получена %T: %v", r, r)
		}
		message := e.Msg
		if contains != "" && !strings.Contains(message, contains) {
			t.Fatalf("ошибка %q не содержит %q", message, contains)
		}
	}()
	fn()
}

func TestReadXML_StrictAndSingleRoot(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"mismatched tags", `<a><b></a>`, ""},
		{"multiple roots", `<a/><b/>`, "более одного корневого"},
		{"text before root", `данные<a/>`, "вне корневого"},
		{"text after root", `<a/>данные`, "вне корневого"},
		{"non XML whitespace outside root", "\u00a0<a/>", "вне корневого"},
		{"character reference before root", `&#x20;<a/>`, "вне корневого"},
		{"character reference after root", `<a/>&#9;`, "вне корневого"},
		{"CDATA before root", `<![CDATA[ ]]><a/>`, "вне корневого"},
		{"duplicate attributes", `<a x="1" x="2"/>`, "повторяющийся атрибут"},
		{"attributes without whitespace", `<a x="1"y="2"/>`, "ожидался пробел"},
		{"unquoted attribute", `<a x=1/>`, "кавычки"},
		{"unknown entity", `<a>&unknown;</a>`, ""},
		{"raw less-than in attribute", `<a x="<"/>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireXMLUserError(t, tc.want, func() {
				_, _ = builtinReadXML([]any{tc.doc}, "", 0)
			})
		})
	}
}

func TestReadXML_AllowsLiteralDocumentWhitespaceAndBOM(t *testing.T) {
	v, err := builtinReadXML([]any{"\uFEFF \t\r\n<a>&#x20;</a> \t\r\n"}, "", 0)
	if err != nil {
		t.Fatalf("ПрочитатьXML: %v", err)
	}
	root := v.(*Struct)
	if got := root.Get(xmlFieldText); got != " " {
		t.Fatalf("ссылка внутри корня дала Текст = %q, ожидался пробел", got)
	}
}

func TestXML_AttributeAndTextBudgets(t *testing.T) {
	t.Run("read attribute limit", func(t *testing.T) {
		var doc strings.Builder
		doc.Grow((maxXMLAttributesPerElement + 1) * 12)
		doc.WriteString("<r")
		for i := 0; i <= maxXMLAttributesPerElement; i++ {
			doc.WriteString(" a")
			doc.WriteString(strconv.Itoa(i))
			doc.WriteString(`="v"`)
		}
		doc.WriteString("/>")
		requireXMLUserError(t, "число атрибутов", func() {
			_, _ = builtinReadXML([]any{doc.String()}, "", 0)
		})
	})

	t.Run("write attribute limit", func(t *testing.T) {
		keys := make([]any, maxXMLAttributesPerElement+1)
		vals := make([]any, maxXMLAttributesPerElement+1)
		for i := range keys {
			keys[i] = "a" + strconv.Itoa(i)
			vals[i] = "v"
		}
		tree := &Struct{vals: map[string]any{}}
		tree.Set(xmlFieldName, "Корень")
		tree.Set(xmlFieldAttrs, &Map{keys: keys, vals: vals})
		requireXMLUserError(t, "число атрибутов", func() {
			_, _ = builtinWriteXML([]any{tree}, "", 0)
		})
	})

	t.Run("read total attribute limit", func(t *testing.T) {
		var doc strings.Builder
		doc.Grow(maxXMLAttributesTotal * 12)
		doc.WriteString("<r>")
		for element := 0; element <= maxXMLAttributesTotal/maxXMLAttributesPerElement; element++ {
			doc.WriteString("<n")
			for i := 0; i < maxXMLAttributesPerElement; i++ {
				doc.WriteString(" a")
				doc.WriteString(strconv.Itoa(i))
				doc.WriteString(`="v"`)
			}
			doc.WriteString("/>")
		}
		doc.WriteString("</r>")
		requireXMLUserError(t, "общее число атрибутов", func() {
			_, _ = builtinReadXML([]any{doc.String()}, "", 0)
		})
	})

	t.Run("read document bytes", func(t *testing.T) {
		doc := "<r>" + strings.Repeat("x", maxXMLDocumentBytes) + "</r>"
		requireXMLUserError(t, "размер XML", func() {
			_, _ = builtinReadXML([]any{doc}, "", 0)
		})
	})

	t.Run("write text bytes", func(t *testing.T) {
		text := strings.Repeat("x", maxXMLTextBytes+1)
		requireXMLUserError(t, "объём текстовых данных", func() {
			_, _ = builtinWriteXML([]any{text, "Корень"}, "", 0)
		})
	})

	t.Run("write exact document bytes", func(t *testing.T) {
		const markupBytes = len("<r></r>")
		value, err := builtinWriteXML([]any{strings.Repeat("x", maxXMLDocumentBytes-markupBytes), "r"}, "", 0)
		if err != nil {
			t.Fatalf("ЗаписатьXML: %v", err)
		}
		text, ok := value.(string)
		if !ok {
			t.Fatalf("ожидалась строка, получено %T", value)
		}
		if len(text) != maxXMLDocumentBytes {
			t.Fatalf("размер готового XML = %d, ожидалось %d", len(text), maxXMLDocumentBytes)
		}
	})

	t.Run("write escaped document bytes", func(t *testing.T) {
		// Логический текст заметно меньше 16 МиБ, но после XML-экранирования
		// каждый '&' занимает пять байт.
		text := strings.Repeat("&", maxXMLDocumentBytes/5+1)
		requireXMLUserError(t, "размер XML", func() {
			_, _ = builtinWriteXML([]any{text, "r"}, "", 0)
		})
	})
}

func TestReadXML_RejectsUnrepresentableContent(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"mixed content", `<a>до<b/>после</a>`, "смешанное содержимое"},
		{"non XML whitespace mixed content", "<a>\u00a0<b/></a>", "смешанное содержимое"},
		{"comment", `<a><!-- важный комментарий --></a>`, "комментарии"},
		{"directive", `<!DOCTYPE a><a/>`, "директивы"},
		{"processing instruction", `<?target value?><a/>`, "инструкции обработки"},
		{"processing instruction named like declaration", `<?xmlfoo bar?><a/>`, "инструкции обработки"},
		{"declaration not at start", `<a/><?xml version="1.0"?>`, "инструкции обработки"},
		{"empty prefix", `<:a/>`, "префикс"},
		{"empty local name", `<a:/>`, "локальное имя"},
		{"two colons", `<a:b:c/>`, "более одного символа"},
		{"non UTF-8 encoding", `<?xml version="1.0" encoding="windows-1251"?><a/>`, "кодировка XML"},
		{"unsupported version", `<?xml version="2.0"?><a/>`, "версия XML"},
		{"declaration without version", `<?xml encoding="UTF-8"?><a/>`, "version"},
		{"unterminated declaration", `<?xml version="1.0"`, "незавершённое объявление"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireXMLUserError(t, tc.want, func() {
				_, _ = builtinReadXML([]any{tc.doc}, "", 0)
			})
		})
	}
}

// Форма сообщения обмена 1С: объявление, префиксы пространств имён и xsi:type,
// в котором передаётся имя типа объекта. Всё это должно доезжать до дерева
// нетронутым и переживать обратную запись.
func TestXML_NamespacesAreLexical(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<v8msg:Body xmlns:v8msg="http://v8.1c.ru/messages" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<Объект xsi:type="СправочникОбъект.Номенклатура"><Ссылка>abc</Ссылка></Объект>` +
		`</v8msg:Body>`
	v, err := builtinReadXML([]any{doc}, "", 0)
	if err != nil {
		t.Fatalf("ПрочитатьXML: %v", err)
	}
	root := v.(*Struct)
	if got := root.Get(xmlFieldName); got != "v8msg:Body" {
		t.Errorf("имя корня = %v, ожидалось v8msg:Body", got)
	}
	attrs := root.Get(xmlFieldAttrs).(*Map)
	if got := attrs.Get("xmlns:v8msg"); got != "http://v8.1c.ru/messages" {
		t.Errorf("объявление xmlns потеряно: %v", got)
	}
	object := root.Get(xmlFieldChildren).(*Array).Index(0).(*Struct)
	if got := object.Get(xmlFieldAttrs).(*Map).Get("xsi:type"); got != "СправочникОбъект.Номенклатура" {
		t.Errorf("xsi:type = %v, ожидалось СправочникОбъект.Номенклатура", got)
	}

	out, err := builtinWriteXML([]any{root}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	// Объявление не является частью дерева и не восстанавливается.
	if got, want := out, strings.TrimPrefix(doc, `<?xml version="1.0" encoding="UTF-8"?>`); got != want {
		t.Fatalf("получено %q, ожидалось %q", got, want)
	}
}

func TestReadXML_AcceptsDeclarationVariants(t *testing.T) {
	for _, declaration := range []string{
		`<?xml version="1.0"?>`,
		`<?xml version='1.0' encoding='utf-8'?>`,
		`<?xml   version = "1.0"   encoding = "UTF-8"   standalone = "no" ?>`,
		string(rune(0xFEFF)) + `<?xml version="1.0" encoding="UTF-8"?>`,
	} {
		t.Run(declaration, func(t *testing.T) {
			v, err := builtinReadXML([]any{declaration + "\n<a>текст</a>"}, "", 0)
			if err != nil {
				t.Fatalf("ПрочитатьXML: %v", err)
			}
			if got := v.(*Struct).Get(xmlFieldText); got != "текст" {
				t.Fatalf("Текст = %v, ожидалось «текст»", got)
			}
		})
	}
}

func TestReadXML_PreservesTextOnlyWhitespace(t *testing.T) {
	doc := "<Текст>  строка &amp; ещё\n</Текст>"
	v, err := builtinReadXML([]any{doc}, "", 0)
	if err != nil {
		t.Fatalf("ПрочитатьXML: %v", err)
	}
	root := v.(*Struct)
	if got, want := root.Get(xmlFieldText), "  строка & ещё\n"; got != want {
		t.Fatalf("Текст = %q, ожидалось %q", got, want)
	}
	out, err := builtinWriteXML([]any{root}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	if got, want := out, doc; got != want {
		t.Fatalf("получено %q, ожидалось %q", got, want)
	}
}

func TestWriteXML_RejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", "1root", ":root", "root:", "a:b:c", `a></a><evil`} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			requireXMLUserError(t, "недопустимое имя", func() {
				_, _ = builtinWriteXML([]any{"value", name}, "", 0)
			})
		})

		tree := &Struct{vals: map[string]any{}}
		tree.Set(xmlFieldName, name)
		requireXMLUserError(t, "недопустимое имя", func() {
			_, _ = builtinWriteXML([]any{tree}, "", 0)
		})
	}
}

func TestWriteXML_AllowsUnicodeNames(t *testing.T) {
	out, err := builtinWriteXML([]any{"значение", "Заказ_2"}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	if got, want := out, "<Заказ_2>значение</Заказ_2>"; got != want {
		t.Fatalf("получено %q, ожидалось %q", got, want)
	}
}

func TestWriteXML_MalformedTreeNeverFallsBack(t *testing.T) {
	newTree := func() *Struct {
		tree := &Struct{vals: map[string]any{}}
		tree.Set(xmlFieldName, "Корень")
		return tree
	}

	cases := []struct {
		name string
		want string
		edit func(*Struct)
	}{
		{"attributes type", "должно быть Соответствием", func(s *Struct) { s.Set(xmlFieldAttrs, "не соответствие") }},
		{"attribute key type", "имя атрибута", func(s *Struct) { s.Set(xmlFieldAttrs, &Map{keys: []any{int64(1)}, vals: []any{"x"}}) }},
		{"attribute value type", "значение атрибута", func(s *Struct) { s.Set(xmlFieldAttrs, &Map{keys: []any{"Код"}, vals: []any{int64(1)}}) }},
		{"children type", "должно быть Массивом", func(s *Struct) { s.Set(xmlFieldChildren, "не массив") }},
		{"malformed child", "не является узлом XML", func(s *Struct) { s.Set(xmlFieldChildren, &Array{items: []any{"не узел"}}) }},
		{"text type", "должно быть строкой", func(s *Struct) { s.Set(xmlFieldText, int64(1)) }},
		{"unknown field", "неизвестное поле", func(s *Struct) { s.Set("ПотерянноеПоле", "данные") }},
		{"mixed content", "смешанное содержимое", func(s *Struct) {
			child := newTree()
			child.Set(xmlFieldName, "Потомок")
			s.Set(xmlFieldText, "данные")
			s.Set(xmlFieldChildren, &Array{items: []any{child}})
		}},
		{"duplicate attributes", "повторяющийся атрибут", func(s *Struct) {
			s.Set(xmlFieldAttrs, &Map{keys: []any{"Код", "Код"}, vals: []any{"1", "2"}})
		}},
		{"malformed qualified attribute", "недопустимое имя атрибута", func(s *Struct) {
			s.Set(xmlFieldAttrs, &Map{keys: []any{"xmlns:"}, vals: []any{"urn:test"}})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := newTree()
			tc.edit(tree)
			requireXMLUserError(t, tc.want, func() {
				_, _ = builtinWriteXML([]any{tree}, "", 0)
			})
		})
	}
}

func TestWriteXML_EncoderEscapesAttributes(t *testing.T) {
	tree := &Struct{vals: map[string]any{}}
	tree.Set(xmlFieldName, "Корень")
	tree.Set(xmlFieldAttrs, &Map{keys: []any{"Описание"}, vals: []any{`"кавычки" & <тег>`}})
	out, err := builtinWriteXML([]any{tree}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	text := out.(string)
	if strings.Contains(text, `<тег>`) || strings.Contains(text, `"кавычки"`) || !strings.Contains(text, "&amp;") {
		t.Fatalf("атрибут записан небезопасно: %s", text)
	}
	if _, err := parseXMLDocument(text); err != nil {
		t.Fatalf("writer создал невалидный XML: %v (%s)", err, text)
	}
}

func TestWriteXML_RejectsInvalidCharacters(t *testing.T) {
	requireXMLUserError(t, "недопустимый символ XML", func() {
		_, _ = builtinWriteXML([]any{"до\x00после", "Корень"}, "", 0)
	})

	tree := &Struct{vals: map[string]any{}}
	tree.Set(xmlFieldName, "Корень")
	tree.Set(xmlFieldAttrs, &Map{keys: []any{"Код"}, vals: []any{"до\x00после"}})
	requireXMLUserError(t, "недопустимый символ XML", func() {
		_, _ = builtinWriteXML([]any{tree}, "", 0)
	})
}

func TestXML_ResourceLimits(t *testing.T) {
	t.Run("read depth", func(t *testing.T) {
		doc := strings.Repeat("<a>", maxXMLDepth+1) + strings.Repeat("</a>", maxXMLDepth+1)
		requireXMLUserError(t, "глубина XML", func() {
			_, _ = builtinReadXML([]any{doc}, "", 0)
		})
	})

	t.Run("read nodes", func(t *testing.T) {
		var doc strings.Builder
		doc.Grow(maxXMLNodes*4 + 7)
		doc.WriteString("<r>")
		for range maxXMLNodes {
			doc.WriteString("<n/>")
		}
		doc.WriteString("</r>")
		requireXMLUserError(t, "число элементов XML", func() {
			_, _ = builtinReadXML([]any{doc.String()}, "", 0)
		})
	})

	t.Run("write recursive collection", func(t *testing.T) {
		array := &Array{}
		array.items = []any{array}
		requireXMLUserError(t, "глубина XML", func() {
			_, _ = builtinWriteXML([]any{array, "Корень"}, "", 0)
		})
	})
}

// Произвольная Структура пишется по именам полей. ⚠️ DSL регистронезависим, и
// Struct.Set хранит имена в нижнем регистре — значит и теги выйдут строчными.
// Когда регистр важен, документ строят деревом (поле Имя), как в round-trip.
func TestWriteXML_ArbitraryCollections(t *testing.T) {
	s := &Struct{vals: map[string]any{}}
	s.Set("Код", int64(7))
	s.Set("Наименование", "Стол")
	out, err := builtinWriteXML([]any{s, "Товар"}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	want := "<Товар><код>7</код><наименование>Стол</наименование></Товар>"
	if out != want {
		t.Errorf("получено %s, ожидалось %s", out, want)
	}
}

func TestWriteXML_EscapesSpecialChars(t *testing.T) {
	s := &Struct{vals: map[string]any{}}
	s.Set("Комментарий", `ООО "Ромашка" & Ко <срочно>`)
	out, err := builtinWriteXML([]any{s, "Заявка"}, "", 0)
	if err != nil {
		t.Fatalf("ЗаписатьXML: %v", err)
	}
	text, _ := out.(string)
	if strings.Contains(text, "<срочно>") || !strings.Contains(text, "&amp;") {
		t.Errorf("спецсимволы не экранированы: %s", text)
	}
}

func TestXMLString_Primitives(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{true, "true"},
		{false, "false"},
		{int64(42), "42"},
		{decimal.RequireFromString("42.50"), "42.5"},
		{"уже строка", "уже строка"},
		{time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC), "2026-08-10T12:30:00Z"},
		{nil, ""},
	}
	for _, c := range cases {
		got, err := builtinXMLString([]any{c.in}, "", 0)
		if err != nil {
			t.Fatalf("XMLСтрока(%v): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("XMLСтрока(%v) = %v, ожидалось %v", c.in, got, c.want)
		}
	}
}

func TestXMLValue_ParsesByType(t *testing.T) {
	got, err := builtinXMLValue([]any{"Число", "42.5"}, "", 0)
	if err != nil {
		t.Fatalf("XMLЗначение: %v", err)
	}
	d, ok := got.(decimal.Decimal)
	if !ok || !d.Equal(decimal.RequireFromString("42.5")) {
		t.Errorf("Число = %v (%T), ожидалось 42.5", got, got)
	}

	got, err = builtinXMLValue([]any{"Булево", "true"}, "", 0)
	if err != nil {
		t.Fatalf("XMLЗначение: %v", err)
	}
	if got != true {
		t.Errorf("Булево = %v, ожидалось true", got)
	}

	got, err = builtinXMLValue([]any{"Дата", "2026-08-10T12:30:00"}, "", 0)
	if err != nil {
		t.Fatalf("XMLЗначение: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok || tm.Year() != 2026 || tm.Day() != 10 {
		t.Errorf("Дата = %v (%T)", got, got)
	}

	wantTime := time.Date(2026, 8, 10, 12, 30, 0, 123456789, time.FixedZone("MSK", 3*60*60))
	got, err = builtinXMLValue([]any{"dateTime", "2026-08-10T12:30:00.123456789+03:00"}, "", 0)
	if err != nil {
		t.Fatalf("XMLЗначение(dateTime): %v", err)
	}
	parsedTime, ok := got.(time.Time)
	if !ok || !parsedTime.Equal(wantTime) || parsedTime.Nanosecond() != wantTime.Nanosecond() {
		t.Errorf("dateTime = %v (%T), ожидалось %v", got, got, wantTime)
	}

	got, err = builtinXMLValue([]any{"decimal", "42.5"}, "", 0)
	if err != nil {
		t.Fatalf("XMLЗначение(decimal): %v", err)
	}
	if d, ok := got.(decimal.Decimal); !ok || !d.Equal(decimal.RequireFromString("42.5")) {
		t.Errorf("decimal = %v (%T), ожидалось 42.5", got, got)
	}
}

func TestXMLValue_StrictBoundedDecimal(t *testing.T) {
	valid := map[string]string{
		"+001.2300": "1.23",
		".5":        "0.5",
		"5.":        "5",
		" -0.25 ":   "-0.25",
	}
	for input, want := range valid {
		got, err := builtinXMLValue([]any{"decimal", input}, "", 0)
		if err != nil {
			t.Fatalf("XMLЗначение(%q): %v", input, err)
		}
		text, err := builtinXMLString([]any{got}, "", 0)
		if err != nil {
			t.Fatalf("XMLСтрока(%q): %v", input, err)
		}
		if text != want {
			t.Errorf("XMLСтрока(XMLЗначение(%q)) = %q, ожидалось %q", input, text, want)
		}
	}

	invalid := []string{
		"1e3",
		"1E+3",
		"NaN",
		"+Inf",
		".",
		"1.2.3",
		"\u00a01",
		strings.Repeat("9", maxXMLDecimalLexicalLength+1),
	}
	for _, input := range invalid {
		requireXMLUserError(t, "некорректное decimal", func() {
			_, _ = builtinXMLValue([]any{"decimal", input}, "", 0)
		})
	}
}

func TestXMLString_RejectsDecimalExpansionBeforeString(t *testing.T) {
	hugeCoefficient, ok := new(big.Int).SetString(strings.Repeat("9", maxXMLDecimalDigits+1), 10)
	if !ok {
		t.Fatal("не удалось создать большой коэффициент")
	}
	cases := []decimal.Decimal{
		decimal.New(1, int32(2_147_483_647)),
		decimal.New(1, int32(-2_147_483_648)),
		decimal.NewFromBigInt(hugeCoefficient, 0),
	}
	for _, value := range cases {
		requireXMLUserError(t, "decimal", func() {
			_, _ = builtinXMLString([]any{value}, "", 0)
		})
		requireXMLUserError(t, "decimal", func() {
			_, _ = builtinXMLTypeOf([]any{value}, "", 0)
		})
		requireXMLUserError(t, "decimal", func() {
			_, _ = builtinWriteXML([]any{value, "Число"}, "", 0)
		})
	}

	unsafeKey := &Map{
		keys: []any{decimal.New(1, int32(2_147_483_647))},
		vals: []any{"значение"},
	}
	requireXMLUserError(t, "ключа Соответствия должно быть строкой", func() {
		_, _ = builtinWriteXML([]any{unsafeKey, "Корень"}, "", 0)
	})
}

func TestXML_NonFiniteFloatFailsClosed(t *testing.T) {
	values := []float64{math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, value := range values {
		requireXMLUserError(t, "конечным", func() {
			_, _ = builtinXMLString([]any{value}, "", 0)
		})
		requireXMLUserError(t, "конечным", func() {
			_, _ = builtinWriteXML([]any{value, "Число"}, "", 0)
		})
		requireXMLUserError(t, "конечным", func() {
			_, _ = builtinXMLTypeOf([]any{value}, "", 0)
		})
	}
}

func TestXML_DateTimeRFC3339Bounds(t *testing.T) {
	invalid := []struct {
		value time.Time
		want  string
	}{
		{time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC), "0000..9999"},
		{time.Date(10_000, 1, 1, 0, 0, 0, 0, time.UTC), "0000..9999"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("+14:01", 14*60*60+60)), "-14:00..+14:00"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("-14:01", -14*60*60-60)), "-14:00..+14:00"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("+24", 24*60*60)), "-14:00..+14:00"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("odd", 60*60+1)), "кратно минуте"},
	}
	for _, tc := range invalid {
		requireXMLUserError(t, tc.want, func() {
			_, _ = builtinXMLString([]any{tc.value}, "", 0)
		})
		requireXMLUserError(t, tc.want, func() {
			_, _ = builtinWriteXML([]any{tc.value, "Дата"}, "", 0)
		})
	}

	valid := time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("edge", 14*60*60))
	got, err := builtinXMLString([]any{valid}, "", 0)
	if err != nil {
		t.Fatalf("XMLСтрока: %v", err)
	}
	if got != "0000-01-01T00:00:00+14:00" {
		t.Fatalf("граничная дата = %q", got)
	}
}

// В поддерживаемом RFC/XSD-диапазоне XMLСтрока и XMLЗначение должны давать
// одинаковое лексическое значение на круге «выгрузил → загрузил».
func TestXMLStringValue_AreInverse(t *testing.T) {
	for _, c := range []struct {
		typeName string
		in       any
	}{
		{"Неопределено", nil},
		{"Число", decimal.RequireFromString("-17.25")},
		{"Булево", false},
		{"Дата", time.Date(2026, 1, 2, 3, 4, 5, 987654321, time.FixedZone("MSK", 3*60*60))},
		{"Строка", "текст с пробелами"},
	} {
		s, err := builtinXMLString([]any{c.in}, "", 0)
		if err != nil {
			t.Fatalf("XMLСтрока: %v", err)
		}
		back, err := builtinXMLValue([]any{c.typeName, s}, "", 0)
		if err != nil {
			t.Fatalf("XMLЗначение(%s, %v): %v", c.typeName, s, err)
		}
		again, err := builtinXMLString([]any{back}, "", 0)
		if err != nil {
			t.Fatalf("XMLСтрока: %v", err)
		}
		if again != s {
			t.Errorf("%s: круг дал %v вместо %v", c.typeName, again, s)
		}
	}
}

func TestXMLTypeOf_CanBePassedToXMLValue(t *testing.T) {
	values := []struct {
		name         string
		value        any
		wantTypeName string
	}{
		{"undefined", nil, xmlUndefinedTypeName},
		{"nil time pointer", (*time.Time)(nil), xmlUndefinedTypeName},
		{"nil decimal pointer", (*decimal.Decimal)(nil), xmlUndefinedTypeName},
		{"decimal", decimal.RequireFromString("17.25"), "decimal"},
		{"boolean", true, "boolean"},
		{"string", "строка", "string"},
		{"dateTime", time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("MSK", 3*60*60)), "dateTime"},
	}
	for _, tc := range values {
		t.Run(tc.name, func(t *testing.T) {
			typeName, err := builtinXMLTypeOf([]any{tc.value}, "", 0)
			if err != nil {
				t.Fatalf("XMLТипЗнч(%v): %v", tc.value, err)
			}
			if typeName != tc.wantTypeName {
				t.Fatalf("XMLТипЗнч(%v) = %q, ожидалось %q", tc.value, typeName, tc.wantTypeName)
			}
			text, err := builtinXMLString([]any{tc.value}, "", 0)
			if err != nil {
				t.Fatalf("XMLСтрока(%v): %v", tc.value, err)
			}
			back, err := builtinXMLValue([]any{typeName, text}, "", 0)
			if err != nil {
				t.Fatalf("XMLЗначение(%v, %v): %v", typeName, text, err)
			}
			again, err := builtinXMLString([]any{back}, "", 0)
			if err != nil {
				t.Fatalf("XMLСтрока(%v): %v", back, err)
			}
			if again != text {
				t.Errorf("%T: круг дал %v вместо %v", tc.value, again, text)
			}
		})
	}
}

func TestXMLValue_UnknownTypeIsUserError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("ожидалась ошибка на неизвестном типе")
		}
	}()
	_, _ = builtinXMLValue([]any{"Ссылка", "что-то"}, "", 0)
}

func TestFindDisallowedXMLCharacters(t *testing.T) {
	got, err := builtinFindDisallowedXMLChars([]any{"обычный текст"}, "", 0)
	if err != nil {
		t.Fatalf("НайтиНедопустимыеСимволыXML: %v", err)
	}
	if got != int64(0) {
		t.Errorf("на чистой строке = %v, ожидалось 0", got)
	}

	got, err = builtinFindDisallowedXMLChars([]any{"те\x00кст"}, "", 0)
	if err != nil {
		t.Fatalf("НайтиНедопустимыеСимволыXML: %v", err)
	}
	if got != int64(3) {
		t.Errorf("позиция нулевого байта = %v, ожидалось 3", got)
	}

	got, err = builtinFindDisallowedXMLChars([]any{"те\xffкст"}, "", 0)
	if err != nil {
		t.Fatalf("НайтиНедопустимыеСимволыXML: %v", err)
	}
	if got != int64(3) {
		t.Errorf("позиция повреждённой UTF-8 = %v, ожидалось 3", got)
	}
}

func TestReadXML_BrokenDocumentIsUserError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("ожидалась ошибка на пустом документе")
		}
	}()
	_, _ = builtinReadXML([]any{"   "}, "", 0)
}
