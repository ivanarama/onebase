package configcheck

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// Запрос отбора формы списка проверяется заранее, как запрос виджета:
// сломанный рантайм закрывает пустым списком, и без check это выглядело бы
// как «у меня нет записей».
func TestCheckQueriesExecutable_ListFormFilterQuery(t *testing.T) {
	заявка := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Автор", Type: metadata.FieldTypeString}},
	}
	заявка.Forms = []*metadata.FormModule{{
		Name: "ФормаСписка", Kind: "list", EntityName: заявка.Name,
		ListFilter: []metadata.FormListCondition{
			{Query: "ВЫБРАТЬ З.Ссылка ИЗ Справочник.Заявка КАК З ГДЕ З.Автор = &ТекущийПользователь"},
		},
	}}
	var checked []string
	issues := CheckQueriesExecutable(&project.Project{Entities: []*metadata.Entity{заявка}}, func(sqlText string) error {
		checked = append(checked, sqlText)
		return errors.New("no such column")
	})
	if len(checked) != 1 || len(issues) != 1 || !strings.Contains(issues[0].Kind, "исполнение запроса отбора") {
		t.Fatalf("запрос отбора не прошёл проверку исполнения: checked=%d issues=%+v", len(checked), issues)
	}
}
