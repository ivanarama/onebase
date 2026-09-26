package query

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Разыменование до ССЫЛОЧНОГО реквизита должно давать колонку с _id:
// «Исполнитель.УчётнаяЗапись» — это учётнаязапись_id у присоединённой таблицы.
// Раньше запрос уходил в SQL с «учётнаязапись» и падал «no such column»,
// причём только на реквизитах-ссылках — выглядело как опечатка в конфигурации.
func TestRefAttrColumnUsesIDColumnForReferenceAttribute(t *testing.T) {
	сотрудник := &metadata.Entity{
		Name: "Пользователи", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "УчётнаяЗапись", Type: "reference:_users", RefEntity: "_users"},
		},
	}
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Исполнитель", Type: "reference:Пользователи", RefEntity: "Пользователи"},
		},
	}
	opts := CompileOpts{Entities: []*metadata.Entity{задача, сотрудник}}
	res, err := Compile(`ВЫБРАТЬ Номер ИЗ Документ.Задача ГДЕ Исполнитель.УчётнаяЗапись = &Кто`, opts)
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !strings.Contains(res.SQL, "ref_исполнитель.учётнаязапись_id") {
		t.Fatalf("ожидалась колонка учётнаязапись_id, получено: %s", res.SQL)
	}
}

// Обычный (не ссылочный) реквизит по-прежнему берётся своим именем.
func TestRefAttrColumnKeepsPlainAttribute(t *testing.T) {
	сотрудник := &metadata.Entity{
		Name: "Пользователи", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Табельный", Type: metadata.FieldTypeString}},
	}
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Исполнитель", Type: "reference:Пользователи", RefEntity: "Пользователи"},
		},
	}
	res, err := Compile(`ВЫБРАТЬ Номер ИЗ Документ.Задача ГДЕ Исполнитель.Табельный = &Кто`,
		CompileOpts{Entities: []*metadata.Entity{задача, сотрудник}})
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !strings.Contains(res.SQL, "ref_исполнитель.табельный") || strings.Contains(res.SQL, "табельный_id") {
		t.Fatalf("простой реквизит изменился: %s", res.SQL)
	}
}
