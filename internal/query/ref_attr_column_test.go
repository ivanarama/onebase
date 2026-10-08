package query

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Случай из заявки #1784: ссылочный реквизит на системную таблицу учётных
// записей. «Исполнитель.УчётнаяЗапись» — это учётнаязапись_id присоединённой
// таблицы; раньше SQL уходил с «учётнаязапись» и падал «no such column».
func TestRefAttrColumnUsesIDColumnForUsersReference(t *testing.T) {
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
	res, err := Compile(`ВЫБРАТЬ Номер ИЗ Документ.Задача ГДЕ Исполнитель.УчётнаяЗапись = &Кто`,
		CompileOpts{Entities: []*metadata.Entity{задача, сотрудник}})
	if err != nil {
		t.Fatalf("компиляция: %v", err)
	}
	if !strings.Contains(res.SQL, "ref_исполнитель.учётнаязапись_id") {
		t.Fatalf("ожидалась колонка учётнаязапись_id, получено: %s", res.SQL)
	}
}
