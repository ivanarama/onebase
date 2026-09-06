package storage

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// refByValue повторяет контракт *interpreter.Ref: методы объявлены НА УКАЗАТЕЛЕ.
type refByValue struct {
	uuid string
	name string
}

func (r *refByValue) GetRefUUID() string { return r.uuid }
func (r *refByValue) String() string     { return r.name }

// Ссылка может прийти в поле ЗНАЧЕНИЕМ, а не указателем — например при
// копировании реквизита из одного документа в другой. GetRefUUID объявлен на
// указателе, поэтому такая копия не проходила проверку типа и уезжала в драйвер
// как есть: запись документа падала с «unsupported type … a struct», не называя
// поля. Ссылочная колонка должна получить uuid, строковая — представление
// (так же, как это давно делает регистр).
func TestFieldValueDialect_RefByValue(t *testing.T) {
	d := SQLiteDialect{}
	id := uuid.New()
	val := refByValue{uuid: id.String(), name: "Ремонт бытовой техники"}

	refField := metadata.Field{Name: "Направление", Type: metadata.FieldType("reference:Напр"), RefEntity: "Напр"}
	got, err := fieldValueDialect(d, refField, map[string]any{"Направление": val})
	if err != nil {
		t.Fatal(err)
	}
	if got != id.String() {
		t.Errorf("ссылочная колонка: %v (%T), ожидался uuid %s", got, got, id)
	}

	strField := metadata.Field{Name: "Направление", Type: metadata.FieldTypeString}
	got, err = fieldValueDialect(d, strField, map[string]any{"Направление": val})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Ремонт бытовой техники" {
		t.Errorf("строковая колонка: %v (%T), ожидалось представление", got, got)
	}

	// Указатель по-прежнему работает как раньше.
	got, err = fieldValueDialect(d, refField, map[string]any{"Направление": &val})
	if err != nil {
		t.Fatal(err)
	}
	if got != id.String() {
		t.Errorf("указатель: %v (%T), ожидался uuid %s", got, got, id)
	}
}
