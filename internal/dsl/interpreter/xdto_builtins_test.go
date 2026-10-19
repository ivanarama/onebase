package interpreter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

type xdtoTestRegistry struct {
	entity *metadata.Entity
}

func (r xdtoTestRegistry) GetEntity(name string) *metadata.Entity {
	if r.entity != nil && strings.EqualFold(r.entity.Name, name) {
		return r.entity
	}
	return nil
}

func TestXDTOSerializerRoundTripPreservesRecordFlags(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	serializer := NewXDTOSerializer(xdtoTestRegistry{entity: ent})
	input := `<DocumentObject.Заказ xmlns="http://v8.1c.ru/8.1/data/enterprise/current-config">
	<Ref>11111111-1111-1111-1111-111111111111</Ref>
	<DeletionMark>true</DeletionMark>
	<Date>2026-09-08T12:30:00</Date>
	<Number>000001</Number>
	<Posted>true</Posted>
</DocumentObject.Заказ>`

	obj, ok := serializer.CallMethod("ПрочитатьXML", []any{input}).(*runtime.Object)
	if !ok {
		t.Fatal("ПрочитатьXML не вернул объект конфигурации")
	}
	output, ok := serializer.CallMethod("ЗаписатьXML", []any{obj}).(string)
	if !ok {
		t.Fatal("ЗаписатьXML не вернул строку")
	}
	for _, flag := range []string{"<DeletionMark>true</DeletionMark>", "<Posted>true</Posted>"} {
		if !strings.Contains(output, flag) {
			t.Errorf("после публичного round-trip потерян %s:\n%s", flag, output)
		}
	}
}

func TestXDTOSerializerReadPreservesNumbers(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	serializer := NewXDTOSerializer(xdtoTestRegistry{entity: ent})
	input := `<DocumentObject.Заказ xmlns="http://v8.1c.ru/8.1/data/enterprise/current-config">
	<Ref>11111111-1111-1111-1111-111111111111</Ref>
	<DeletionMark>false</DeletionMark>
	<Posted>false</Posted>
	<Сумма>12.50</Сумма>
	<Строки><Количество>3.25</Количество></Строки>
</DocumentObject.Заказ>`

	obj, ok := serializer.CallMethod("ПрочитатьXML", []any{input}).(*runtime.Object)
	if !ok {
		t.Fatal("ПрочитатьXML не вернул объект конфигурации")
	}
	assertDecimal := func(name string, got any, want string) {
		t.Helper()
		value, ok := got.(decimal.Decimal)
		if !ok {
			t.Fatalf("%s: получен %T, ожидается decimal.Decimal", name, got)
		}
		if !value.Equal(decimal.RequireFromString(want)) {
			t.Fatalf("%s: получено %s, ожидается %s", name, value, want)
		}
	}
	assertDecimal("Сумма", obj.Get("Сумма"), "12.50")
	if len(obj.TablePartRows["Строки"]) != 1 {
		t.Fatalf("Строки: получено %d строк, ожидается 1", len(obj.TablePartRows["Строки"]))
	}
	assertDecimal("Строки.Количество", obj.TablePartRows["Строки"][0]["Количество"], "3.25")
}

