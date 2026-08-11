package metadata

import (
	"strings"
	"testing"
)

func TestFormTableDefinitionsCombinesCanonicalMetadata(t *testing.T) {
	form := &FormModule{Attributes: []*FormAttribute{{
		Name: "Подбор", TypeRef: "ValueTable",
		Columns: []*FormAttributeColumn{{Name: "Номенклатура"}, {Name: "Количество"}},
	}}}
	tableParts := []TablePart{{Name: "Товары", Fields: []Field{{Name: "Цена"}}}}

	definitions, err := FormTableDefinitions(form, tableParts)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || definitions[0].Name != "Товары" || definitions[1].Name != "Подбор" {
		t.Fatalf("definitions = %+v", definitions)
	}
	if got := strings.Join(definitions[1].Columns, ","); got != "Номенклатура,Количество" {
		t.Fatalf("ValueTable columns = %q", got)
	}
}

func TestFormTableDefinitionsRejectsCaseInsensitiveCollisions(t *testing.T) {
	tests := []struct {
		name       string
		form       *FormModule
		tableParts []TablePart
	}{
		{
			name:       "table part and value table",
			form:       &FormModule{Attributes: []*FormAttribute{{Name: "товары", TypeRef: "ValueTable"}}},
			tableParts: []TablePart{{Name: "Товары"}},
		},
		{
			name: "two value tables",
			form: &FormModule{Attributes: []*FormAttribute{
				{Name: "Подбор", TypeRef: "ValueTable"},
				{Name: "ПОДБОР", TypeRef: "valuetable"},
			}},
		},
		{
			name:       "duplicate table parts",
			tableParts: []TablePart{{Name: "Строки"}, {Name: "строки"}},
		},
		{
			name: "duplicate columns",
			form: &FormModule{Attributes: []*FormAttribute{{
				Name: "Подбор", TypeRef: "ValueTable",
				Columns: []*FormAttributeColumn{{Name: "Цена"}, {Name: "цена"}},
			}}},
		},
		{
			name:       "unicode simple-fold table names",
			tableParts: []TablePart{{Name: "S"}},
			form: &FormModule{Attributes: []*FormAttribute{{
				Name: "ſ", TypeRef: "ValueTable",
			}}},
		},
		{
			name: "unicode simple-fold columns",
			form: &FormModule{Attributes: []*FormAttribute{{
				Name: "Подбор", TypeRef: "ValueTable",
				Columns: []*FormAttributeColumn{{Name: "Σ"}, {Name: "ς"}},
			}}},
		},
		{
			name: "nil form attribute",
			form: &FormModule{Attributes: []*FormAttribute{nil}},
		},
		{
			name: "nil ValueTable column",
			form: &FormModule{Attributes: []*FormAttribute{{
				Name: "Подбор", TypeRef: "ValueTable", Columns: []*FormAttributeColumn{nil},
			}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := FormTableDefinitions(test.form, test.tableParts); err == nil {
				t.Fatal("ambiguous form table metadata was accepted")
			}
		})
	}
}
