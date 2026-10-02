package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// writeChoiceFilterProcessorProject — проект проверки choice_filter плюс
// обработка с параметрами-ссылками и её управляемая форма (#1840). Владелец
// формы — обработка: источники Объект.<Параметр> разбираются по параметрам.
func writeChoiceFilterProcessorProject(t *testing.T, dir string, elementYAML string) {
	t.Helper()
	writeChoiceFilterCheckProject(t, dir, true, `  - {id: placeholder, kind: Надпись}`)
	mkFile(t, filepath.Join(dir, "processors", "поисквыбора.yaml"), `name: ПоискВыбора
params:
  - {name: Направление, type: "reference:Направление"}
  - {name: Плоский, type: "reference:Плоский"}
  - {name: Неисправность, type: "reference:Неисправность"}
  - {name: Комментарий, type: string}
`)
	mkFile(t, filepath.Join(dir, "forms", "поисквыбора", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: Объекта
  kind: processor
  entity: ПоискВыбора
attributes:
  - name: НаправлениеФормы
    type: CatalogRef.Направление
elements:
`+elementYAML+"\n")
}

// Форма обработки с корректным choice_filter проходит onebase check: те же
// операторы и источники, что у формы документа — параметр, реквизит формы,
// один переход по ссылке, литерал.
func TestRunFullChoiceFilterProcessorFormAcceptsParams(t *testing.T) {
	dir := t.TempDir()
	writeChoiceFilterProcessorProject(t, dir, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - {field: Направление, op: in_hierarchy, from: Объект.Направление}
      - {field: Плоский, op: eq_or_empty, from: Объект.Плоский}
      - {field: Группа, op: eq, from: Объект.Направление.ГруппаНеисправностей}
      - {field: Муниципальный, op: eq, value: false}
  - id: fault-by-form
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - {field: Направление, op: eq, from: Форма.НаправлениеФормы}`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if issues := choiceFilterIssues(result); !result.OK || len(issues) != 0 {
		t.Fatalf("корректный choice_filter формы обработки не прошёл onebase check: issues=%+v", result.Issues)
	}
	assertNoChoiceFilterLintWarning(t, result)
}

// До #1840 onebase check обходил только формы документов и справочников:
// ошибочный choice_filter формы обработки проходил молча, а в рантайме
// подбор показывал весь справочник. Теперь форма обработки проверяется тем же
// контрактом, и ошибка указывает на файл формы обработки.
func TestRunFullChoiceFilterProcessorFormRejectsInvalidContract(t *testing.T) {
	tests := []struct {
		name    string
		element string
		want    string
	}{
		{"unknown target field", `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: НеСуществует, op: eq, from: Объект.Направление}]`, "нет реквизита"},
		{"unknown processor parameter", `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.НетТакого}]`, "не является явной ссылкой"},
		{"incompatible reference parameter", `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Плоский}]`, "несовместимые ссылки"},
		{"string parameter as data path", `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Комментарий
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление}]`, "не выбирает ссылку"},
		{"missing stable id", `  - kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter: [{field: Направление, op: eq, from: Объект.Направление}]`, "стабильный id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeChoiceFilterProcessorProject(t, dir, test.element)
			result := RunFull(dir)
			for _, issue := range choiceFilterIssues(result) {
				if issue.Object != "ПоискВыбора" {
					continue
				}
				if !strings.Contains(filepath.ToSlash(issue.File), "forms/поисквыбора/") {
					t.Fatalf("ошибка формы обработки указывает не на её файл: %+v", issue)
				}
				if strings.Contains(issue.Message, test.want) {
					return
				}
			}
			t.Fatalf("onebase check не отклонил choice_filter формы обработки (%q): issues=%+v", test.want, result.Issues)
		})
	}
}
