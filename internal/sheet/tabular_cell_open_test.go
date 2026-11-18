package sheet

import (
	"strings"
	"testing"
)

func TestTabularHTMLIncludesDoubleClickOnlyForDrilldown(t *testing.T) {
	doc := NewDocument()
	doc.GetOrCreateCell(0, 0).Text = "Запись"
	opts := HTMLOptions{XLSXURL: "/report.xlsx", DrilldownURL: func(_, _ int) string { return "/cell" }}
	if html := doc.HTML(opts); strings.Contains(html, "addEventListener('dblclick'") {
		t.Fatal("plain sheet contains the drilldown script")
	}
	doc.GetOrCreateCell(0, 0).Drilldown = &Reference{Entity: "Item", ID: "test"}
	html := doc.HTML(opts)
	if !strings.Contains(html, "addEventListener('dblclick'") ||
		!strings.Contains(html, `data-sheet-drilldown="1"`) || !strings.Contains(html, `href="/cell"`) {
		t.Fatal("the viewer must delegate to the existing authorized link")
	}
}
