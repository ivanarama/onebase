package configcheck

import (
	"strings"
	"testing"
)

// choice_filter запросом проходит настоящий onebase check (RunFull): YAML-ключи
// query/params известны линту, источники параметров проверены, текст запроса
// скомпилирован. Ошибки называются кодом form.choice-filter или как запрос.
func TestRunFullChoiceFilterQuery(t *testing.T) {
	const valid = `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - query: |
          ВЫБРАТЬ Н.Ссылка ИЗ Справочник.Неисправность КАК Н
          ГДЕ Н.Направление = &Направление И Н.Наименование <> &Имя
        params:
          Направление: Объект.Направление
          Имя: Объект.Направление.Наименование
      - field: Муниципальный
        op: eq
        value: false`
	dir := t.TempDir()
	writeChoiceFilterCheckProject(t, dir, true, valid)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if !result.OK || len(choiceFilterIssues(result)) != 0 {
		t.Fatalf("условие запросом не прошло onebase check: issues=%+v warnings=%+v", result.Issues, result.Warnings)
	}
	assertNoChoiceFilterLintWarning(t, result)

	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"query с field", `      - query: ВЫБРАТЬ Н.Ссылка ИЗ Справочник.Неисправность КАК Н
        field: Направление`, "несовместим"},
		{"params без query", `      - field: Муниципальный
        op: eq
        value: false
        params: {А: Объект.Направление}`, "только вместе с query"},
		{"строковый корень параметра", `      - query: ВЫБРАТЬ Н.Ссылка ИЗ Справочник.Неисправность КАК Н ГДЕ Н.Наименование = &К
        params: {К: Объект.Комментарий}`, "не является ссылкой"},
		{"чужой корень", `      - query: ВЫБРАТЬ Н.Ссылка ИЗ Справочник.Неисправность КАК Н ГДЕ Н.Направление = &Н
        params: {Н: Строка.Направление}`, "должен быть путём"},
		{"служебный параметр", `      - query: ВЫБРАТЬ Н.Ссылка ИЗ Справочник.Неисправность КАК Н
        params: {ТекущийПользователь: Объект.Направление}`, "подставляется сам"},
		{"нет реквизита за ссылкой", `      - query: ВЫБРАТЬ Н.Ссылка ИЗ Справочник.Неисправность КАК Н ГДЕ Н.Наименование = &Н
        params: {Н: Объект.Направление.НетТакого}`, "нет реквизита"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
`+tc.yaml)
			result := RunFullWithOptions(dir, Options{Lint: true})
			var found bool
			for _, issue := range choiceFilterIssues(result) {
				found = found || strings.Contains(issue.Message, tc.want)
			}
			if !found {
				t.Fatalf("ждали ошибку %q: issues=%+v", tc.want, result.Issues)
			}
		})
	}

	// Сломанный текст запроса рантайм закрывает пустым подбором — ловит check.
	writeChoiceFilterCheckProject(t, dir, true, `  - id: fault
    kind: ПолеВвода
    data_path: Объект.Неисправность
    choice_filter:
      - query: ВЫБРАТЬ Х.Ссылка ИЗ Справочник.НетТакого КАК Х`)
	result = RunFullWithOptions(dir, Options{Lint: true})
	var queryIssue bool
	for _, issue := range result.Issues {
		queryIssue = queryIssue || strings.Contains(issue.Kind, "choice_filter")
	}
	if result.OK || !queryIssue {
		t.Fatalf("сломанный запрос choice_filter не назван: issues=%+v", result.Issues)
	}
}
