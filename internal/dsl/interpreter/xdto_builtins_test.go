package interpreter

import (
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
