package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// eq_or_empty — та же пара «ссылка ↔ ссылочный источник», что у eq, и ничего
// больше: литерал, служебные поля и чужой тип источника отвергаются.
func TestCheckFormChoiceFilterEqualOrEmpty(t *testing.T) {
	proj := choiceFilterProject([]metadata.FormChoiceCondition{
		{Field: "Направление", Op: metadata.FormChoiceOpEqualOrEmpty, From: "Объект.Направление"},
	})
	if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
		t.Fatalf("valid eq_or_empty rejected: %+v", issues)
	}

	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
		want string
	}{
		{"literal", metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpEqualOrEmpty, Value: boolPointer(true)}, "eq_or_empty"},
		{"service field", metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqualOrEmpty, Value: boolPointer(false)}, "eq_or_empty"},
		{"foreign source", metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpEqualOrEmpty, From: "Объект.Неисправность"}, "несовместимые"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := choiceFilterProject([]metadata.FormChoiceCondition{tc.cond})
			issues := CheckFormChoiceFilter(proj)
			for _, issue := range issues {
				if strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("want diagnostic %q, got %+v", tc.want, issues)
		})
	}
}
