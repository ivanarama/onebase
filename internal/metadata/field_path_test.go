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
		{"search_fields", "[Строка]", "неизвестный реквизит шапки"},
		{"fulltext", "[Контакты.строка]", "служебные колонки"},
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

// Строка is reserved only as a table-part column, not as a header field or
// the name of a table part. Exercise the public YAML and validation path.
func TestFieldPathsServiceNamesBySegmentRole(t *testing.T) {
	const yaml = `name: Записи
fields:
  - {name: Строка, type: string}
tableparts:
  - name: Строка
    fields:
      - {name: Значение, type: string}
  - name: id
    fields:
      - {name: Значение, type: string}
`
	for _, tc := range []struct {
		key, path, canonical string
		tablePart            bool
	}{
		{"search_fields", "Строка", "Строка", false},
		{"search_fields", "сТрОкА", "Строка", false},
		{"fulltext", "Строка", "Строка", false},
		{"fulltext", "сТрОкА", "Строка", false},
		{"search_fields", "строка.значение", "Строка.Значение", true},
		{"search_fields", "ID.Значение", "id.Значение", true},
	} {
		t.Run(tc.key+tc.path, func(t *testing.T) {
			e, err := LoadFile(writeEntityYAML(t, yaml+tc.key+": ["+tc.path+"]\n"), KindCatalog)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate([]*Entity{e}, nil); err != nil {
				t.Fatal(err)
			}
			if err := ValidateIdentifiers([]*Entity{e}, nil, nil, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			resolve := SearchFieldPaths
			if tc.key == "fulltext" {
				resolve = FullTextFieldPaths
			}
			paths, err := resolve(e)
			if err != nil || len(paths) != 1 || paths[0].Path != tc.canonical || (paths[0].TablePart != nil) != tc.tablePart {
				t.Fatalf("declared path lost: %+v, %v", paths, err)
			}
			if tc.key == "fulltext" {
				fields := HeaderFullTextFields(e)
				if len(fields) != 1 || fields[0].Name != "Строка" {
					t.Fatalf("header FTS field lost: %+v", fields)
				}
			}
		})
	}
}
