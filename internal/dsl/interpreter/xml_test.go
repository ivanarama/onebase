package interpreter

import (
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

// Главное свойство пары: документ переживает круг чтение → запись.
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
	if out != doc {
		t.Errorf("круг не сошёлся:\n получено %s\n ожидалось %s", out, doc)
	}
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
}

func TestReadXML_BrokenDocumentIsUserError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("ожидалась ошибка на пустом документе")
		}
	}()
	_, _ = builtinReadXML([]any{"   "}, "", 0)
}
