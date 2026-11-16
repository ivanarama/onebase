package navigation_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"gopkg.in/yaml.v3"
)

const recursiveYAML = `sections:
- id: reports
  title: Reports
  nodes:
  - id: direct
    target: report:Full
  - id: masters
    title: Masters
    roles: [Chief]
    nodes:
    - id: held
      title: Held
      nodes:
      - id: full
        title: Custom entry
        target: report:Full
      - id: summary
        target: report:Summary
  - id: last
    target: report:Full
`

func recursiveTree(t *testing.T) navigation.Tree {
	t.Helper()
	var m metadata.Menu
	if err := yaml.Unmarshal([]byte(recursiveYAML), &m); err != nil {
		t.Fatal(err)
	}
	tree, ds := navigation.Normalize("subsystem:OTK", &m, navigation.NewScope([]navigation.Object{{Target: navigation.Target{Kind: "report", Name: "Full"}}, {Target: navigation.Target{Kind: "report", Name: "Summary"}}}, nil, true))
	for _, d := range ds {
		if !d.Warning {
			t.Fatal(d)
		}
	}
	return tree
}
func copyTree(t *testing.T, tree navigation.Tree) navigation.Tree {
	t.Helper()
	raw, err := tree.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var out navigation.Tree
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestRecursiveMenuPublicNormalizeAndLayers(t *testing.T) {
	base := recursiveTree(t)
	if base.Version != 2 {
		t.Fatal("not v2")
	}
	if got := base.Sections[0].Nodes[1].Nodes[0].Nodes[0]; got.ID != "cfg:full" || got.Title != "Custom entry" || navigation.AllowsRoles(got.RoleClauses, []string{"Staff"}, false) || !navigation.AllowsRoles(got.RoleClauses, []string{"Staff", "Chief"}, false) {
		t.Fatal("identity, caption or inherited roles lost")
	}
	desired := copyTree(t, base)
	nodes := desired.Sections[0].Nodes
	desired.Sections[0].Nodes = []navigation.Node{nodes[1], nodes[0], nodes[2]}
	// Move a restricted command out of its original folder and forge its clauses.
	full := desired.Sections[0].Nodes[0].Nodes[0].Nodes[0]
	full.RoleClauses = nil
	desired.Sections[0].Nodes[0].Nodes[0].Nodes = desired.Sections[0].Nodes[0].Nodes[0].Nodes[1:]
	desired.Sections[0].Nodes = append(desired.Sections[0].Nodes, full)
	delta, err := navigation.Diff(base, desired, navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := navigation.EncodeDelta(delta, navigation.AdminLayer)
	if err != nil {
		t.Fatal(err)
	}
	actual, ds := navigation.Compose(base, raw, nil)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	last := actual.Sections[0].Nodes[3]
	if last.ID != "cfg:full" || navigation.AllowsRoles(last.RoleClauses, []string{"Staff"}, false) {
		t.Fatal("move removed role restriction")
	}
	// No-op round trip must retain mixed order, nested nodes and exact hash.
	round, err := navigation.Diff(actual, copyTree(t, actual), navigation.UserLayer)
	if err != nil || len(round.Ops) != 0 {
		t.Fatalf("round trip: %#v %v", round, err)
	}
	h1, _ := base.Hash()
	reset, _ := navigation.Compose(base, nil, nil)
	h2, _ := reset.Hash()
	if h1 != h2 {
		t.Fatal("reset changed configuration")
	}
}
func TestRecursiveMenuAtomicInvalidMovesAndStaleRules(t *testing.T) {
	base := recursiveTree(t)
	hash, _ := base.Hash()
	for _, ops := range [][]navigation.Operation{
		{{Op: "move", Node: "cfg:masters", Parent: "cfg:held"}},
		{{Op: "move", Node: "cfg:direct", Parent: "cfg:full"}},
	} {
		raw, err := navigation.EncodeDelta(navigation.Delta{Version: 1, BaseHash: hash, Ops: ops}, navigation.UserLayer)
		if err != nil {
			t.Fatal(err)
		}
		got, ds := navigation.Compose(base, nil, raw)
		h, _ := got.Hash()
		if h != hash || len(ds) != 1 || ds[0].Code != "invalid-layer" {
			t.Fatal("partial invalid layer applied")
		}
	}
	title := "Extra"
	id := "usr:00000000-0000-4000-8000-000000000001"
	delta := navigation.Delta{Version: 1, BaseHash: hash, Ops: []navigation.Operation{{Op: "add_group", ID: id, Parent: "cfg:held", Title: &title}, {Op: "move", Node: "cfg:summary", Parent: id}, {Op: "remove_custom", Node: id}, {Op: "move", Node: "cfg:missing", Parent: "cfg:held"}}}
	got, ds, err := navigation.ApplyDelta(base, delta, navigation.UserLayer)
	if err != nil || len(ds) != 1 || ds[0].Code != "stale" {
		t.Fatalf("remove/stale: %v %v", err, ds)
	}
	navigation.WalkItems(got, func(i navigation.Item) {
		if i.ID == "cfg:summary" {
			t.Fatal("removed descendant survived")
		}
	})
}
func TestRecursiveMenuStrictDecodeAndLimits(t *testing.T) {
	for _, raw := range []string{
		"sections: [{id: a, title: A, nodes: [], items: []}]",
		"sections: [{id: a, title: A, nodes: null, groups: []}]",
		"sections: [{id: a, title: A, nodes: [{id: b, target: 'report:Full', nodes: []}]}]",
		"sections: [{id: a, title: A, nodes: [{id: b, title: B}]}]",
		"sections: [{id: a, title: A, nodes: [{id: b, url: 'https://evil'}]}]",
		"sections: &s [{id: a, title: A, nodes: *s}]",
	} {
		var m metadata.Menu
		if err := yaml.Unmarshal([]byte(raw), &m); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for depth := 8; depth <= 9; depth++ {
		node := metadata.MenuNode{ID: "command", Target: "report:Full"}
		for i := depth; i > 1; i-- {
			node = metadata.MenuNode{ID: fmt.Sprintf("folder-%d", i), Title: "Folder", Nodes: []metadata.MenuNode{node}}
		}
		m := metadata.Menu{Sections: []metadata.MenuSection{{ID: "root", Title: "Root", Nodes: []metadata.MenuNode{node}}}}
		_, ds := navigation.Normalize("global", &m, navigation.NewScope([]navigation.Object{{Target: navigation.Target{Kind: "report", Name: "Full"}}}, nil, true))
		fail := false
		for _, d := range ds {
			fail = fail || !d.Warning
		}
		if fail != (depth == 9) {
			t.Fatalf("depth %d: %v", depth, ds)
		}
	}
	for _, mutate := range []func(*metadata.Menu){
		func(m *metadata.Menu) { m.Sections[0].Nodes[1].ID = "direct" },
		func(m *metadata.Menu) { m.Sections[0].Nodes[0].Target = "https://evil" },
		func(m *metadata.Menu) { m.Sections[0].Nodes[0].Target = "report:Missing" },
	} {
		var m metadata.Menu
		_ = yaml.Unmarshal([]byte(recursiveYAML), &m)
		mutate(&m)
		_, ds := navigation.Normalize("global", &m, navigation.NewScope(nil, nil, true))
		found := false
		for _, d := range ds {
			found = found || !d.Warning
		}
		if !found {
			t.Fatal("invalid menu accepted")
		}
	}
}

func TestRecursiveMenuJSONRejectsAmbiguousShape(t *testing.T) {
	for _, raw := range []string{
		`{"sections":[{"id":"root","title":"Root","nodes":null,"items":[]}]}`,
		`{"sections":[{"id":"root","title":"Root","nodes":[{"id":"leaf","target":"report:Full","nodes":[]}]}]}`,
		`{"sections":[{"id":"root","title":"Root","nodes":[{"id":"leaf","target":"report:Full","url":"https://evil"}]}]}`,
	} {
		var menu metadata.Menu
		if err := json.Unmarshal([]byte(raw), &menu); err == nil {
			t.Fatalf("ambiguous JSON accepted: %s", raw)
		}
	}
}

func TestRecursiveMenuEmptyContainersRoundTripBothFormats(t *testing.T) {
	original := metadata.Menu{Sections: []metadata.MenuSection{{ID: "root", Title: "Root", Nodes: []metadata.MenuNode{{ID: "empty", Title: "Empty", Nodes: []metadata.MenuNode{}}}}, {ID: "empty-section", Title: "Empty section", Nodes: []metadata.MenuNode{}}}}
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			var raw []byte
			var err error
			if format == "yaml" {
				raw, err = yaml.Marshal(original)
			} else {
				raw, err = json.Marshal(original)
			}
			if err != nil {
				t.Fatal(err)
			}
			var restored metadata.Menu
			if format == "yaml" {
				err = yaml.Unmarshal(raw, &restored)
			} else {
				err = json.Unmarshal(raw, &restored)
			}
			if err != nil {
				t.Fatalf("round trip %s: %v", raw, err)
			}
			if restored.Sections[1].Nodes == nil || restored.Sections[0].Nodes[0].Nodes == nil {
				t.Fatal("empty recursive container lost format")
			}
		})
	}
}
