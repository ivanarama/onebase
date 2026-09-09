package interpreter

import (
	"fmt"
	"strings"
	"testing"

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

func TestXDTOSerializerReadRejectsInvalidTypedValues(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Дата", Type: metadata.FieldTypeDate},
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Контрагент", Type: metadata.FieldTypeString, RefEntity: "Контрагенты"},
		},
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "ДатаСобытия", Type: metadata.FieldTypeDate},
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
	<Контрагент>22222222-2222-2222-2222-222222222222</Контрагент>
	<Строки><ДатаСобытия>2026-09-09T08:15:00</ДатаСобытия></Строки>
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
		{name: "reference field", old: "22222222-2222-2222-2222-222222222222", bad: "не-ссылка", want: `поле "Контрагент"`},
		{name: "table part date", old: "2026-09-09T08:15:00", bad: "31 февраля", want: `поле "Строки.ДатаСобытия"`},
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
