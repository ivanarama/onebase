package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// Хвост ревью #2172: ошибочное условие form.filter проходило onebase check, а
// список падал на сервере. Теперь check называет неизвестное поле (в том числе
// реквизит связанной сущности) и неподдерживаемый оператор.
func TestCheckFormListFilters(t *testing.T) {
	user := &metadata.Entity{Name: "Пользователи", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "УчётнаяЗапись", Type: "reference:_users", RefEntity: "_users"}}}
	proj := func(conds ...metadata.FormListCondition) *project.Project {
		task := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Hierarchical: true, Fields: []metadata.Field{
			{Name: "Автор", Type: metadata.FieldTypeString},
			{Name: "Инициатор", Type: "reference:Пользователи", RefEntity: "Пользователи"},
		}}
		task.Forms = []*metadata.FormModule{{Name: "ФормаСписка", Kind: "list", EntityName: task.Name,
			LayoutKind: metadata.FormLayoutManaged, ListFilter: conds}}
		return &project.Project{Entities: []*metadata.Entity{task, user}}
	}
	for _, ok := range [][]metadata.FormListCondition{
		{{Field: "Автор", Value: metadata.ListFilterCurrentUser}},
		{{Field: "Инициатор.УчётнаяЗапись", Value: metadata.ListFilterCurrentUser, Roles: []string{"Оператор"}}},
		{{Field: "Автор", Op: "ne", Value: "x"}, {Field: "is_folder", Op: "empty"}},
	} {
		if issues := CheckFormListFilters(proj(ok...)); len(issues) != 0 {
			t.Fatalf("корректный filter %+v отклонён: %+v", ok, issues)
		}
	}
	for _, tc := range []struct {
		cond metadata.FormListCondition
		want string
	}{
		{metadata.FormListCondition{Field: "MissingAuthor", Value: "me"}, "не найдено"},
		{metadata.FormListCondition{Field: "Автор", Op: "gt", Value: "me"}, "не поддерживается"},
		{metadata.FormListCondition{Field: "Инициатор.Нет", Value: "me"}, "не найден у Пользователи"},
		{metadata.FormListCondition{Field: "Автор.Поле", Value: "me"}, "не ссылочный"},
		{metadata.FormListCondition{Field: "Инициатор.УчётнаяЗапись.Login", Value: "me"}, "одно разыменование"},
	} {
		issues := CheckFormListFilters(proj(tc.cond))
		if len(issues) != 1 || !strings.Contains(issues[0].Message, tc.want) || issues[0].Code != "form.list-filter" {
			t.Fatalf("%+v: %+v, want %q", tc.cond, issues, tc.want)
		}
	}
}
