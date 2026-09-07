package typedempty

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestValue_CanonicalPrimitiveEmpties(t *testing.T) {
	tests := []struct {
		name string
		desc Descriptor
		ok   func(any) bool
	}{
		{"number", Descriptor{Type: metadata.FieldTypeNumber}, func(v any) bool { d, ok := v.(decimal.Decimal); return ok && d.IsZero() }},
		{"bool", Descriptor{Type: metadata.FieldTypeBool}, func(v any) bool { b, ok := v.(bool); return ok && !b }},
		{"string", Descriptor{Type: metadata.FieldTypeString}, func(v any) bool { s, ok := v.(string); return ok && s == "" }},
		{"richtext", Descriptor{Type: metadata.FieldTypeRichText}, func(v any) bool { s, ok := v.(string); return ok && s == "" }},
		{"enum", Descriptor{EnumName: "Статусы"}, func(v any) bool { s, ok := v.(string); return ok && s == "" }},
		{"date", Descriptor{Type: metadata.FieldTypeDate}, func(v any) bool { d, ok := v.(time.Time); return ok && d.IsZero() }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Value(tc.desc, nil)
			if !ok || !tc.ok(got) {
				t.Fatalf("Value(%+v) = %T(%v), ok=%v", tc.desc, got, got, ok)
			}
		})
	}
}

func TestFromFormType_PreservesFullDescriptor(t *testing.T) {
	d, ok := FromFormType("decimal(15,2)", 0, 0)
	if !ok || d.Type != metadata.FieldTypeNumber || d.Length != 15 || d.Scale != 2 {
		t.Fatalf("decimal descriptor = %+v, ok=%v", d, ok)
	}
	d, ok = FromFormType("CatalogRef.Клиенты", 0, 0)
	if !ok || d.RefEntity != "Клиенты" || d.Type != "reference:Клиенты" {
		t.Fatalf("reference descriptor = %+v, ok=%v", d, ok)
	}
}

func TestNormalize_EmptyInputUsesDeclaredType(t *testing.T) {
	if got, ok := Normalize(Descriptor{Type: metadata.FieldTypeNumber}, "", nil).(decimal.Decimal); !ok || !got.IsZero() {
		t.Fatalf("empty number = %T(%v)", Normalize(Descriptor{Type: metadata.FieldTypeNumber}, "", nil), Normalize(Descriptor{Type: metadata.FieldTypeNumber}, "", nil))
	}
	if got, ok := Normalize(Descriptor{Type: metadata.FieldTypeDate}, "", nil).(time.Time); !ok || !got.IsZero() {
		t.Fatalf("empty date = %T(%v)", Normalize(Descriptor{Type: metadata.FieldTypeDate}, "", nil), Normalize(Descriptor{Type: metadata.FieldTypeDate}, "", nil))
	}
	if got := Normalize(Descriptor{Type: metadata.FieldTypeString}, "", nil); got != "" {
		t.Fatalf("empty string = %T(%v)", got, got)
	}
}
