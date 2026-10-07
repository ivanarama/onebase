package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// parent_id (#1819) — служебная ссылка иерархического справочника на себя:
// onebase check проверяет его как ссылочный реквизит, источник которого —
// тот же справочник. Папку неисправностей форма получает реквизитом формы.
func parentChoiceProject(conditions ...metadata.FormChoiceCondition) *project.Project {
	proj := choiceFilterProject(conditions)
	form := proj.Entities[2].Forms[0]
	form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: "ПапкаНеисправностей", TypeRef: "CatalogRef.Неисправность"})
	return proj
}

func TestCheckFormChoiceFilterParentIDValid(t *testing.T) {
	for _, op := range []metadata.FormChoiceOperator{metadata.FormChoiceOpInHierarchy, metadata.FormChoiceOpEqual} {
		proj := parentChoiceProject(
			metadata.FormChoiceCondition{Field: "parent_id", Op: op, From: "Форма.ПапкаНеисправностей"},
			metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPointer(false)},
		)
		if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
			t.Fatalf("parent_id %s: %+v", op, issues)
		}
	}
}

func TestCheckFormChoiceFilterParentIDRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
		edit func(*project.Project)
		want string
	}{
		{
			name: "неиерархический справочник",
			cond: metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, From: "Форма.ПапкаНеисправностей"},
			edit: func(p *project.Project) { p.Entities[1].Hierarchical = false },
			want: "parent_id допустим только у иерархического справочника",
		},
		{
			name: "без источника — литерал",
			cond: metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpEqual, Value: boolPointer(true)},
			want: "литерал value допустим",
		},
		{
			name: "in_hierarchy от другого справочника",
			cond: metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление"},
			want: "должен ссылаться на тот же иерархический справочник Неисправность",
		},
		{
			name: "eq от другого справочника",
			cond: metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"},
			want: "eq сравнивает несовместимые ссылки Неисправность.parent_id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := parentChoiceProject(tc.cond)
			if tc.edit != nil {
				tc.edit(proj)
			}
			issues := CheckFormChoiceFilter(proj)
			for _, issue := range issues {
				if strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("нет замечания %q: %+v", tc.want, issues)
		})
	}
}
