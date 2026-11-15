package sheet

import (
	"bytes"
	"github.com/xuri/excelize/v2"
	dom "golang.org/x/net/html"
	"strings"
	"testing"
)

func colorAttr(n *dom.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
func colorAnchor(t *testing.T, markup string) *dom.Node {
	t.Helper()
	d, e := dom.Parse(strings.NewReader(markup))
	if e != nil {
		t.Fatal(e)
	}
	var found *dom.Node
	var walk func(*dom.Node)
	walk = func(n *dom.Node) {
		if n.Type == dom.ElementNode && n.Data == "a" && colorAttr(n, "data-sheet-drilldown") == "1" {
			found = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(d)
	if found == nil {
		t.Fatal("no native drilldown anchor")
	}
	return found
}
func TestTabularDrilldownTextColorHTML(t *testing.T) {
	for _, tc := range []struct {
		name, color string
		inherit     bool
	}{{"red", "#FF0000", true}, {"orange", "#FF9900", true}, {"yellow", "#A3A300", true}, {"green", "#008000", true}, {"default", "", false}, {"invalid", "red;position:fixed", false}, {"payload", "#FF0000", true}} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDocument()
			c := d.GetOrCreateCell(0, 0)
			c.Text = "Own <Master> & name"
			c.TextColor = tc.color
			c.Drilldown = &Reference{Entity: "OwnMaster", ID: "11111111-1111-4111-8111-111111111111"}
			opts := HTMLOptions{DrilldownURL: func(r, c int) string { return "/ui/tabular/own/drilldown?row=0&col=0" }}
			if tc.name == "payload" {
				c.Drilldown = nil
				c.DrilldownPayload = &DrilldownValue{Kind: "string", Text: "own"}
				opts.ParentDrilldown = true
			}
			before := *c
			markup := d.HTML(opts)
			a := colorAnchor(t, markup)
			if (colorAttr(a, "style") == "color:inherit") != tc.inherit {
				t.Fatalf("anchor style=%q inherit=%v", colorAttr(a, "style"), tc.inherit)
			}
			if !tc.inherit && colorAttr(a, "style") != "" {
				t.Fatal("default/unsafe color changed default link style")
			}
			if tc.inherit && !strings.Contains(colorAttr(a.Parent, "style"), "color:"+tc.color) {
				t.Fatal("safe cell color missing on parent")
			}
			if colorAttr(a, "href") != "/ui/tabular/own/drilldown?row=0&col=0" || colorAttr(a, "target") != "_blank" || colorAttr(a, "rel") != "noopener" {
				t.Fatal("drilldown URL/attributes changed")
			}
			if strings.Contains(markup, "<Master>") || strings.Contains(markup, "position:fixed") {
				t.Fatal("text/CSS escaped boundary changed")
			}
			if *c != before {
				t.Fatal("render mutated source cell")
			}
		})
	}
}
func TestTabularDrilldownColorXLSXUnchanged(t *testing.T) {
	for _, color := range []string{"#FF0000", "#FF9900", "#A3A300", "#008000"} {
		t.Run(strings.TrimPrefix(color, "#"), func(t *testing.T) {
			d := NewDocument()
			c := d.GetOrCreateCell(0, 0)
			c.Text = "MASTER"
			c.TextColor = color
			c.Drilldown = &Reference{Entity: "OwnMaster", ID: "11111111-1111-4111-8111-111111111111"}
			data, e := d.XLSX()
			if e != nil {
				t.Fatal(e)
			}
			f, e := excelize.OpenReader(bytes.NewReader(data))
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			sid, e := f.GetCellStyle(f.GetSheetList()[0], "A1")
			if e != nil {
				t.Fatal(e)
			}
			style, e := f.GetStyle(sid)
			if e != nil || style.Font == nil || !strings.EqualFold(style.Font.Color, strings.TrimPrefix(color, "#")) {
				t.Fatalf("XLSX font lost explicit color: %+v %v", style, e)
			}
		})
	}
}
