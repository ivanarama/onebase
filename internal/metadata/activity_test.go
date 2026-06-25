package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFile_ActivityDefaultsAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Номенклатура
fields:
  - name: Наименование
    type: string
  - name: Активный
    type: bool
activity:
  field: Активный
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if e.Activity == nil {
		t.Fatal("Activity = nil, want config")
	}
	if e.Activity.Field != "Активный" {
		t.Errorf("Activity.Field = %q, want Активный", e.Activity.Field)
	}
	if e.Activity.DefaultScope != ActivityScopeActive {
		t.Errorf("DefaultScope = %q, want %q", e.Activity.DefaultScope, ActivityScopeActive)
	}
	if !e.Activity.HideFromChoice {
		t.Error("HideFromChoice = false, want true by default")
	}
	if err := Validate([]*Entity{e}, nil); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_ActivityRequiresCatalogBoolField(t *testing.T) {
	cases := []struct {
		name string
		ent  *Entity
		want string
	}{
		{
			name: "document",
			ent: &Entity{
				Name:     "Заказ",
				Kind:     KindDocument,
				Fields:   []Field{{Name: "Активный", Type: FieldTypeBool}},
				Activity: &ActivityConfig{Field: "Активный", DefaultScope: ActivityScopeActive},
			},
			want: "supported only for catalogs",
		},
		{
			name: "unknown field",
			ent: &Entity{
				Name:     "Номенклатура",
				Kind:     KindCatalog,
				Fields:   []Field{{Name: "Наименование", Type: FieldTypeString}},
				Activity: &ActivityConfig{Field: "Активный", DefaultScope: ActivityScopeActive},
			},
			want: "unknown field",
		},
		{
			name: "not bool",
			ent: &Entity{
				Name:     "Номенклатура",
				Kind:     KindCatalog,
				Fields:   []Field{{Name: "Активный", Type: FieldTypeString}},
				Activity: &ActivityConfig{Field: "Активный", DefaultScope: ActivityScopeActive},
			},
			want: "must have type bool",
		},
		{
			name: "unsupported default scope",
			ent: &Entity{
				Name:     "Номенклатура",
				Kind:     KindCatalog,
				Fields:   []Field{{Name: "Активный", Type: FieldTypeBool}},
				Activity: &ActivityConfig{Field: "Активный", DefaultScope: ActivityScopeInactive},
			},
			want: "activity.default_scope must be active or all",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]*Entity{tc.ent}, nil)
			if err == nil {
				t.Fatal("Validate returned nil, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %q, want fragment %q", err.Error(), tc.want)
			}
		})
	}
}
