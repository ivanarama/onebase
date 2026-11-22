package sheet

import (
	"strings"
	"testing"
)

func TestTabularHTMLIncludesLogicalColumnsForResizing(t *testing.T) {
	doc := NewDocument()
	doc.GetOrCreateCell(0, 0).Text = "Первый"
	doc.GetOrCreateCell(0, 1).Text = "Второй"
	doc.GetOrCreateCell(0, 2).Text = "Третий"
	doc.GetOrCreateCell(0, 0).ColSpan = 2
	html := doc.HTML(HTMLOptions{XLSXURL: "/report.xlsx"})
	for _, want := range []string{`<colgroup data-sheet-columns><col><col><col></colgroup>`,
		`data-sheet-col="0" colspan="2"`, `data-sheet-col="2"`, "setPointerCapture"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(doc.HTMLString(), "data-sheet-columns") {
		t.Fatal("standalone print HTML should retain its original markup")
	}
}
