package sheet

import (
	"strings"
	"testing"
)

func TestHTMLGroupLevelsWithoutExportURL(t *testing.T) {
	doc := NewDocument()
	doc.GetOrCreateCell(0, 0).Text = "Group"
	doc.GetOrCreateCell(1, 0).Text = "Detail"
	doc.Groups = []RowGroup{{Start: 0, HeaderEnd: 0, Level: 1, Expanded: false}}
	html := doc.HTML(HTMLOptions{})
	for _, want := range []string{`data-sheet-row="0"`, `data-sheet-row="1"`, `data-sheet-level`, `aria-expanded`, `"Expanded":false`} {
		if !strings.Contains(html, want) {
			t.Fatalf("group controls are not mounted without an export URL: %q", want)
		}
	}
	if doc.Groups[0].Expanded {
		t.Fatal("HTML viewer changed the document snapshot")
	}
}
