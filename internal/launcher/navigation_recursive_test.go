package launcher

import (
	"context"
	"net/http"
	"net/url"
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

func TestMenuReportVariantEditorRoundTrip(t *testing.T) {
	for _, mode := range []string{"file", "database"} {
		t.Run(mode, func(t *testing.T) {
			const source = "subsystems/custom-school.yaml"
			h, b := newNavigationEditorFixture(t, mode, source)
			if err := h.writeConfigFileRaw(context.Background(), b, "reports/Work.yaml", []byte("name: Work\nquery: ВЫБРАТЬ 1 КАК Amount\nvariants:\n- name: Полный\n")); err != nil {
				t.Fatal(err)
			}
			raw := strings.Replace(navigationEditorFixture, "contents:\n", "contents:\n  reports: [Work]\n", 1)
			if err := h.writeConfigFileRaw(context.Background(), b, source, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			data := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School", ""))
			section := &data.Menu.Sections[0]
			section.Items, section.Groups = nil, nil
			section.Nodes = []metadata.MenuNode{{ID: "full", Target: "report:Work", Variant: "Полный"}}
			body := navigationEditorBody(t, "School", data.Menu)
			preview := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/preview", body))
			u, err := url.Parse(preview.Preview[0].Nodes[0].URL)
			if err != nil || u.Query().Get("__variant") != "Полный" {
				t.Fatal("preview lost variant", u, err)
			}
			readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodPost, "/save", body))
			again := readNavigationEditorData(t, navigationEditorHTTP(h, http.MethodGet, "?subsystem=School", ""))
			if again.Menu.Sections[0].Nodes[0].Variant != "Полный" {
				t.Fatal("saved menu lost report variant")
			}
			before, _ := h.readConfigFileRaw(context.Background(), b, source)
			again.Menu.Sections[0].Nodes[0].Variant = "Missing"
			bad := navigationEditorHTTP(h, http.MethodPost, "/save", navigationEditorBody(t, "School", again.Menu))
			if bad.Code != http.StatusBadRequest {
				t.Fatal("unknown variant saved", bad.Code)
			}
			after, _ := h.readConfigFileRaw(context.Background(), b, source)
			if string(after) != string(before) {
				t.Fatal("failed save changed source")
			}
		})
	}
}
