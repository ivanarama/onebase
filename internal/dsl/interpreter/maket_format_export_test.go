package interpreter

import (
	"github.com/ivantit66/onebase/internal/printform"
	"testing"
)

func TestMaketFormatReachesTabularExport(t *testing.T) {
	lt := &printform.LayoutTemplate{Areas: []*printform.LayoutArea{{Name: "Body", Rows: []printform.LayoutRow{{Cells: []printform.LayoutCell{{Parameter: "Amount", Format: "0.0%"}}}}}}}
	area, ok := NewMaket(lt).Get("Body").(*SpreadsheetDocumentArea)
	if !ok || area == nil || area.Area.Cells["0,0"].Format != "0.0%" {
		t.Fatalf("DSL maket format missing: %+v", area)
	}
}
