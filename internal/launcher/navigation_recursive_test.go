package launcher

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"gopkg.in/yaml.v3"
)

func TestRecursiveNavigationEditorPublicRoundTrip(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			const source = "subsystems/custom-school.yaml"
			h, b := newNavigationEditorFixture(t, mode, source)
			data := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School", ""))
			section := &data.Menu.Sections[0]
			a, order, bItem := section.Items[0], section.Items[1], section.Groups[0].Items[0]
			section.Items, section.Groups = nil, nil
			section.Nodes = []metadata.MenuNode{
				{ID: a.ID, Target: a.Target},
				{ID: "masters", Title: "Masters", Roles: []string{"Teacher"}, Nodes: []metadata.MenuNode{{ID: "held", Title: "Held", Nodes: []metadata.MenuNode{{ID: bItem.ID, Target: bItem.Target}}}}},
				{ID: order.ID, Target: order.Target},
			}
			body := navigationEditorBody(t, "School", data.Menu)
			preview := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/preview", body))
			if got := preview.Preview[0].Nodes; len(got) != 3 || got[1].Nodes[0].Nodes[0].ID != "cfg:b" {
				t.Fatalf("recursive preview lost: %+v", got)
			}
			readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/save", body))
			got, ok := h.readConfigFileRaw(context.Background(), b, source)
			if !ok {
				t.Fatal("source lost")
			}
			for _, text := range []string{"# item B travels with this comment", "# section comment", "future_flag: preserve", "future_home: keep", "en: English education"} {
				if !strings.Contains(string(got), text) {
					t.Fatalf("lost %q: %s", text, got)
				}
			}
			var saved struct {
				Menu *metadata.Menu `yaml:"menu"`
			}
			if err := yaml.Unmarshal(got, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Menu.Sections[0].Nodes[1].Nodes[0].Nodes[0].ID != "b" || saved.Menu.Sections[0].Nodes[1].Roles[0] != "Teacher" {
				t.Fatal("nodes or role clauses lost")
			}
			again := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School", ""))
			if again.Menu.Sections[0].Nodes[2].ID != "order" {
				t.Fatal("load reordered mixed nodes")
			}
		})
	}
}
