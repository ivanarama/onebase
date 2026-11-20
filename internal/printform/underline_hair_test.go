package printform

import (
	"github.com/ivantit66/onebase/internal/sheet"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutUnderlineHairPropagation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.yaml")
	if err := os.WriteFile(path, []byte("name: Own\nareas:\n  - name: Header\n    rows:\n      - cells:\n          - text: Own\n            underline: true\n            borders: {bottom: hair}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	layout, err := LoadLayout(path)
	if err != nil {
		t.Fatal(err)
	}
	area := BuildAreaCells(layout.Area("Header"))
	cell := area.Cells["0,0"]
	if cell == nil || !cell.Underline || cell.BorderBottom != "hair" {
		t.Fatal("native layout style dropped")
	}
	d := sheet.NewDocument()
	d.Put(area)
	if !strings.Contains(d.HTML(sheet.HTMLOptions{}), "text-decoration:underline") {
		t.Fatal("layout underline dropped at put")
	}
}
