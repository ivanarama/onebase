package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// not_in_hierarchy (#1821) — дополнение in_hierarchy: те же требования к полю
// (ссылка на иерархический справочник или parent_id) и к источнику (from того
// же справочника или ref).
func TestCheckFormChoiceFilterNotInHierarchyValid(t *testing.T) {
	notIn := metadata.FormChoiceOpNotInHierarchy
	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
	}{
		{"ссылочный реквизит, from", metadata.FormChoiceCondition{Field: "Направление", Op: notIn, From: "Объект.Направление"}},
		{"ссылочный реквизит, ref", metadata.FormChoiceCondition{Field: "Направление", Op: notIn, Ref: choiceRefFolder}},
		{"parent_id, from реквизитом формы", metadata.FormChoiceCondition{Field: "parent_id", Op: notIn, From: "Форма.ПапкаНеисправностей"}},
		{"parent_id, ref", metadata.FormChoiceCondition{Field: "parent_id", Op: notIn, Ref: choiceRefFolder}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := parentChoiceProject(tc.cond, metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPointer(false)})
			if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
				t.Fatalf("%+v", issues)
			}
		})
	}
}

func TestCheckFormChoiceFilterNotInHierarchyRejected(t *testing.T) {
	notIn := metadata.FormChoiceOpNotInHierarchy
	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
		want string
	}{
		{"is_folder", metadata.FormChoiceCondition{Field: "is_folder", Op: notIn, Value: boolPointer(false)}, "not_in_hierarchy требует ссылочный field и from"},
		{"булев литерал", metadata.FormChoiceCondition{Field: "Муниципальный", Op: notIn, Value: boolPointer(true)}, "not_in_hierarchy требует ссылочный field и from"},
		{"строковый реквизит", metadata.FormChoiceCondition{Field: "Код", Op: notIn, From: "Объект.Направление"}, "не ссылается на иерархический справочник"},
		{"источник другого справочника", metadata.FormChoiceCondition{Field: "parent_id", Op: notIn, From: "Объект.Направление"}, "должен ссылаться на тот же иерархический справочник Неисправность"},
		{"ref у строкового реквизита", metadata.FormChoiceCondition{Field: "Код", Op: notIn, Ref: choiceRefFolder}, "ref допустим только у ссылочного реквизита"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := CheckFormChoiceFilter(parentChoiceProject(tc.cond))
			for _, issue := range issues {
				if strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("нет замечания %q: %+v", tc.want, issues)
		})
	}
	t.Run("справочник ссылки не иерархический", func(t *testing.T) {
		for _, cond := range []metadata.FormChoiceCondition{
			{Field: "Направление", Op: notIn, From: "Объект.Направление"},
			{Field: "Направление", Op: notIn, Ref: choiceRefFolder},
		} {
			proj := parentChoiceProject(cond)
			proj.Entities[0].Hierarchical = false // Направление
			found := false
			for _, issue := range CheckFormChoiceFilter(proj) {
				found = found || strings.Contains(issue.Message, "не ссылается на иерархический справочник")
			}
			if !found {
				t.Fatalf("%+v: плоский справочник не отклонён", cond)
			}
		}
	})
}

// Через настоящий onebase check по YAML: оператор известен и линтеру, и
// проверке условий; у плоской ссылки — та же ошибка, что у in_hierarchy.
func TestRunFullChoiceFilterNotInHierarchy(t *testing.T) {
	element := func(condition string) string {
		return `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [` + condition + `]`
	}
	t.Run("корректное условие", func(t *testing.T) {
		dir := t.TempDir()
		writeChoiceFilterCheckProject(t, dir, true, element(`{field: Направление, op: not_in_hierarchy, from: Объект.Направление}, {field: parent_id, op: not_in_hierarchy, ref: "`+choiceRefFolder+`"}, {field: is_folder, op: eq, value: false}`))
		result := RunFull(dir)
		if issues := choiceFilterIssues(result); len(issues) != 0 {
			t.Fatalf("%+v", issues)
		}
		for _, issue := range result.Issues {
			if strings.Contains(issue.Message, "not_in_hierarchy") {
				t.Fatalf("оператор не принят: %+v", issue)
			}
		}
	})
	t.Run("плоская ссылка", func(t *testing.T) {
		dir := t.TempDir()
		writeChoiceFilterCheckProject(t, dir, true, element(`{field: Плоский, op: not_in_hierarchy, from: Объект.Плоский}`))
		for _, issue := range choiceFilterIssues(RunFull(dir)) {
			if strings.Contains(issue.Message, "не ссылается на иерархический") {
				return
			}
		}
		t.Fatal("onebase check пропустил not_in_hierarchy по плоскому справочнику")
	})
}
