package configcheck

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Exercise the complete check --lint path, including metadata and form loading.
func TestRunFullWithLint_SupportedEntityAndFormKeys(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{"catalogs", "documents"} {
		mkFile(t, filepath.Join(dir, kind, kind+".yaml"), `name: `+kind+`
list_filters: [Наименование]
interactive_create: false
post_caption: Выполнить
post_and_close_hidden: true
fields:
  - name: Наименование
    type: string
`)
	}
	mkFile(t, filepath.Join(dir, "forms", "documents", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: documents
  enter_submits_form: true
elements:
  - kind: ГруппаФормы
    name: Внешняя
    orientation: horizontal
    equal_columns: true
    children:
      - kind: ГруппаФормы
        name: Вложенная
        orientation: horizontal
        equal_columns: true
        children:
          - kind: ПолеВвода
            name: Наименование
            data_path: Объект.Наименование
`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if !result.OK {
		t.Fatalf("supported configuration failed to load: %+v", result.Issues)
	}
	for _, warning := range result.Warnings {
		if warning.Code == "metadata.unvalidated-key" {
			t.Errorf("supported key reported as unknown: %+v", warning)
		}
	}
}

func TestRunFullWithLint_SupportedKeysStillCheckSpellingAndScope(t *testing.T) {
	for _, tc := range []struct {
		name, entityExtra, fieldExtra, formExtra, headerExtra, elementExtra string
		want                                                                []string
	}{
		{
			name: "spelling",
			entityExtra: `list_filter: [Наименование]
interactive_creat: false
post_captin: Выполнить
post_and_close_hiden: true
`,
			headerExtra:  "  enter_submit_form: true\n",
			elementExtra: "    equal_column: true\n",
			want:         []string{"list_filter", "interactive_creat", "post_captin", "post_and_close_hiden", "form.enter_submit_form", "elements[].equal_column"},
		},
		{
			name: "scope",
			fieldExtra: `    list_filters: [Наименование]
    interactive_create: false
    post_caption: Выполнить
    post_and_close_hidden: true
`,
			formExtra:    "enter_submits_form: true\nequal_columns: true\n",
			headerExtra:  "  equal_columns: true\n",
			elementExtra: "    enter_submits_form: true\n",
			want:         []string{"fields[].list_filters", "fields[].interactive_create", "fields[].post_caption", "fields[].post_and_close_hidden", "enter_submits_form", "equal_columns", "form.equal_columns", "elements[].enter_submits_form"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), "name: Заказ\n"+tc.entityExtra+`fields:
  - name: Наименование
    type: string
`+tc.fieldExtra)
			mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), "schema: onebase.form/v1\n"+tc.formExtra+`form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
`+tc.headerExtra+`elements:
  - kind: ПолеВвода
    name: Наименование
    data_path: Объект.Наименование
`+tc.elementExtra)
			result := RunFullWithOptions(dir, Options{Lint: true})
			if !result.OK {
				t.Fatalf("unknown keys should remain advisory: %+v", result.Issues)
			}
			var found []Issue
			for _, warning := range result.Warnings {
				if warning.Code == "metadata.unvalidated-key" {
					found = append(found, warning)
				}
			}
			if len(found) != len(tc.want) {
				t.Fatalf("got %d unknown keys, want %d: %+v", len(found), len(tc.want), found)
			}
			for _, key := range tc.want {
				matched := false
				for _, warning := range found {
					if strings.Contains(warning.Message, strconv.Quote(key)) {
						matched = true
						if warning.File == "" || warning.Line <= 0 || warning.Column <= 0 {
							t.Errorf("missing location for %s: %+v", key, warning)
						}
					}
				}
				if !matched {
					t.Errorf("missing diagnostic for %s: %+v", key, found)
				}
			}
		})
	}
}
