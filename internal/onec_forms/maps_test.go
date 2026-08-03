package onec_forms

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Карты элементов должны быть симметричными: 1С→OneBase→1С даёт исходное
// имя для каждого однозначного ключа. Несимметричные случаи (Decoration,
// CommandBarButton) сюда не входят — они решаются хуками в mapping_in/out.
func TestElementMap_RoundTrip(t *testing.T) {
	for name1c := range elementMap {
		obKind, ok := Element1CToOneBase(name1c)
		if !ok {
			t.Errorf("Element1CToOneBase(%q): not found", name1c)
			continue
		}
		back, ok := ElementOneBaseTo1C(obKind)
		if !ok {
			t.Errorf("ElementOneBaseTo1C(%v): not found (origin %q)", obKind, name1c)
			continue
		}
		// для CommandBar/AutoCommandBar обратный путь может отдать любое
		// из двух имён — проверяем, что оба маппятся в один FormElementType.
		if backKind, ok := Element1CToOneBase(back); !ok || backKind != obKind {
			t.Errorf("round-trip element %q → %v → %q (mapped to %v)", name1c, obKind, back, backKind)
		}
	}
}

func TestEventMap_RoundTrip(t *testing.T) {
	for name1c := range eventMap {
		obEvt, ok := Event1CToOneBase(name1c)
		if !ok {
			t.Errorf("Event1CToOneBase(%q): not found", name1c)
			continue
		}
		back, ok := EventOneBaseTo1C(obEvt)
		if !ok {
			t.Errorf("EventOneBaseTo1C(%v): not found (origin %q)", obEvt, name1c)
			continue
		}
		// синонимы (Choose/Choice, OnClick/Click) могут вернуть не исходный
		// ключ — проверяем что обратный поход даёт тот же FormEventType.
		if backEvt, ok := Event1CToOneBase(back); !ok || backEvt != obEvt {
			t.Errorf("round-trip event %q → %v → %q (mapped to %v)", name1c, obEvt, back, backEvt)
		}
	}
}

func TestType1CToOneBase(t *testing.T) {
	cases := []struct {
		name         string
		xsd          string
		length, prec int
		want         string
	}{
		{"string40", "xs:string", 40, 0, "string(40)"},
		{"string_unbounded", "xs:string", 0, 0, "string"},
		{"decimal_15_2", "xs:decimal", 15, 2, "decimal(15,2)"},
		{"decimal_15", "xs:decimal", 15, 0, "decimal(15)"},
		{"date", "xs:dateTime", 0, 0, "dateTime"},
		{"bool", "xs:boolean", 0, 0, "bool"},
		{"catalog_ref", "cfg:CatalogRef.Контрагенты", 0, 0, "CatalogRef.Контрагенты"},
		{"document_ref", "cfg:DocumentRef.РеализацияТоваров", 0, 0, "DocumentRef.РеализацияТоваров"},
		{"value_table", "v8:ValueTable", 0, 0, "ValueTable"},
		{"any_ref", "cfg:AnyRef", 0, 0, "AnyRef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Type1CToOneBase(c.xsd, c.length, c.prec, "")
			if got != c.want {
				t.Errorf("Type1CToOneBase(%q, %d, %d) = %q, want %q", c.xsd, c.length, c.prec, got, c.want)
			}
		})
	}
}

func TestTypeOneBaseTo1C(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		typeName  string
		length    int
		precision int
	}{
		{"string40", "string(40)", "xs:string", 40, 0},
		{"decimal_15_2", "decimal(15,2)", "xs:decimal", 15, 2},
		{"date", "dateTime", "xs:dateTime", 0, 0},
		{"bool", "bool", "xs:boolean", 0, 0},
		{"catalog_ref", "CatalogRef.Контрагенты", "cfg:CatalogRef.Контрагенты", 0, 0},
		{"value_table", "ValueTable", "v8:ValueTable", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tn, l, p, _ := TypeOneBaseTo1C(c.in)
			if tn != c.typeName || l != c.length || p != c.precision {
				t.Errorf("TypeOneBaseTo1C(%q) = (%q, %d, %d), want (%q, %d, %d)",
					c.in, tn, l, p, c.typeName, c.length, c.precision)
			}
		})
	}
}

// Карты, которые понадобятся mapping_in: убедимся что все
// важные ключевые типы OneBase представлены в инверсии.
func TestEventMap_CoversAllOneBaseEvents(t *testing.T) {
	expect := []metadata.FormEventType{
		metadata.FormEventOnOpen,
		metadata.FormEventOnChange,
		metadata.FormEventOnClick,
		metadata.FormEventExecuteCommand,
	}
	for _, e := range expect {
		if _, ok := EventOneBaseTo1C(e); !ok {
			t.Errorf("event %v has no 1C reverse mapping", e)
		}
	}
}
