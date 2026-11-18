package interpreter

import "testing"

func TestTabularAutoScalePropertySnapshot(t *testing.T) {
	d := NewSpreadsheetDocument()
	if d.Get("АвтоМасштаб") != false {
		t.Fatal("unexpected default")
	}
	d.Set("АвтоМасштаб", true)
	if d.Get("AutoScale") != true || !d.Doc.Snapshot().AutoScale {
		t.Fatal("print setting lost")
	}
	d.Set("autoscale", false)
	if d.Get("АвтоМасштаб") != false {
		t.Fatal("explicit false ignored")
	}
}
