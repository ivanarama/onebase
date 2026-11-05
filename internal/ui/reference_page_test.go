package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestReferencePageHTTP(t *testing.T) {
	for _, local := range []bool{false, true} {
		proj := refCardProject(t, "", "    reference_page: Переписка\n    readonly: true\n", local)
		s, ctx := newSubmitTestServer(t, proj.Entities)
		if err := s.store.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		s.Mount(router)
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape("Заказ")+"/new", nil))
		if r.Code != http.StatusOK {
			t.Fatalf("form status: %d", r.Code)
		}
		if !strings.Contains(r.Body.String(), `data-ref-page="Переписка"`) {
			t.Fatal("YAML reference_page lost in rendered control")
		}
	}
}

func TestReferencePageBrowserNavigation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	cmd := exec.Command(node, "--test", "static/reference_page_behavior_test.js") //nolint:gosec // test-only node executable
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("navigation: %v\n%s", err, out)
	}
}
