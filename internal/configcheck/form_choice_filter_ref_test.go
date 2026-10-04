package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// Постоянная ссылка ref (#1820): третий взаимоисключающий источник условия.
// onebase check проверяет формат UUID и сочетание field/op; существование
// записи — данные базы, её проверяет рантайм (fail-closed).
const choiceRefFolder = "d4ba641c-70ae-4632-bf99-035f4caaa0af"

func TestCheckFormChoiceFilterRefValid(t *testing.T) {
	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
	}{
		{"parent_id in_hierarchy", metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: choiceRefFolder}},
		{"parent_id eq", metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder}},
		{"ссылочный реквизит eq", metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder}},
		{"ссылочный реквизит eq_or_empty", metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpEqualOrEmpty, Ref: choiceRefFolder}},
		{"ссылочный реквизит in_hierarchy", metadata.FormChoiceCondition{Field: "Группа", Op: metadata.FormChoiceOpInHierarchy, Ref: choiceRefFolder}},
		{"UUID с пробелами по краям", metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: " " + choiceRefFolder + " "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := choiceFilterProject([]metadata.FormChoiceCondition{
				tc.cond,
				{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPointer(false)},
			})
			if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
				t.Fatalf("%+v", issues)
			}
		})
	}
}

func TestCheckFormChoiceFilterRefRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
		edit func(*project.Project)
		want string
	}{
		{"битый UUID", metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: "папка-рабочие"}, nil, "не является UUID"},
		{"нулевой UUID", metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpInHierarchy, Ref: "00000000-0000-0000-0000-000000000000"}, nil, "нулевой UUID"},
		{"ref вместе с from", metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder, From: "Объект.Направление"}, nil, "ровно одно из from, value и ref"},
		{"ref вместе с value", metadata.FormChoiceCondition{Field: "Муниципальный", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder, Value: boolPointer(true)}, nil, "ровно одно из from, value и ref"},
		{"ref у is_folder", metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder}, nil, "ref допустим только у ссылочного реквизита"},
		{"ref у булева реквизита", metadata.FormChoiceCondition{Field: "Муниципальный", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder}, nil, "ref допустим только у ссылочного реквизита"},
		{"ref у строкового реквизита", metadata.FormChoiceCondition{Field: "Код", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder}, nil, "ref допустим только у ссылочного реквизита"},
		{"in_hierarchy по плоскому справочнику", metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, Ref: choiceRefFolder},
			func(p *project.Project) { p.Entities[0].Hierarchical = false }, "не ссылается на иерархический справочник"},
		{"parent_id у неиерархического справочника", metadata.FormChoiceCondition{Field: "parent_id", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder},
			func(p *project.Project) { p.Entities[1].Hierarchical = false }, "parent_id допустим только у иерархического справочника"},
		{"неизвестный оператор", metadata.FormChoiceCondition{Field: "parent_id", Op: "not_in_hierarchy", Ref: choiceRefFolder}, nil, "неизвестный оператор"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := choiceFilterProject([]metadata.FormChoiceCondition{tc.cond})
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

// Через настоящий onebase check по YAML: пустой ref ловится по самому YAML
// (в структуре он неотличим от отсутствующего ключа), ключ ref известен и
// линтеру, и проверке условий.
func TestRunFullChoiceFilterRef(t *testing.T) {
	element := func(condition string) string {
		return `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [` + condition + `]`
	}
	t.Run("корректный ref", func(t *testing.T) {
		dir := t.TempDir()
		writeChoiceFilterCheckProject(t, dir, true, element(`{field: parent_id, op: in_hierarchy, ref: "`+choiceRefFolder+`"}, {field: is_folder, op: eq, value: false}`))
		result := RunFull(dir)
		if issues := choiceFilterIssues(result); len(issues) != 0 {
			t.Fatalf("%+v", issues)
		}
		for _, issue := range result.Issues {
			if strings.Contains(issue.Message, `"ref"`) {
				t.Fatalf("ключ ref неизвестен линтеру: %+v", issue)
			}
		}
	})
	for _, tc := range []struct {
		name, condition, want string
	}{
		{"пустой ref", `{field: parent_id, op: in_hierarchy, ref: ""}`, "ref пуст"},
		{"ref не строка", `{field: parent_id, op: in_hierarchy, ref: 42}`, "ref должен быть строкой"},
		{"нулевой ref", `{field: parent_id, op: in_hierarchy, ref: "00000000-0000-0000-0000-000000000000"}`, "нулевой UUID"},
		{"битый ref", `{field: parent_id, op: in_hierarchy, ref: "d4ba641c"}`, "не является UUID"},
		{"ref и from", `{field: Направление, op: eq, from: Объект.Направление, ref: "` + choiceRefFolder + `"}`, "ровно одно из from, value и ref"},
		{"ref и value", `{field: Муниципальный, op: eq, value: true, ref: "` + choiceRefFolder + `"}`, "ровно одно из from, value и ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeChoiceFilterCheckProject(t, dir, true, element(tc.condition))
			result := RunFull(dir)
			for _, issue := range choiceFilterIssues(result) {
				if strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("onebase check: нет замечания %q: %+v", tc.want, result.Issues)
		})
	}
}
