package configcheck

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFullTPChoicePublicYAML(t *testing.T) {
	for _, tt := range []struct{ name, source, id, path, key, want string }{
		{"global", "Объект.Направление", "local", "Объект.Строки.Неисправность", "", ""},
		{"form-attribute", "Форма.НаправлениеФормы", "local", "Объект.Строки.Неисправность", "", ""},
		{"row-local", "Строки.Направление", "local", "Объект.Строки.Неисправность", "", ""},
		{"foreign-row", "Чужая.Направление", "local", "Объект.Строки.Неисправность", "", "префикс ТЧ"},
		{"missing-id", "Строки.Направление", "", "Объект.Строки.Неисправность", "", "стабильный id"},
		{"foreign-column", "Строки.Направление", "local", "Объект.Чужая.Неисправность", "", "не выбирает ссылку"},
		{"unknown-source", "Строки.Нет", "local", "Объект.Строки.Неисправность", "", "row-local"},
		{"deep-row", "Строки.Направление.ГруппаНеисправностей", "local", "Объект.Строки.Неисправность", "", "два сегмента"},
		{"unknown-key", "Строки.Направление", "local", "Объект.Строки.Неисправность", ", sql: unsafe", "неизвестный ключ"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeChoiceFilterCheckProject(t, dir, true, `  - kind: ТабличнаяЧасть
    name: Строки
    data_path: Объект.Строки
    columns:
      - id: `+tt.id+`
        kind: ПолеВвода
        data_path: `+tt.path+`
        choice_filter: [{field: Направление, op: eq, from: `+tt.source+tt.key+`}]
`)
			mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
 - {name: Направление, type: "reference:Направление"}
tableparts:
 - name: Строки
   fields:
    - {name: Направление, type: "reference:Направление"}
    - {name: Неисправность, type: "reference:Неисправность"}
`)
			result := RunFull(dir)
			issues := choiceFilterIssues(result)
			if tt.want == "" {
				if len(result.Issues) > 0 {
					t.Fatalf("%+v", result.Issues)
				}
				assertNoChoiceFilterLintWarning(t, result)
				return
			}
			for _, issue := range issues {
				if strings.Contains(issue.Message, tt.want) {
					return
				}
			}
			t.Fatalf("expected %q in %+v", tt.want, issues)
		})
	}
}

// A string target must not bypass the TP source shape gate. The same deep
// source remains supported on ordinary B1 fields, through public RunFull.
func TestRunFullTPChoiceRejectsDeepStringSource(t *testing.T) {
	for _, source := range []string{"Объект.Направление.Наименование", "Форма.НаправлениеФормы.Наименование"} {
		for _, layout := range []string{"columns", "children"} {
			t.Run(source+"/"+layout, func(t *testing.T) {
				dir := t.TempDir()
				kind := "ПолеВвода"
				if layout == "children" {
					kind = "Колонка"
				}
				writeChoiceFilterCheckProject(t, dir, true, fmt.Sprintf(`  - kind: ТабличнаяЧасть
    name: Строки
    data_path: Объект.Строки
    %s:
      - id: fault
        kind: %s
        data_path: Объект.Строки.Неисправность
        choice_filter: [{field: Наименование, op: eq, from: %s}]
`, layout, kind, source))
				mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
 - {name: Направление, type: "reference:Направление"}
 - {name: Неисправность, type: "reference:Неисправность"}
tableparts:
 - name: Строки
   fields:
    - {name: Направление, type: "reference:Направление"}
    - {name: Неисправность, type: "reference:Неисправность"}
`)
				result := RunFull(dir)
				issues := choiceFilterIssues(result)
				found := false
				for _, issue := range issues {
					if strings.Contains(issue.Message, "from колонки ТЧ должен содержать ровно два сегмента") {
						found = true
					}
				}
				if !found || result.OK {
					t.Fatalf("deep TP string source accepted or misdiagnosed: %+v", result)
				}

				// Replace only the form, keeping the same source and target.
				mkFile(t, filepath.Join(dir, "forms", "заявка", "объекта.form.yaml"), fmt.Sprintf(`schema: onebase.form/v1
form: {name: Объекта, kind: object, entity: Заявка}
attributes:
 - {name: НаправлениеФормы, type: CatalogRef.Направление}
elements:
 - id: fault
   kind: ПолеВвода
   data_path: Объект.Неисправность
   choice_filter: [{field: Наименование, op: eq, from: %s}]
`, source))
				result = RunFull(dir)
				if len(result.Issues) != 0 {
					t.Fatalf("ordinary B1 deep string source rejected: %+v", result.Issues)
				}
				assertNoChoiceFilterLintWarning(t, result)
			})
		}
	}
}
