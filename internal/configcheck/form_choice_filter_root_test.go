package configcheck

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func TestRunFullChoiceFilterIsRootAttribute(t *testing.T) {
	for _, hierarchical := range []bool{false, true} {
		for _, tc := range []struct{ fieldType, condition string }{
			{"bool", "{field: IS_ROOT, op: eq, value: true}"},
			{"bool", "{field: is_root, op: eq, value: false}"},
			{"reference:Направление", "{field: is_root, op: eq, from: Объект.Направление}"},
			{"reference:Направление", "{field: is_root, op: not_in_hierarchy, ref: " + choiceRefFolder + "}"},
			{"string", "{field: is_root, op: eq, from: Объект.Направление.Наименование}"},
		} {
			t.Run(fmt.Sprintf("%t/%s/%s", hierarchical, tc.fieldType, tc.condition), func(t *testing.T) {
				dir := t.TempDir()
				writeChoiceFilterCheckProject(t, dir, hierarchical, "  - id: fault\n    kind: ПолеВвода\n    data_path: Объект.Неисправность\n    choice_filter: ["+tc.condition+"]")
				root, err := os.OpenRoot(dir)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := root.Close(); err != nil {
						t.Error(err)
					}
				})
				const path = "catalogs/неисправность.yaml"
				data, err := root.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, []byte("  - {name: is_root, type: \""+tc.fieldType+"\"}\n")...)
				if err := root.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				if result := RunFull(dir); !result.OK {
					t.Fatalf("configured is_root rejected: %+v", result.Issues)
				}
			})
		}
	}
}

func TestCheckFormChoiceFilterIsRoot(t *testing.T) {
	for _, value := range []bool{true, false} {
		proj := choiceFilterProject([]metadata.FormChoiceCondition{
			{Field: "is_root", Op: metadata.FormChoiceOpEqual, Value: boolPointer(value)},
			{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPointer(true)},
		})
		if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
			t.Fatalf("is_root=%v: %+v", value, issues)
		}
	}
	for _, tc := range []struct {
		name string
		cond metadata.FormChoiceCondition
		edit func(*project.Project)
		want string
	}{
		{"плоский справочник", metadata.FormChoiceCondition{Field: "is_root", Op: metadata.FormChoiceOpEqual, Value: boolPointer(true)}, func(p *project.Project) { p.Entities[1].Hierarchical = false }, "is_root допустим только у иерархического справочника"},
		{"from вместо value", metadata.FormChoiceCondition{Field: "is_root", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"}, nil, "is_root требует boolean value"},
		{"ref вместо value", metadata.FormChoiceCondition{Field: "is_root", Op: metadata.FormChoiceOpEqual, Ref: choiceRefFolder}, nil, "ref допустим только у ссылочного реквизита"},
		{"исключение поддерева", metadata.FormChoiceCondition{Field: "is_root", Op: metadata.FormChoiceOpNotInHierarchy, From: "Объект.Направление"}, nil, "not_in_hierarchy требует ссылочный field и from"},
		{"неверный оператор", metadata.FormChoiceCondition{Field: "is_root", Op: metadata.FormChoiceOpInHierarchy, Value: boolPointer(true)}, nil, "in_hierarchy требует ссылочный field и from"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proj := choiceFilterProject([]metadata.FormChoiceCondition{tc.cond})
			if tc.edit != nil {
				tc.edit(proj)
			}
			for _, issue := range CheckFormChoiceFilter(proj) {
				if strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("нет замечания %q", tc.want)
		})
	}
}

func TestRunFullChoiceFilterIsRoot(t *testing.T) {
	element := func(condition string) string {
		return "  - id: fault\n    kind: ПолеВвода\n    data_path: Объект.Неисправность\n    choice_filter: [" + condition + "]"
	}
	for _, tc := range []struct{ name, condition, want string }{
		{"корень", "{field: is_root, op: eq, value: true}, {field: is_folder, op: eq, value: true}", ""},
		{"совместный отбор", "{field: is_root, op: eq, value: false}, {field: parent_id, op: not_in_hierarchy, ref: " + choiceRefFolder + "}", ""},
		{"неверное исключение корня", "{field: is_root, op: not_in_hierarchy, from: Объект.Направление}", "not_in_hierarchy требует ссылочный field и from"},
		{"вложенные", "{field: is_root, op: eq, value: false}", ""},
		{"неверный источник", "{field: is_root, op: eq, from: Объект.Направление}", "is_root требует boolean value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeChoiceFilterCheckProject(t, dir, true, element(tc.condition))
			issues := choiceFilterIssues(RunFull(dir))
			if tc.want == "" {
				if len(issues) != 0 {
					t.Fatalf("onebase check: %+v", issues)
				}
				return
			}
			for _, issue := range issues {
				if strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("onebase check: нет замечания %q: %+v", tc.want, issues)
		})
	}
}
