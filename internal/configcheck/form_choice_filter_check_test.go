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
	faultGroup := &metadata.Entity{
		Name: "ГруппаНеисправностей", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ГруппаНеисправностей", Type: "reference:ГруппаНеисправностей", RefEntity: "ГруппаНеисправностей"},
			{Name: "Код", Type: metadata.FieldTypeString},
		},
	}
	fault := &metadata.Entity{
		Name: "Неисправность", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Направление", Type: "reference:Направление", RefEntity: "Направление"},
			{Name: "Группа", Type: "reference:ГруппаНеисправностей", RefEntity: "ГруппаНеисправностей"},
			{Name: "Муниципальный", Type: metadata.FieldTypeBool},
			{Name: "Код", Type: metadata.FieldTypeString},
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
	return &project.Project{Entities: []*metadata.Entity{direction, fault, request, faultGroup}}
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
	// План 183, B1: «неисправности той группы, что указана у направления» —
	// один переход по ссылке вместо реквизита-посредника в справочнике.
	proj.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Группа", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.ГруппаНеисправностей",
	}}
	if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
		t.Fatalf("valid deep source choice_filter: %+v", issues)
	}
	// Булев литерал: «только немуниципальные».
	proj.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Муниципальный", Op: metadata.FormChoiceOpEqual, Value: boolPointer(false),
	}}
	if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
		t.Fatalf("valid boolean literal choice_filter: %+v", issues)
	}
	// Строковый конец пути: строковый реквизит цели равен строковому
	// реквизиту записи по ссылке (ВладелецКод дома = ИД улицы).
	proj.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
		Field: "Код", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Код",
	}}
	if issues := CheckFormChoiceFilter(proj); len(issues) != 0 {
		t.Fatalf("valid string deep source choice_filter: %+v", issues)
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
		}, "не более одного перехода"},
		{"source deeper than one hop", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].From = "Объект.Направление.ГруппаНеисправностей.Наименование"
		}, "не более одного перехода"},
		{"deep source unknown attribute", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].From = "Объект.Направление.ID"
		}, "нет реквизита"},
		{"deep source is not a reference", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter[0].From = "Объект.Направление.Наименование"
		}, "не ссылочный"},
		{"reference field with string deep source", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
				Field: "Группа", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Код",
			}}
		}, "не ссылочный"},
		{"string field with reference source", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
				Field: "Код", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление",
			}}
		}, "несовместимые ссылки"},
		{"string field with reference deep attribute", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
				Field: "Код", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.ГруппаНеисправностей",
			}}
		}, "сравнивается только со строковым"},
		{"string field with unknown deep attribute", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
				Field: "Код", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление.Нет",
			}}
		}, "нет реквизита"},
		{"string field in hierarchy", func(p *project.Project) {
			p.Entities[2].Forms[0].Elements[0].ChoiceFilter = []metadata.FormChoiceCondition{{
				Field: "Код", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление.Код",
			}}
		}, "не ссылается на иерархический"},
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
		}, "булева реквизита"},
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
  - {name: ГруппаНеисправностей, type: "reference:ГруппаНеисправностей"}
`)
	mkFile(t, filepath.Join(dir, "catalogs", "плоский.yaml"), `name: Плоский
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "catalogs", "группанеисправностей.yaml"), `name: ГруппаНеисправностей
hierarchical: true
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "catalogs", "неисправность.yaml"), fmt.Sprintf(`name: Неисправность
hierarchical: %t
fields:
  - {name: Наименование, type: string}
  - {name: Направление, type: "reference:Направление"}
  - {name: Плоский, type: "reference:Плоский"}
  - {name: Группа, type: "reference:ГруппаНеисправностей"}
  - {name: Муниципальный, type: bool}
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

	// Срез B1 плана 183: глубокий источник и булев литерал — тот самый каскад
	// колл-центра, ради которого грамматика расширяется.
	writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - field: Группа
        op: eq
        from: Объект.Направление.ГруппаНеисправностей
      - field: Муниципальный
        op: eq
        value: false`)
	if result := RunFullWithOptions(dir, Options{Lint: true}); !result.OK || len(choiceFilterIssues(result)) != 0 {
		t.Fatalf("пример среза B1 не прошёл onebase check: issues=%+v warnings=%+v", result.Issues, result.Warnings)
	} else {
		assertNoChoiceFilterLintWarning(t, result)
	}

	// Строковый конец пути: строковый реквизит цели равен строковому
	// реквизиту записи по ссылке — дом адресного классификатора хранит ИД
	// улицы строкой (ВладелецКод), а не ссылкой.
	writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - field: Наименование
        op: eq
        from: Объект.Направление.Наименование`)
	if result := RunFullWithOptions(dir, Options{Lint: true}); !result.OK || len(choiceFilterIssues(result)) != 0 {
		t.Fatalf("строковый конец пути не прошёл onebase check: issues=%+v warnings=%+v", result.Issues, result.Warnings)
	} else {
		assertNoChoiceFilterLintWarning(t, result)
	}
}

