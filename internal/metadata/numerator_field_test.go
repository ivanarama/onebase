package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadNumeratorYAML(t *testing.T, kind Kind, body string) (*Entity, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "object.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadFile(path, kind)
}

func TestLoadFileNumeratorFieldLegacyAndCanonical(t *testing.T) {
	for _, tc := range []struct {
		name, field, id string
		kind            Kind
	}{
		{"catalog", StandardCodeField, StandardCodeFieldID, KindCatalog},
		{"document", StandardNumberField, StandardNumberFieldID, KindDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := "name: Test\nnumerator:\n  length: 6\n"
			props := "    title: Custom\n    titles: {en: Custom code}\n    required: true\n    default: ABC\n    pii: true\n"
			canonical := base + "  field:\n" + props + "fields:\n  - {name: Other, type: string}\n"
			legacy := base + "fields:\n  - id: f_old\n    name: " + tc.field + "\n    type: string\n" + props + "  - {name: Other, type: string}\n"
			for _, body := range []string{canonical, legacy} {
				e, err := loadNumeratorYAML(t, tc.kind, body)
				if err != nil {
					t.Fatal(err)
				}
				if len(e.Fields) != 2 || e.Fields[0].Name != tc.field || e.Fields[0].ID != tc.id || e.Fields[0].Type != FieldTypeString {
					t.Fatalf("standard field identity: %+v", e.Fields)
				}
				f := e.Fields[0]
				if f.Title != "Custom" || f.Titles["en"] != "Custom code" || !f.Required || f.Default != "ABC" || !f.PII {
					t.Fatalf("standard field properties lost: %+v", f)
				}
			}
		})
	}
}

func TestLoadFileNumeratorFieldRejectsAmbiguity(t *testing.T) {
	base := "name: Test\nnumerator:\n  length: 6\n"
	for _, tc := range []struct{ name, yaml, want string }{
		{"two sources", base + "  field: {title: Canonical}\nfields:\n  - {name: Код, type: string}\n", "both fields and numerator.field"},
		{"duplicate legacy", base + "fields:\n  - {name: Код, type: string}\n  - {name: код, type: string}\n", "multiple Код"},
		{"forged identity", base + "  field: {id: f_wrong}\n", "numerator.field"},
		{"forged type", base + "  field: {type: number}\n", "numerator.field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadNumeratorYAML(t, KindCatalog, tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
