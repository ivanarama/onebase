package interpreter

import (
	"fmt"
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
		var message string
		switch e := r.(type) {
		case userError:
			message = e.Msg
		case error:
			message = e.Error()
		default:
			message = fmt.Sprint(e)
		}
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
		{"duplicate attributes", `<a x="1" x="2"/>`, "повторяющийся атрибут"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireXMLUserError(t, tc.want, func() {
				_, _ = builtinReadXML([]any{tc.doc}, "", 0)
			})
		})
	}
}

func TestReadXML_RejectsUnrepresentableContent(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"mixed content", `<a>до<b/>после</a>`, "смешанное содержимое"},
		{"non XML whitespace mixed content", "<a>\u00a0<b/></a>", "смешанное содержимое"},
		{"default namespace", `<a xmlns="urn:test"/>`, "пространства имён"},
		{"prefixed namespace", `<p:a xmlns:p="urn:test"/>`, "пространства имён"},
		{"comment", `<a><!-- важный комментарий --></a>`, "комментарии"},
		{"directive", `<!DOCTYPE a><a/>`, "директивы"},
		{"processing instruction", `<?target value?><a/>`, "инструкции обработки"},
		{"xml declaration", `<?xml version="1.0"?><a/>`, "инструкции обработки"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireXMLUserError(t, tc.want, func() {
				_, _ = builtinReadXML([]any{tc.doc}, "", 0)
			})
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
	for _, name := range []string{"", "1root", "ns:root", `a></a><evil`} {
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
		{"namespace attribute", "пространства имён", func(s *Struct) {
			s.Set(xmlFieldAttrs, &Map{keys: []any{"xmlns"}, vals: []any{"urn:test"}})
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
		{time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC), "2026-08-10T12:30:00"},
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
}

// XMLСтрока и XMLЗначение должны быть обратны друг другу — иначе обмен данными
// молча теряет значения на круге «выгрузил → загрузил».
func TestXMLStringValue_AreInverse(t *testing.T) {
	for _, c := range []struct {
		typeName string
		in       any
	}{
		{"Число", decimal.RequireFromString("-17.25")},
		{"Булево", false},
		{"Дата", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
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
