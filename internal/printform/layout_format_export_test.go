package printform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLayoutFormatReachesTabularExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.yaml")
	data := []byte("name: Formatted\nareas:\n  - name: Body\n    rows:\n      - cells:\n          - {parameter: Amount, format: 'ЧДЦ=2'}\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	lt, err := LoadLayout(path)
	if err != nil {
		t.Fatal(err)
	}
	area := BuildAreaCells(lt.Area("Body"))
	cell := area.Cells["0,0"]
	if cell == nil || cell.Format != "ЧДЦ=2" || cell.ExcelFormat() != "0.00" {
		t.Fatalf("format not propagated: %+v", cell)
	}
}
