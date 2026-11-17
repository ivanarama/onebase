package navigation_test

import (
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"gopkg.in/yaml.v3"
)

func TestMenuReportVariantsNormalizeResolveAndLayers(t *testing.T) {
	const full = "Полный + по филиалам / & ?"
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "recursive"}[recursive], func(t *testing.T) {
			section := metadata.MenuSection{ID: "reports", Title: "Reports"}
			items := []metadata.MenuItem{{ID: "full", Target: "report:Work", Variant: full}, {ID: "total", Target: "report:Work", Variant: "Итоговый"}}
			if recursive {
				section.Nodes = []metadata.MenuNode{{ID: "folder", Title: "Folder", Nodes: []metadata.MenuNode{
					{ID: "full", Target: items[0].Target, Variant: items[0].Variant}, {ID: "total", Target: items[1].Target, Variant: items[1].Variant},
				}}}
			} else {
				section.Items = items
			}
			objects := []navigation.Object{{Target: navigation.Target{Kind: "report", Name: "Work"}, Variants: []string{full, "Итоговый"}}}
			base, ds := navigation.Normalize("subsystem:ОТК & КЦ", &metadata.Menu{Sections: []metadata.MenuSection{section}}, navigation.NewScope(objects, nil, true))
			if len(ds) != 0 {
				t.Fatal(ds)
			}
			var links []string
			navigation.WalkItems(base, func(i navigation.Item) {
				u, err := url.Parse(navigation.ResolveItem(i, "ru", "ОТК & КЦ", func(s string) string { return s }, false).URL)
				if err != nil || u.Path != "/ui/report/work" || u.Query().Get("subsystem") != "ОТК & КЦ" || u.Query().Get("__preset") != "__standard" {
					t.Fatalf("report URL: %v %v", u, err)
				}
				links = append(links, u.Query().Get("__variant"))
			})
			if len(links) != 2 || links[0] != full || links[1] != "Итоговый" {
				t.Fatal("different entries lost their variant", links)
			}
			desired := copyTree(t, base)
			if recursive {
				nodes := desired.Sections[0].Nodes[0].Nodes
				nodes[0], nodes[1] = nodes[1], nodes[0]
			} else {
				list := desired.Sections[0].Items
				list[0], list[1] = list[1], list[0]
			}
			delta, err := navigation.Diff(base, desired, navigation.UserLayer)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := navigation.EncodeDelta(delta, navigation.UserLayer)
			if err != nil {
				t.Fatal(err)
			}
			got, ds := navigation.Compose(base, nil, raw)
			if len(ds) != 0 {
				t.Fatal(ds)
			}
			variants := []string{}
			navigation.WalkItems(got, func(i navigation.Item) { variants = append(variants, i.Variant) })
			if len(variants) != 2 || variants[0] != "Итоговый" || variants[1] != full {
				t.Fatal("personal move lost variant", variants)
			}
			if recursive {
				desired.Sections[0].Nodes[0].Nodes[0].Variant = full
			} else {
				desired.Sections[0].Items[0].Variant = full
			}
			if _, err := navigation.Diff(base, desired, navigation.UserLayer); err == nil {
				t.Fatal("personal layer changed fixed variant")
			}
		})
	}
}

func TestMenuReportVariantInvalidBindings(t *testing.T) {
	scope := navigation.NewScope([]navigation.Object{
		{Target: navigation.Target{Kind: "report", Name: "Work"}, Variants: []string{"Full"}},
		{Target: navigation.Target{Kind: "catalog", Name: "Item"}},
	}, nil, true)
	for _, body := range []string{
		"nodes: [{id: full, target: 'report:Work', variant: Missing}]",
		"items: [{id: full, target: 'report:Work', variant: Missing}]",
		"nodes: [{id: full, target: 'catalog:Item', variant: Full}]",
		"nodes: [{id: full, title: Folder, variant: Full, nodes: []}]",
	} {
		t.Run(body, func(t *testing.T) {
			var menu metadata.Menu
			if err := yaml.Unmarshal([]byte("sections:\n- id: reports\n  title: Reports\n  "+body+"\n"), &menu); err != nil {
				return // malformed folder rejected by strict metadata decoding
			}
			_, ds := navigation.Normalize("global", &menu, scope)
			for _, d := range ds {
				if !d.Warning && d.Code == "navigation.variant" {
					return
				}
			}
			t.Fatal("invalid report variant accepted", ds)
		})
	}
}

func TestMenuReportVariantsKeepOrdinaryMenuRevision(t *testing.T) {
	menu := &metadata.Menu{Sections: []metadata.MenuSection{{ID: "reports", Title: "Reports", Items: []metadata.MenuItem{{ID: "work", Target: "report:Work"}}}}}
	object := navigation.Object{Target: navigation.Target{Kind: "report", Name: "Work"}}
	base, _ := navigation.Normalize("global", menu, navigation.NewScope([]navigation.Object{object}, nil, true))
	object.Variants = []string{"New variant"}
	updated, _ := navigation.Normalize("global", menu, navigation.NewScope([]navigation.Object{object}, nil, true))
	before, _ := base.Hash()
	after, _ := updated.Hash()
	if before != after {
		t.Fatal("unselected report variants changed ordinary menu revision")
	}
}