func TestXDTOSerializerReadRejectsInvalidTypedValues(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
			{Name: "Контрагент", Type: metadata.FieldTypeString, RefEntity: "Контрагенты"},
		},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "ДатаСобытия", Type: metadata.FieldTypeDate},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	serializer := NewXDTOSerializer(xdtoTestRegistry{entity: ent})
	valid := `<DocumentObject.Заказ xmlns="http://v8.1c.ru/8.1/data/enterprise/current-config">
	<Ref>11111111-1111-1111-1111-111111111111</Ref>
	<DeletionMark>false</DeletionMark>
	<Date>2026-09-08T12:30:00</Date>
	<Number>000001</Number>
	<Posted>true</Posted>
	<Активен>true</Активен>
	<Сумма>12.50</Сумма>
	<Контрагент>22222222-2222-2222-2222-222222222222</Контрагент>
	<Строки><ДатаСобытия>2026-09-09T08:15:00</ДатаСобытия><Количество>3.25</Количество></Строки>
</DocumentObject.Заказ>`

	tests := []struct {
		name string
		old  string
		bad  string
		want string
	}{
		{name: "Ref", old: "11111111-1111-1111-1111-111111111111", bad: "не-uuid", want: `поле "Ref"`},
		{name: "DeletionMark", old: "<DeletionMark>false</DeletionMark>", bad: "<DeletionMark>нет</DeletionMark>", want: `поле "DeletionMark"`},
		{name: "Date", old: "2026-09-08T12:30:00", bad: "вчера", want: `поле "Date"`},
		{name: "Posted", old: "<Posted>true</Posted>", bad: "<Posted>да</Posted>", want: `поле "Posted"`},
		{name: "boolean field", old: "<Активен>true</Активен>", bad: "<Активен>да</Активен>", want: `поле "Активен"`},
		{name: "number field", old: "<Сумма>12.50</Сумма>", bad: "<Сумма>не-число</Сумма>", want: `поле "Сумма"`},
		{name: "reference field", old: "22222222-2222-2222-2222-222222222222", bad: "не-ссылка", want: `поле "Контрагент"`},
		{name: "table part date", old: "2026-09-09T08:15:00", bad: "31 февраля", want: `поле "Строки.ДатаСобытия"`},
		{name: "table part number", old: "<Количество>3.25</Количество>", bad: "<Количество>много</Количество>", want: `поле "Строки.Количество"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(valid, tc.old, tc.bad, 1)
			message := readXMLError(t, serializer, input)
			if !strings.Contains(message, tc.want) {
				t.Fatalf("ошибка не содержит контекст поля %s: %s", tc.want, message)
			}
		})
	}
}

func readXMLError(t *testing.T, serializer *XDTOSerializer, text string) (message string) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("ПрочитатьXML принял повреждённое типизированное значение")
		}
		switch err := value.(type) {
		case userError:
			message = err.Msg
		default:
			t.Fatalf("ПрочитатьXML вызвал не пользовательскую ошибку: %s", fmt.Sprint(value))
		}
	}()
	serializer.CallMethod("ПрочитатьXML", []any{text})
	return ""
}

func runXDTOScript(t *testing.T, ent *metadata.Entity, input, script string) (*runtime.Object, error) {
	t.Helper()
	prog, err := parser.New(lexer.New("Процедура Выполнить()\n"+script+"\nКонецПроцедуры", "xdto-test.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.NewObject("Результат", metadata.KindDocument)
	err = New().Run(prog.Procedures[0], result, map[string]any{
		"Вход":             input,
		"СериализаторXDTO": NewXDTOSerializer(xdtoTestRegistry{entity: ent}),
	})
	return result, err
}

func TestXDTOSerializerDSLTablePartEditRoundTrip(t *testing.T) {
	ent := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		}}},
	}
	input := `<DocumentObject.Заказ><Ref>11111111-1111-1111-1111-111111111111</Ref><Строки><Количество>1</Количество><Комментарий>старое</Комментарий></Строки></DocumentObject.Заказ>`
	result, err := runXDTOScript(t, ent, input, `
 Док = СериализаторXDTO.ПрочитатьXML(Вход);
 Существующая = Док.Строки.Получить(0);
 Существующая.кОлИчЕсТвО = 2.5;
 Существующая.комментарий = "изменено";
 Стр = Док.Строки.Добавить();
 Стр.КОЛИЧЕСТВО = 3.25;
 Стр.Комментарий = "добавлено & сохранено";
 ЭтотОбъект.XML = СериализаторXDTO.ЗаписатьXML(Док);
 Повтор = СериализаторXDTO.ПрочитатьXML(ЭтотОбъект.XML);
 Стр2 = Повтор.Строки.Получить(1);
 ЭтотОбъект.Количество = Стр2.Количество;
 ЭтотОбъект.Комментарий = Стр2.Комментарий;
 ЭтотОбъект.Строк = Повтор.Строки.Количество();`)
	if err != nil {
		t.Fatal(err)
	}
	output := result.Get("XML").(string)
	for _, want := range []string{"<Количество>2.5</Количество>", "<Комментарий>изменено</Комментарий>", "<Количество>3.25</Количество>", "<Комментарий>добавлено &amp; сохранено</Комментарий>"} {
		if !strings.Contains(output, want) {
			t.Errorf("XML lost %s:\n%s", want, output)
		}
	}
	n, ok := result.Get("Количество").(decimal.Decimal)
	if !ok || !n.Equal(decimal.RequireFromString("3.25")) {
		t.Errorf("round-trip quantity = %v", result.Get("Количество"))
	}
	if result.Get("Комментарий") != "добавлено & сохранено" || result.Get("Строк") != float64(2) {
		t.Errorf("round-trip result = %+v", result.Fields)
	}
}

func TestXDTOSerializerDSLRootBoundary(t *testing.T) {
	ent := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument}
	id := "11111111-1111-1111-1111-111111111111"
	root := `<DocumentObject.Заказ><Ref>` + id + `</Ref></DocumentObject.Заказ>`
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"root", root, true},
		{"BOM and XML declaration", "\uFEFF<?xml version=\"1.0\"?>\n" + root, true},
		{"trailing whitespace and comments", root + " \n<!-- end --><?note ok?>", true},
		{"unknown wrapper", strings.Replace(root, "</DocumentObject.Заказ>", "<Unknown><Ref>22222222-2222-2222-2222-222222222222</Ref></Unknown></DocumentObject.Заказ>", 1), true},
		{"external ref", root + "<Ref>22222222-2222-2222-2222-222222222222</Ref>", false},
		{"second object", root + root, false},
		{"trailing text", root + "garbage", false},
		{"trailing non-XML whitespace", root + "\u00a0", false},
		{"leading text", "garbage" + root, false},
		{"trailing directive", root + "<!DOCTYPE Ref>", false},
		{"unclosed root", strings.TrimSuffix(root, "</DocumentObject.Заказ>"), false},
		{"nested ref", strings.Replace(root, "<Ref>"+id+"</Ref>", "<Ref><Ref>"+id+"</Ref></Ref>", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := runXDTOScript(t, ent, tc.input, `
 Док = СериализаторXDTO.ПрочитатьXML(Вход);
 ЭтотОбъект.XML = СериализаторXDTO.ЗаписатьXML(Док);`)
			if !tc.valid {
				if err == nil {
					t.Fatal("ПрочитатьXML accepted invalid XML")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Get("XML").(string); !strings.Contains(got, "<Ref>"+id+"</Ref>") {
				t.Fatalf("XML lost original Ref: %s", got)
			}
		})
	}
}

func TestXDTOSerializerDSLRefIdentityAndNamespaces(t *testing.T) {
	const ns = "http://v8.1c.ru/8.1/data/enterprise/current-config"
	const id = "11111111-1111-1111-1111-111111111111"
	const other = "22222222-2222-2222-2222-222222222222"
	ref := "<Ref>" + id + "</Ref>"
	foreign := `<ext:Ref xmlns:ext="urn:foreign">` + other + `</ext:Ref>`
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		ent := &metadata.Entity{Name: "Обмен", Kind: kind, Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}}}}
		root := "CatalogObject.Обмен"
		if kind == metadata.KindDocument {
			root = "DocumentObject.Обмен"
		}
		for _, tc := range []struct {
			name, body, want, errorText string
			prefixed                    bool
		}{
			{name: "one Ref", body: ref, want: id},
			{name: "zero Ref", body: "<Ref>00000000-0000-0000-0000-000000000000</Ref>", want: "00000000-0000-0000-0000-000000000000"},
			{name: "missing Ref", errorText: "Ref"},
			{name: "invalid Ref", body: "<Ref>bad-uuid</Ref>", errorText: "UUID"},
			{name: "same duplicate Ref", body: ref + ref, errorText: "Ref"},
			{name: "different duplicate Ref", body: ref + "<Ref>" + other + "</Ref>", errorText: "Ref"},
			{name: "foreign before", body: foreign + ref, want: id},
			{name: "foreign after", body: ref + foreign, want: id},
			{name: "foreign only", body: foreign, errorText: "Ref"},
			{name: "nested only", body: "<Unknown>" + ref + "</Unknown>", errorText: "Ref"},
			{name: "equivalent prefix", body: `<p:Ref xmlns:p="` + ns + `">` + id + `</p:Ref>`, want: id, prefixed: true},
			{name: "foreign state and fields", body: ref + `<DeletionMark>true</DeletionMark><Posted>true</Posted><Телефон>visible</Телефон><Строки><Текст>row</Текст><ext:Текст xmlns:ext="urn:foreign"><Ref>ignored</Ref></ext:Текст></Строки><ext:DeletionMark xmlns:ext="urn:foreign">false</ext:DeletionMark><ext:Posted xmlns:ext="urn:foreign">false</ext:Posted><ext:Телефон xmlns:ext="urn:foreign">secret</ext:Телефон><ext:Строки xmlns:ext="urn:foreign"><Текст>extra</Текст></ext:Строки>`, want: id},
		} {
			for _, namespace := range []string{"", ns} {
				if tc.prefixed && namespace == "" {
					continue
				}
				t.Run(string(kind)+"/"+tc.name+"/"+namespace, func(t *testing.T) {
					attr := ""
					if namespace != "" {
						attr = ` xmlns="` + namespace + `"`
					}
					input := "<" + root + attr + ">" + tc.body + "</" + root + ">"
					result, err := runXDTOScript(t, ent, input, `Об = СериализаторXDTO.ПрочитатьXML(Вход); ЭтотОбъект.XML = СериализаторXDTO.ЗаписатьXML(Об);`)
					if tc.errorText != "" {
						if err == nil || !strings.Contains(err.Error(), tc.errorText) {
							t.Fatalf("expected %s error, got %v", tc.errorText, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					xml := result.Get("XML").(string)
					if !strings.Contains(xml, "<Ref>"+tc.want+"</Ref>") {
						t.Fatalf("identity changed: %s", xml)
					}
					if tc.name == "foreign state and fields" {
						for _, want := range []string{"<DeletionMark>true</DeletionMark>", "<Телефон>visible</Телефон>", "<Текст>row</Текст>"} {
							if !strings.Contains(xml, want) {
								t.Fatalf("lost %s: %s", want, xml)
							}
						}
						if strings.Contains(xml, "secret") || strings.Contains(xml, "extra") || strings.Count(xml, "<Строки>") != 1 {
							t.Fatalf("foreign namespace changed object: %s", xml)
						}
						if kind == metadata.KindDocument && !strings.Contains(xml, "<Posted>true</Posted>") {
							t.Fatalf("foreign Posted changed state: %s", xml)
						}
					}
				})
			}
		}
	}
}
