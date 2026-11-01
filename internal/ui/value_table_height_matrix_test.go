package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Load an actual YAML form, create a record via its registered HTTP route and
// reopen it. Both dialects must retain the layout and dynamic-state anchor.
func TestValueTableHeight_FormHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{Name: "LayoutClient", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		source := `schema: onebase.form/v1
form:
  name: Object
  kind: object
  entity: LayoutClient
attributes:
  - name: Объект
    type: CatalogRef.LayoutClient
    main: true
    save: true
  - name: История
    type: ValueTable
    columns:
      - name: Номер
        type: string
elements:
  - kind: ТабличнаяЧасть
    name: History
    data_path: Форма.История
    width: 640
    height: 180
  - kind: ПолеВвода
    name: Following
    data_path: Объект.Наименование
`
		file := filepath.Join(t.TempDir(), "object.form.yaml")
		if err := os.WriteFile(file, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		form, err := loader.NewManagedFormLoader().LoadFormFile(file, entity.Name)
		if err != nil {
			t.Fatal(err)
		}
		entity.Forms = []*metadata.FormModule{form}
		if err := db.Migrate(t.Context(), []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
		interp := interpreter.New()
		interp.LookupProc = reg.GetModuleProc
		server := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
		t.Cleanup(server.BeginShutdown)
		router := chi.NewRouter()
		server.Mount(router)
		call := func(method, path string, values url.Values) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec
		}
		check := func(rec *httptest.ResponseRecorder) {
			t.Helper()
			if rec.Code != http.StatusOK {
				t.Fatalf("form HTTP status: %d", rec.Code)
			}
			node := managedAnchorNode(t, rec.Body.String(), "History")
			if node == nil {
				t.Fatal("ValueTable anchor absent")
			}
			for attr, want := range map[string]string{
				"class": "managed-vt-layout managed-vt-scroll", "data-ob-el-path": "0",
				"style": "width:640px;max-width:100%;flex:0 0 auto;min-width:0;height:180px;",
			} {
				if got, _ := managedHTMLAttr(node, attr); got != want {
					t.Errorf("%s=%q, want %q", attr, got, want)
				}
			}
		}
		check(call(http.MethodGet, "/ui/catalog/layoutclient/new", nil))
		saved := call(http.MethodPost, "/ui/catalog/layoutclient/new", url.Values{"Наименование": {"Saved layout"}})
		if saved.Code != http.StatusSeeOther {
			t.Fatalf("save HTTP status: %d", saved.Code)
		}
		rows, err := db.List(t.Context(), entity.Name, entity, storage.ListParams{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("saved rows=%d, err=%v", len(rows), err)
		}
		id := fmt.Sprint(rows[0]["id"])
		reopened := call(http.MethodGet, "/ui/catalog/layoutclient/"+id, nil)
		check(reopened)
		if !strings.Contains(reopened.Body.String(), `value="Saved layout"`) {
			t.Fatal("saved field value lost")
		}
	})
}
