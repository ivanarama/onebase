package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/formtest"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestEqualColumns_RuntimeHTTPAndBrowser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "форма.form.yaml")
	if err := os.WriteFile(path, []byte(formtest.EqualColumnsYAML()), 0600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Клиент")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Активен", Type: metadata.FieldTypeBool},
	}, Forms: []*metadata.FormModule{fm}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	render := func() string {
		req := httptest.NewRequest("GET", "/ui/catalog/клиент/new", nil)
		route := chi.NewRouteContext()
		route.URLParams.Add("entity", "клиент")
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
		rec := httptest.NewRecorder()
		s.form(rec, req)
		if rec.Code != 200 {
			t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	page := render()
	// Defaults, unsupported orientation and scroll_x do not activate the mode.
	fm.Elements[0].EqualColumns = false
	fm.Elements[1].Orientation = "vertical"
	fm.Elements[2].ScrollX = true
	legacy := render()
	if strings.Count(legacy, ` ob-equal-columns"`) != 1 {
		t.Fatal("flag applied to unsupported group or changed default")
	}
	formtest.AssertEqualColumns(t, managedLayoutTestBrowser(t), page)
}
