package ui

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestMenuReportVariantBrowserControllerInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cmd := exec.Command(node, "--test", "static/navigation_tree_behavior_test.js") //nolint:gosec // Fixed local test and executable resolved with LookPath.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("menu controller: %v\n%s", err, out)
	}
}

func TestMenuReportVariantHTTPSelectionAndPermissions(t *testing.T) {
	f := newNavigationHTTPFixture(t)
	const full = "Полный + по филиалам / & ?"
	// Варианты различаются компоновкой: заголовок показателя доказывает, что
	// открылся именно выбранный вариант.
	variantComposition := func(title string) *reportpkg.Composition {
		return &reportpkg.Composition{Measures: []reportpkg.Measure{{Field: "N", Agg: "sum", Title: title}}, Totals: reportpkg.Totals{Grand: true}}
	}
	rep := &reportpkg.Report{Name: "MenuVariants", Query: `ВЫБРАТЬ 1 КАК N`, Composition: variantComposition("MENU_BASE_SOURCE"), Variants: []reportpkg.ReportVariant{
		{Name: full, Composition: variantComposition("MENU_FULL_SOURCE")},
		{Name: "Итоговый", Composition: variantComposition("MENU_TOTAL_SOURCE")},
	}}
	f.server.reg.Load(runtime.LoadOptions{Entities: f.server.reg.Entities(), Reports: []*reportpkg.Report{rep}})
	f.server.reg.LoadSubsystems([]*metadata.Subsystem{{Name: "School", Contents: metadata.SubsystemContents{Reports: []string{rep.Name}}, Menu: &metadata.Menu{Sections: []metadata.MenuSection{{
		ID: "reports", Title: "Reports", Nodes: []metadata.MenuNode{{ID: "full", Title: "Full entry", Target: "report:MenuVariants", Variant: full}, {ID: "total", Title: "Total entry", Target: "report:MenuVariants", Variant: "Итоговый"}},
	}}}}})
	role := semanticTeacher().Roles[0]
	role.Permissions.Reports = map[string][]string{rep.Name: {"run"}}
	if err := f.server.authRepo.SyncRoles(context.Background(), []*auth.Role{role}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.store.SaveReportPreset(context.Background(), storage.ReportPreset{
		Report: rep.Name, User: "alice", Name: "Unrelated personal default", IsDefault: true, SettingsJSON: `{"variant":"Итоговый"}`,
	}); err != nil {
		t.Fatal(err)
	}
	page := f.request(t, http.MethodGet, "/ui/?subsystem=School", "alice", nil)
	if page.Code != http.StatusOK {
		t.Fatal(page.Code)
	}
	doc, err := html.Parse(strings.NewReader(page.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var fullLink string
	for _, tc := range []struct{ id, variant, marker string }{{"cfg:full", full, "MENU_FULL_SOURCE"}, {"cfg:total", "Итоговый", "MENU_TOTAL_SOURCE"}} {
		link := semanticFind(doc, "data-nav-id", tc.id)
		if link == nil {
			t.Fatal("menu entry missing", tc.id)
		}
		path := semanticAttr(link, "href")
		if tc.id == "cfg:full" {
			fullLink = path
		}
		form := f.request(t, http.MethodGet, path, "alice", nil)
		if form.Code != http.StatusOK {
			t.Fatal("report form", form.Code)
		}
		formDoc, err := html.Parse(strings.NewReader(form.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		selectNode := semanticFind(formDoc, "name", "__variant")
		if selectNode == nil {
			t.Fatal("report variant selector missing")
		}
		option := semanticFind(selectNode, "value", tc.variant)
		selected := false
		if option != nil {
			for _, attr := range option.Attr {
				selected = selected || attr.Key == "selected"
			}
		}
		if !selected {
			t.Fatal("menu variant not selected", tc.variant)
		}
		run := f.request(t, http.MethodPost, path, "alice", nil)
		if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), tc.marker) {
			t.Fatalf("menu variant did not apply its own composition: %d %s", run.Code, run.Body.String())
		}
	}
	state := f.personalEditor(t, "alice", "School")
	state.Desired.Sections[0].Nodes[0].Variant = "Итоговый"
	if rec := f.request(t, http.MethodPost, "/ui/settings/navigation/save", "alice", personalNavigationForm(t, state)); rec.Code != http.StatusBadRequest {
		t.Fatal("client changed fixed menu variant", rec.Code)
	}
	role.Permissions.Reports = nil
	if err := f.server.authRepo.SyncRoles(context.Background(), []*auth.Role{role}); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(t, http.MethodGet, fullLink, "alice", nil); rec.Code != http.StatusForbidden {
		t.Fatal("menu link bypassed revoked report permission", rec.Code)
	}
}
