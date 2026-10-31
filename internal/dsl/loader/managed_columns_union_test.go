package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

func TestManagedColumnsPublicLoadAndMarshal(t *testing.T) {
	const header = "form: {name: Object, kind: object, entity: Owner}\nelements:\n"
	const list = " - kind: Список\n   name: History\n   entity: Target\n   columns: [Name]\n   filter: [{field: Active, op: eq, value: false}]\n"
	const table = " - kind: ТабличнаяЧасть\n   name: Items\n   data_path: Объект.Items\n   columns:\n    - kind: ПолеВвода\n      name: Item\n      data_path: Items.Item\n      choice_filter: [{field: Active, op: eq, value: false}]\n"
	path := filepath.Join(t.TempDir(), "object.form.yaml")
	load := func(text string) (*metadata.FormModule, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		return NewManagedFormLoader().LoadFormFile(path, "Owner")
	}
	form, err := load(header + list + table)
	if err != nil {
		t.Fatal(err)
	}
	check := func(form *metadata.FormModule) {
		t.Helper()
		if len(form.Elements) != 2 || form.Elements[0].EntityList == nil || len(form.Elements[0].Columns) != 0 || len(form.Elements[0].EntityList.Columns) != 1 || form.Elements[0].EntityList.Columns[0] != "Name" {
			t.Fatalf("list columns lost: %+v", form.Elements)
		}
		cols := form.Elements[1].Columns
		if form.Elements[1].EntityList != nil || len(cols) != 1 || cols[0].Name != "Item" || len(cols[0].ChoiceFilter) != 1 || cols[0].ChoiceFilter[0].Value == nil || *cols[0].ChoiceFilter[0].Value {
			t.Fatalf("table columns lost: %+v", cols)
		}
	}
	check(form)
	aliased, err := load(header + strings.Replace(list, "columns: [Name]", "order_by: &visible [Name]\n   columns: *visible", 1) + table)
	if err != nil {
		t.Fatal(err)
	}
	check(aliased)
	elements, err := yaml.Marshal(struct {
		Elements []*metadata.FormElement `yaml:"elements"`
	}{form.Elements})
	if err != nil {
		t.Fatal(err)
	}
	again, err := load("form: {name: Object, kind: object, entity: Owner}\n" + string(elements))
	if err != nil {
		t.Fatal(err)
	}
	check(again)
	for _, tc := range []struct{ name, text, want string }{
		{"list mappings", strings.Replace(list, "[Name]", "[{name: Name}]", 1), "columns"},
		{"list mixed", strings.Replace(list, "[Name]", "[Name, {name: Name}]", 1), "columns"},
		{"list numeric", strings.Replace(list, "[Name]", "[42]", 1), "columns"},
		{"list scalar", strings.Replace(list, "[Name]", "Name", 1), "columns"},
		{"list duplicate", list + "   columns: [Other]\n", "columns"},
		{"list unknown", list + "   colums: [Other]\n", "colums"},
		{"list limit type", list + "   limit: '5'\n", "limit"},
		{"table strings", " - kind: ТабличнаяЧасть\n   name: Items\n   columns: [Name]\n", "cannot unmarshal"},
		{"table mixed", table + "    - Name\n", "cannot unmarshal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(header + tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q rejection, got %v", tc.want, err)
			}
		})
	}
}
