package metadata

import (
	"strings"
	"testing"
)

func TestValidateFormFieldsLegacyTablePartReferences(t *testing.T) {
	entity := &Entity{
		Name:   "Order",
		Kind:   KindDocument,
		Fields: []Field{{Name: "Name", Type: FieldTypeString}},
		TableParts: []TablePart{{
			Name:   "Lines",
			Fields: []Field{{Name: "Quantity", Type: FieldTypeNumber}},
		}},
		ItemForm: []string{"Name", "tp.Lines.Quantity"},
	}
	if err := Validate([]*Entity{entity}, nil); err != nil {
		t.Fatalf("legacy item_form table-part field rejected: %v", err)
	}

	entity.ItemForm = []string{"tp.Lines.Missing"}
	if err := Validate([]*Entity{entity}, nil); err == nil || !strings.Contains(err.Error(), "item_form") {
		t.Fatalf("unknown legacy table-part field accepted: %v", err)
	}

	entity.ItemForm = nil
	entity.ListForm = []string{"tp.Lines.Quantity"}
	if err := Validate([]*Entity{entity}, nil); err == nil || !strings.Contains(err.Error(), "list_form") {
		t.Fatalf("table-part field accepted in list_form: %v", err)
	}
}
