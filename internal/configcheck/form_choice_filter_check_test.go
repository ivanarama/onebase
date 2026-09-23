package configcheck

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"gopkg.in/yaml.v3"
)

func boolPointer(v bool) *bool { return &v }

func choiceFilterProject(conditions []metadata.FormChoiceCondition) *project.Project {
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	fault := &metadata.Entity{
		Name: "Неисправность", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Направление", Type: "reference:Направление", RefEntity: "Направление"},
		},
	}
	request := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: "reference:Направление", RefEntity: "Направление"},
			{Name: "Неисправность", Type: "reference:Неисправность", RefEntity: "Неисправность"},
		},
	}
	request.Forms = []*metadata.FormModule{{
		Name: "Объекта", LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "НаправлениеФормы", TypeRef: "CatalogRef.Направление"}},
		Elements: []*metadata.FormElement{{
			ID: "fault", Name: "ЗаявленнаяНеисправность", Kind: metadata.FormElementField,
			DataPath: "Объект.Неисправность", ChoiceFilter: conditions,
		}},
	}}
	return &project.Project{Entities: []*metadata.Entity{direction, fault, request}}
}

func validChoiceConditions() []metadata.FormChoiceCondition {
	return []metadata.FormChoiceCondition{
		{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление"},
		{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: boolPointer(false)},
	}
}

func TestCheckFormChoiceFilterValidPlanExamples(t *testing.T) {
	proj := choiceFilterProject(validChoiceConditions())
	if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
		t.Fatalf("valid choice_filter: %+v", issues)
	}
	proj.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Направление", Op: metadata.FormChoiceOpEqual, From: "Форма.НаправлениеФормы",
	}}
	if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
		t.Fatalf("valid eq choice_filter: %+v", issues)
	}
}

func TestCheckFormChoiceFilterRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name string
		edit func(*project.Project)
		want string
	}{
		{"missing id", func(p *project.Project) { p.Entities[2].Forms[0].Elements[0].ID = "" }, "стабильный id"},
		{"empty list", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{}
		}, "от 1 до 8"},
		{"duplicate id", func(p *project.Project) {
			form := p.Entities[2].Forms[0]
			form.Elements = append(form.Elements, &metadata.FormElement{ID: "fault", Kind: metadata.FormElementLabel})
		}, "не уникален"},
		{"wrong kind", func(p *project.Project) { p.Entities[2].Forms[0].Elements[0].Kind = metadata.FormElementLabel }, "допустим только"},
		{"too many", func(p *project.Project) {
			el := p.Entities[2].Forms[0].Elements[0]
			el.ChoiceFilter = make([]metadata.FormChoiceCondition, 9)
			for i := range el.ChoiceFilter {
				el.ChoiceFilter[i] = metadata.FormChoiceCondition{Field: "field" + string(rune('a'+i)), Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"}
			}
		}, "допустимо от 1 до 8"},
		{"bare source", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].From = "Направление"
		}, "тот же иерархический"},
		{"deep source", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].From = "Объект.Направление.ID"
		}, "тот же иерархический"},
		{"both sources", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].Value = boolPointer(false)
		}, "ровно одно"},
		{"no source", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].From = ""
		}, "ровно одно"},
		{"unknown operator", func(p *project.Project) { p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].Op = "contains" }, "неизвестный оператор"},
		{"unknown target field", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].Field = "Направлене"
		}, "нет реквизита"},
		{"duplicate target field", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = append(p.Entities[2].Forms[0].Elements[0].ChoiceFilter,
				metadata.FormChoiceCondition{Field: "НАПРАВЛЕНИЕ", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"})
		}, "повторяется"},
		{"literal on reference", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0] = metadata.FormChoiceCondition{Field: "Направление", Op: metadata.FormChoiceOpEqual, Value: boolPointer(false)}
		}, "только для is_folder"},
		{"folder from", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[1] = metadata.FormChoiceCondition{Field: "is_folder", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"}
		}, "требует boolean value"},
		{"nonhierarchical target", func(p *project.Project) { p.Entities[0].Hierarchical = false }, "не ссылается на иерархический"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := choiceFilterProject(validChoiceConditions())
			tt.edit(proj)
			issues := CheckFormChoiceFilter(proj)
			for _, issue := range issues {
				if issue.Code != "form.choice-filter" {
					t.Fatalf("unexpected code %q", issue.Code)
				}
				if strings.Contains(issue.Message, tt.want) {
					return
				}
			}
			t.Fatalf("issues %+v do not contain %q", issues, tt.want)
		})
	}
}

func writeChoiceFilterCheckProject(t *testing.T, dir string, targetHierarchical bool, elementYAML string) {
	t.Helper()
	mkFile(t, filepath.Join(dir, "catalogs", "направление.yaml"), `name: Направление
hierarchical: true
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "catalogs", "плоский.yaml"), `name: Плоский
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "catalogs", "неисправность.yaml"), fmt.Sprintf(`name: Неисправность
hierarchical: %t
fields:
  - {name: Наименование, type: string}
  - {name: Направление, type: "reference:Направление"}
  - {name: Плоский, type: "reference:Плоский"}
`, targetHierarchical))
	mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
  - {name: Направление, type: "reference:Направление"}
  - {name: Плоский, type: "reference:Плоский"}
  - {name: Неисправность, type: "reference:Неисправность"}
  - {name: Комментарий, type: string}
`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: Объекта
  kind: object
  entity: Заявка
attributes:
  - name: НаправлениеФормы
    type: CatalogRef.Направление
elements:
`+elementYAML+"\n")
}

func choiceFilterIssues(result Result) []Issue {
	var out []Issue
	for _, issue := range result.Issues {
		if issue.Code == "form.choice-filter" {
			out = append(out, issue)
		}
	}
	return out
}

