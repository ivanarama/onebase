package metadata

import "testing"

// Системная таблица учётных записей — легальная цель ссылки наравне с
// сущностями конфигурации (issue #1646): валидатор не должен отвергать
// reference:_users как неизвестную сущность, но чужие имена по-прежнему ловит.
func TestValidate_AllowsSystemUsersRefTarget(t *testing.T) {
	entity := &Entity{Name: "Сотрудники", Kind: KindCatalog, Fields: []Field{
		{Name: "Наименование", Type: FieldTypeString},
		{Name: "УчётнаяЗапись", Type: FieldType("reference:" + SystemUsersEntity), RefEntity: SystemUsersEntity},
	}}
	if err := Validate([]*Entity{entity}, nil); err != nil {
		t.Fatalf("reference:_users отвергнут валидатором: %v", err)
	}

	broken := &Entity{Name: "Сотрудники", Kind: KindCatalog, Fields: []Field{
		{Name: "Лид", Type: "reference:НетТакой", RefEntity: "НетТакой"},
	}}
	if err := Validate([]*Entity{broken}, nil); err == nil {
		t.Fatal("неизвестная сущность-цель прошла валидацию")
	}

	consts := []*Constant{{Name: "Куратор", Type: "reference:" + SystemUsersEntity, RefEntity: SystemUsersEntity}}
	if err := ValidateConstants(consts, nil, nil); err != nil {
		t.Fatalf("reference:_users отвергнут у константы: %v", err)
	}
}
