package interpreter

import (
	"github.com/ivantit66/onebase/internal/sheet"
	"testing"
)

func TestTabularAutoGroupingLeavesFooterOutside(t *testing.T) {
	d := NewSpreadsheetDocument()
	a := &SpreadsheetDocumentArea{Area: sheet.NewArea(0, 0, 0, 0)}
	a.Area.GetOrCreateCell(0, 0).Text = "own"
	d.CallMethod("вывести", []any{a, 1, "group", false})
	d.CallMethod("вывести", []any{a})
	d.CallMethod("закончитьавтогруппировкустрок", nil)
	d.CallMethod("вывести", []any{a})
	groups := d.Doc.ResolvedGroups()
	if len(groups) != 1 || groups[0].End != 1 || d.Doc.CurrentRow != 3 {
		t.Fatalf("footer entered collapsed group: %+v", groups)
	}
}
