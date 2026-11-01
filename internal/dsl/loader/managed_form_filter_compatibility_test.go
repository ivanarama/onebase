package loader

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

func TestManagedFormLoader_ListFilterWithMainSettings(t *testing.T) {
	for _, settings := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "configured"}[settings], func(t *testing.T) {
			want := &metadata.FormModule{EntityName: "Заявка", Name: "ФормаСписка", Kind: "list"}
			if settings {
				button := false
				want.RefCardButton = &button
				want.RefCardButtonAdminOnly = true
				want.EnterSubmitsForm = true
				want.ListFilter = []metadata.FormListCondition{
					{Field: "Автор", Value: metadata.ListFilterCurrentUser, Roles: []string{"Оператор"}},
					{Query: "ВЫБРАТЬ Ссылка ИЗ Справочник.Заявка", Removable: true, Label: map[string]string{"ru": "Только мои"}},
				}
			}
			// The metadata YAML tags and the public loader must agree on all keys.
			body, err := yaml.Marshal(map[string]any{"schema": "onebase.form/v1", "form": want})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "списка.form.yaml")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := NewManagedFormLoader().LoadFormFile(path, "Заявка")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.ListFilter, want.ListFilter) || !reflect.DeepEqual(got.RefCardButton, want.RefCardButton) || got.RefCardButtonAdminOnly != want.RefCardButtonAdminOnly || got.EnterSubmitsForm != want.EnterSubmitsForm {
				t.Fatalf("settings changed across YAML load: got %+v, want %+v", got, want)
			}
		})
	}
}