// eq_or_empty принимает тот же глубокий источник, что eq: в одной форме оба
// оператора с `Объект.Направление.ГруппаНеисправностей` проходят onebase check,
// а неверный глубокий путь отвергается с тем же разбором, что у eq. Раньше
// ветка eq_or_empty знала только двухсегментный путь и отклоняла такой from.
func TestRunFullChoiceFilterEqualOrEmptyAcceptsDeepSourceLikeEqual(t *testing.T) {
	dir := t.TempDir()
	writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - field: Группа
        op: eq
        from: Объект.Направление.ГруппаНеисправностей
  - id: faultOrCommon
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - field: Группа
        op: eq_or_empty
        from: Объект.Направление.ГруппаНеисправностей`)
	if result := RunFullWithOptions(dir, Options{Lint: true}); !result.OK || len(choiceFilterIssues(result)) != 0 {
		t.Fatalf("глубокий источник eq/eq_or_empty не прошёл onebase check: issues=%+v warnings=%+v", result.Issues, result.Warnings)
	} else {
		assertNoChoiceFilterLintWarning(t, result)
	}

	for _, tc := range []struct {
		name, from, field, want string
	}{
		{"deeper than one hop", "Объект.Направление.ГруппаНеисправностей.Наименование", "Группа", "не более одного перехода"},
		{"intermediate attribute is not a reference", "Объект.Направление.Наименование", "Группа", "не ссылочный"},
		{"unknown intermediate attribute", "Объект.Направление.НетТакого", "Группа", "нет реквизита"},
		{"incompatible deep reference", "Объект.Направление.ГруппаНеисправностей", "Направление", "несовместимые ссылки"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeChoiceFilterCheckProject(t, dir, true, fmt.Sprintf(`  - id: faultOrCommon
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: %s, op: eq_or_empty, from: %s}]`, tc.field, tc.from))
			result := RunFull(dir)
			for _, issue := range choiceFilterIssues(result) {
				if strings.Contains(issue.Message, tc.want) && strings.Contains(issue.Message, tc.from) {
					return
				}
			}
			t.Fatalf("onebase check issues %+v do not contain eq_or_empty diagnostic %q for %q", result.Issues, tc.want, tc.from)
		})
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
    choice_filter: [{field: Направление, op: eq, from: Направление}]`, "не более одного перехода"},
		{"source deeper than one hop", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Группа, op: eq, from: Объект.Направление.ГруппаНеисправностей.Наименование}]`, "не более одного перехода"},
		{"deep source unknown attribute", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление.ID}]`, "нет реквизита"},
		{"deep source is not a reference", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление.Наименование}]`, "не ссылочный"},
		{"deep source of incompatible kind", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление.ГруппаНеисправностей}]`, "несовместимые ссылки"},
		{"unknown operator", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: contains, from: Объект.Направление}]`, "неизвестный оператор"},
		{"literal for reference", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, value: false}]`, "булева реквизита"},
		{"non-reference condition field", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Наименование, op: eq, from: Объект.Направление}]`, "несовместимые ссылки"},
		{"string field with reference deep attribute", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Наименование, op: eq, from: Объект.Направление.ГруппаНеисправностей}]`, "сравнивается только со строковым"},
		// Строковый конец пути — только у eq: «пусто» у eq_or_empty означает
		// пустую ССЫЛКУ (общая запись), у строки такого смысла нет.
		{"eq or empty with string deep end", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Наименование, op: eq_or_empty, from: Объект.Направление.Наименование}]`, "не ссылочный"},
		{"string field in hierarchy", true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Наименование, op: in_hierarchy, from: Объект.Направление.Наименование}]`, "не ссылается на иерархический"},
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
