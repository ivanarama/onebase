package ui

import (
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"testing"
)

func TestFormDirtyTypedDefaults(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				field metadata.Field
				a, b  any
				equal bool
			}{
				{"bool-null-false", metadata.Field{Type: metadata.FieldTypeBool}, nil, false, true},
				{"bool-null-zero", metadata.Field{Type: metadata.FieldTypeBool}, nil, int64(0), true},
				{"bool-null-empty", metadata.Field{Type: metadata.FieldTypeBool}, nil, "", true},
				{"bool-string-false", metadata.Field{Type: metadata.FieldTypeBool}, "0", false, true},
				{"bool-string-true", metadata.Field{Type: metadata.FieldTypeBool}, "1", true, true},
				{"bool-true-is-edit", metadata.Field{Type: metadata.FieldTypeBool}, nil, true, false},
				{"bool-false-is-edit", metadata.Field{Type: metadata.FieldTypeBool}, int64(1), false, false},
				{"string-null-empty", metadata.Field{Type: metadata.FieldTypeString}, nil, "", true},
				{"string-real-edit", metadata.Field{Type: metadata.FieldTypeString}, nil, "note", false},
				{"reference-null-empty", metadata.Field{Type: metadata.FieldTypeString, RefEntity: "Own"}, nil, "", true},
				{"reference-null-zero", metadata.Field{Type: metadata.FieldTypeString, RefEntity: "Own"}, nil, uuid.Nil, true},
				{"reference-null-empty-ref", metadata.Field{Type: metadata.FieldTypeString, RefEntity: "Own"}, nil, &interpreter.Ref{UUID: uuid.Nil.String()}, true},
				{"reference-real-edit", metadata.Field{Type: metadata.FieldTypeString, RefEntity: "Own"}, nil, uuid.New(), false},
				// Storage normalizeNumber(nil) preserves NULL, not zero. Do not erase
				// that distinction through the comparison.
				{"number-null-remains-null", metadata.Field{Type: metadata.FieldTypeNumber}, nil, float64(0), false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if got := tpCellNormStored(tc.field, tc.a, dialect) == tpCellNormStored(tc.field, tc.b, dialect); got != tc.equal {
						t.Fatalf("equal=%t want=%t", got, tc.equal)
					}
				})
			}
		})
	}
}

func TestFormDirtyUnsavedAttributes(t *testing.T) {
	for _, saved := range []bool{false, true} {
		name := "save-false"
		if saved {
			name = "save-true"
		}
		t.Run(name, func(t *testing.T) {
			form := &metadata.FormModule{Attributes: []*metadata.FormAttribute{
				{Name: "Source", TypeRef: "string", Save: saved}, {Name: "Available", TypeRef: "bool", Save: saved},
				{Name: "Rows", TypeRef: "ValueTable", Save: saved},
			}, Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Editable", DataPath: "Объект.Source"}}}
			obj := &runtime.Object{Fields: map[string]any{"Source": "old", "Available": false}, TablePartRows: map[string][]map[string]any{"Rows": nil}}
			before := snapshotFieldValues(obj.Fields)
			obj.Fields["Source"] = "new"
			obj.Fields["Available"] = true
			obj.TablePartRows["Rows"] = []map[string]any{{"Text": "display row"}}
			if got := transientManagedFormDirty(obj, before, map[string][]map[string]any{"Rows": nil}, formViewStateFields(form, nil)); got != saved {
				t.Fatalf("dirty=%t want=%t", got, saved)
			}
			// A real entity field or persistent table part always remains guarded.
			ent := &metadata.Entity{Fields: []metadata.Field{{Name: "Source", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Rows"}}}
			excluded := formViewStateFields(form, ent)
			if excluded["source"] || excluded["rows"] {
				t.Fatal("persistent state exempted")
			}
		})
	}
}
