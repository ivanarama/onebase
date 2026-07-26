package interpreter

import (
	"testing"

	"github.com/ivantit66/onebase/internal/printform"
)

// TestMaketGetArea_NewFields ensures the new LayoutCell properties
// (VAlign, FontFamily, Border, RowSpan) are propagated to SpreadsheetDocumentCell
// when an area is materialised via макет.ПолучитьОбласть.
func TestMaketGetArea_NewFields(t *testing.T) {
	lt := &printform.LayoutTemplate{
		Name: "T",
		Areas: []*printform.LayoutArea{
			{
				Name: "Шапка",
				Rows: []printform.LayoutRow{
					{Cells: []printform.LayoutCell{
						{
							Text:       "Hello",
							VAlign:     "Middle",
							FontFamily: "Arial",
							Border:     "Thick",
							RowSpan:    2,
							ColSpan:    3,
						},
					}},
				},
			},
		},
	}

	m := NewMaket(lt)
	area := m.getArea("Шапка")
	if area == nil {
		t.Fatal("area is nil")
		return
	}
	cell, ok := area.Area.Cells["0,0"]
	if !ok {
		t.Fatalf("cell 0,0 missing in area: %#v", area.Area.Cells)
	}
	if cell.Vertical != "middle" {
		t.Errorf("vertical: got %q, want middle", cell.Vertical)
	}
	if cell.FontFamily != "Arial" {
		t.Errorf("fontFamily: got %q, want Arial", cell.FontFamily)
	}
	if cell.Border != "thick" {
		t.Errorf("border: got %q, want thick", cell.Border)
	}
	if cell.RowSpan != 2 {
		t.Errorf("rowSpan: got %d, want 2", cell.RowSpan)
	}
	if cell.ColSpan != 3 {
		t.Errorf("colSpan: got %d, want 3", cell.ColSpan)
	}
}

// TestInjectMaket проверяет функцию инъекции переменной «Макет» в vars:
// при наличии layout переменная добавляется, при nil — нет (поведение DSL без
// макета не меняется). Используется всеми путями запуска обработок.
func TestInjectMaket(t *testing.T) {
	lt := &printform.LayoutTemplate{
		Name:  "T",
		Areas: []*printform.LayoutArea{{Name: "A", Rows: []printform.LayoutRow{{Cells: []printform.LayoutCell{{Text: "x"}}}}}},
	}

	// С layout — переменная добавлена и это *Макет.
	vars := map[string]any{}
	InjectMaket(vars, lt)
	got, ok := vars["Макет"]
	if !ok {
		t.Fatal("переменная Макет не добавлена при наличии layout")
	}
	if _, ok := got.(*Макет); !ok {
		t.Fatalf("ожидался *Макет, получено %T", got)
	}

	// Без layout (nil) — переменная не добавляется.
	vars2 := map[string]any{}
	InjectMaket(vars2, nil)
	if _, ok := vars2["Макет"]; ok {
		t.Error("при nil layout переменная Макет не должна добавляться")
	}

	// nil-карта не паникует.
	InjectMaket(nil, lt)
}

// TestMaketGetArea_DefaultsPreserved ensures old layouts without new fields
// still produce cells with sensible defaults.
func TestMaketGetArea_DefaultsPreserved(t *testing.T) {
	lt := &printform.LayoutTemplate{
		Areas: []*printform.LayoutArea{
			{Name: "A", Rows: []printform.LayoutRow{
				{Cells: []printform.LayoutCell{{Text: "x"}}},
			}},
		},
	}
	area := NewMaket(lt).getArea("A")
	cell := area.Area.Cells["0,0"]
	if cell.Vertical != "top" || cell.FontFamily != "Times New Roman" || cell.Border != "all" {
		t.Errorf("defaults overwritten unexpectedly: %+v", cell)
	}
}