func assertNoChoiceFilterLintWarning(t *testing.T, result Result) {
	t.Helper()
	for _, warning := range result.Warnings {
		if warning.Code == "metadata.unvalidated-key" && strings.Contains(warning.Message, "choice_filter") {
			t.Fatalf("поддержанный choice_filter объявлен неизвестным YAML-ключом: %+v", warning)
		}
	}
}

// RunFull is the production entry used by `onebase check`. These fixtures
// prove that both public examples survive the real YAML loader and that the
// checker is actually wired into the user-facing gate (not merely unit-tested
// as an otherwise dead helper).
func TestRunFullChoiceFilterAcceptsPlanExamples(t *testing.T) {
	dir := t.TempDir()
	writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - field: Направление
        op: in_hierarchy
        from: Объект.Направление
      - field: is_folder
        op: eq
        value: false`)
	if result := RunFullWithOptions(dir, Options{Lint: true}); !result.OK || len(choiceFilterIssues(result)) != 0 {
		t.Fatalf("первый пример плана не прошёл onebase check: issues=%+v warnings=%+v", result.Issues, result.Warnings)
	} else {
		assertNoChoiceFilterLintWarning(t, result)
	}

	writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - field: Направление
        op: eq
        from: Форма.НаправлениеФормы`)
	if result := RunFullWithOptions(dir, Options{Lint: true}); !result.OK || len(choiceFilterIssues(result)) != 0 {
		t.Fatalf("второй пример плана не прошёл onebase check: issues=%+v warnings=%+v", result.Issues, result.Warnings)
	} else {
		assertNoChoiceFilterLintWarning(t, result)
	}
}

func TestRunFullChoiceFilterRejectsPublicContractViolations(t *testing.T) {
	tests := []struct {
		name         string
		hierarchical bool
		element      string
		want         string
	}{
		{"missing stable id", true, `  - kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление}]`, "стабильный id"},
		{"duplicate stable id", true, `  - {id: fault, kind: Надпись}
  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление}]`, "не уникален"},
		{"wrong element kind", true, `  - id: fault
    kind: Надпись
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление}]`, "допустим только"},
		{"empty conditions", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: []`, "от 1 до 8"},
		{"too many conditions", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - {field: Направление, op: eq, from: Объект.Направление}
      - {field: a, op: eq, from: Объект.Направление}
      - {field: b, op: eq, from: Объект.Направление}
      - {field: c, op: eq, from: Объект.Направление}
      - {field: d, op: eq, from: Объект.Направление}
      - {field: e, op: eq, from: Объект.Направление}
      - {field: f, op: eq, from: Объект.Направление}
      - {field: g, op: eq, from: Объект.Направление}
      - {field: h, op: eq, from: Объект.Направление}`, "от 1 до 8"},
		{"target is not reference", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Комментарий
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление}]`, "не выбирает ссылку"},
		{"unknown target field", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направлене, op: eq, from: Объект.Направление}]`, "нет реквизита"},
		{"duplicate target field", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - {field: Направление, op: eq, from: Объект.Направление}
      - {field: НАПРАВЛЕНИЕ, op: eq, from: Объект.Направление}`, "повторяется"},
		{"neither source nor value", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq}]`, "ровно одно"},
		{"both source and value", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление, value: false}]`, "ровно одно"},
		{"bare source", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Направление}]`, "явной ссылкой"},
		{"deep source", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление.ID}]`, "явной ссылкой"},
		{"unknown operator", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: contains, from: Объект.Направление}]`, "неизвестный оператор"},
		{"literal for reference", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, value: false}]`, "только для is_folder"},
		{"non-reference condition field", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Наименование, op: eq, from: Объект.Направление}]`, "несовместимые ссылки"},
		{"in hierarchy over flat reference", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Плоский, op: in_hierarchy, from: Объект.Плоский}]`, "не ссылается на иерархический"},
		{"is folder over flat target", false, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: is_folder, op: eq, value: false}]`, "только у иерархического"},
		{"incompatible references", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Плоский}]`, "несовместимые ссылки"},
		{"folder from instead of value", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: is_folder, op: eq, from: Объект.Направление}]`, "boolean value"},
		{"unknown condition key", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление, sql: unsafe}]`, "неизвестный ключ"},
		{"condition is not object", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [bad]`, "должен быть объектом"},
		{"value is not boolean", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: is_folder, op: eq, value: "false"}]`, "должен быть boolean"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeChoiceFilterCheckProject(t, dir, test.hierarchical, test.element)
			result := RunFull(dir)
			for _, issue := range choiceFilterIssues(result) {
				if strings.Contains(issue.Message, test.want) {
					return
				}
			}
			t.Fatalf("onebase check issues %+v do not contain form.choice-filter %q", result.Issues, test.want)
		})
	}
}

func TestFormChoiceConditionYAMLRoundTripKeepsFalseAndOrder(t *testing.T) {
	original := &metadata.FormElement{
		ID: "fault", Kind: metadata.FormElementField, DataPath: "Объект.Неисправность",
		ChoiceFilter: validChoiceConditions(),
	}
	// FormElement.AccessKey is an HTML keyboard mnemonic, not a credential.
	raw, err := yaml.Marshal(original) //nolint:gosec // G117 false positive on the accesskey YAML field.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "value: false") {
		t.Fatalf("explicit false was lost:\n%s", raw)
	}
	var decoded metadata.FormElement
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ChoiceFilter) != 2 || decoded.ChoiceFilter[0].Field != "Направление" || decoded.ChoiceFilter[1].Value == nil || *decoded.ChoiceFilter[1].Value {
		t.Fatalf("round-trip changed conditions: %+v", decoded.ChoiceFilter)
	}
}
