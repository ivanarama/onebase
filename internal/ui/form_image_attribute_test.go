package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Public form GET: an image kept only in form state must render the standard
// viewer and respect readonly, without requiring a persistent entity field.
func TestFormImageAttributeHTTP(t *testing.T) {
	for _, ro := range []bool{true, false} {
		name := "editable"
		if ro {
			name = "readonly"
		}
		t.Run(name, func(t *testing.T) {
			form := managedObjectForm(&metadata.FormElement{Kind: metadata.FormElementField, Name: "Preview", DataPath: "Объект.Preview", ReadOnly: ro})
			form.Attributes = []*metadata.FormAttribute{{Name: "Preview", TypeRef: "image", Save: false}}
			ent := &metadata.Entity{Name: "PhotoOrder", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{form}}
			srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			req := reqWithChi(http.MethodGet, "/ui/catalog/PhotoOrder/new", nil, map[string]string{"entity": ent.Name})
			rec := httptest.NewRecorder()
			srv.form(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET status %d: %s", rec.Code, rec.Body.String())
			}
			html := rec.Body.String()
			for _, want := range []string{`data-ob-image-field="Preview"`, `type="hidden" name="Preview"`, `data-ob-image-view`, `data-ob-image-download`, `data-ob-readonly-navigation="1"`} {
				if !strings.Contains(html, want) {
					t.Errorf("GET missing image attribute markup %q", want)
				}
			}
			if strings.Contains(html, `data-ob-image-upload=`) == ro {
				t.Errorf("readonly=%v: incorrect upload controls", ro)
			}
			if strings.Contains(html, `type="text" autocomplete="off" name="Preview"`) {
				t.Error("image attribute rendered as a text UUID")
			}
		})
	}
}
