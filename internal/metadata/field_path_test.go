package metadata

import (
	"strings"
	"testing"
)

const fieldPathYAML = `name: Клиенты
fields:
  - {name: Наименование, type: string}
  - {name: Число, type: number}
tableparts:
  - name: Контакты
    fields:
      - {name: Значение, type: string}
      - {name: Число, type: number}
      - {name: Ссылка, type: 'reference:Клиенты'}
      - {name: Флаг, type: bool}
      - {name: Дата, type: date}
  - name: Другие
    fields:
      - {name: Значение, type: string}
`

func TestFieldPathsPublicYAMLContract(t *testing.T) {
	for _, tc := range []struct{ key, value, want string }{
		{"search_fields", "[наименование, контакты.значение, Другие.Значение, Контакты.Число, Контакты.Флаг, Контакты.Дата]", ""},
		{"search_fields", "[]", ""},
		{"search_fields", "[Контакты.Нет]", "Контакты.Нет"},
		{"search_fields", "[Нет.Значение]", "Нет.Значение"},
		{"search_fields", "[Контакты.Значение.Нет]", "Контакты.Значение.Нет"},
		{"search_fields", "['Контакты.']", "Контакты."},
		{"search_fields", "['.Значение']", ".Значение"},
		{"search_fields", "['']", `путь ""`},
		{"search_fields", "[Контакты.parent_id]", "Контакты.parent_id"},
		{"search_fields", "[Контакты.строка]", "Контакты.строка"},
		{"search_fields", "[id]", "id"},
		{"search_fields", "[Контакты.Ссылка]", "Контакты.Ссылка"},
		{"search_fields", "[Контакты.Значение, контакты.значение]", "дважды"},
		{"search_fields", "[Наименование, наименование]", "дважды"},
		{"fulltext", "[Наименование]", ""},
		{"fulltext", "[]", ""},
		{"fulltext", "['']", `путь ""`},
		{"fulltext", "[Контакты.Значение]", "пока не поддерживается"},
		{"fulltext", "[Контакты.Число]", "нужен тип string или richtext"},
		{"fulltext", "[Контакты.Ссылка]", "нужен тип string или richtext"},
		{"fulltext", "[Контакты.Нет]", "Контакты.Нет"},
		{"fulltext", "[Наименование, наименование]", "дважды"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			e, err := LoadFile(writeEntityYAML(t, fieldPathYAML+tc.key+": "+tc.value+"\n"), KindCatalog)
			if err != nil {
				t.Fatal(err)
			}
			err = Validate([]*Entity{e}, nil)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "Клиенты") {
				t.Fatalf("want entity and %q, got %v", tc.want, err)
			}
		})
	}
}

func TestTypedFieldPathsDefaultsAndCanonicalNames(t *testing.T) {
	e, err := LoadFile(writeEntityYAML(t, fieldPathYAML), KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, resolve := range []func(*Entity) ([]EntityFieldPath, error){SearchFieldPaths, FullTextFieldPaths} {
		paths, err := resolve(e)
		if err != nil || len(paths) != 1 || paths[0].Path != "Наименование" || paths[0].TablePart != nil {
			t.Fatalf("header-only default: %+v, %v", paths, err)
		}
	}
	e.SearchSet = true
	e.Search = []string{"контакты.значение", "Другие.Значение", "наименование"}
	paths, err := SearchFieldPaths(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[0].Path != "Контакты.Значение" || paths[0].TablePart.Name != "Контакты" || paths[0].Field.Name != "Значение" || paths[2].TablePart != nil {
		t.Fatalf("canonical paths: %+v", paths)
	}
	e.Search = nil
	if paths, err = SearchFieldPaths(e); err != nil || len(paths) != 0 {
		t.Fatalf("explicit empty: %v %v", paths, err)
	}
	e.FullTextSet = true
	e.FullText = []string{"Наименование", "Контакты.Значение"}
	paths, err = FullTextFieldPaths(e)
	if err != nil || len(paths) != 2 || paths[1].TablePart == nil {
		t.Fatalf("shared typed fulltext contract: %v %v", paths, err)
	}
	if got := HeaderFullTextFields(e); len(got) != 0 {
		t.Fatalf("header adapter silently dropped a dotted path: %v", got)
	}
	e.FullText = []string{"Наименование", "Нет"}
	if got := HeaderFullTextFields(e); len(got) != 0 {
		t.Fatalf("invalid header adapter returned partial index: %v", got)
	}
}
