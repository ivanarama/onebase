package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Query filters constrain folders as well as records. Field filters preserve
// navigable folders, while row access always constrains both kinds of row.
func TestUIListFormQueryFiltersFoldersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})
		for name, body := range map[string]string{
			"catalogs/tasks.yaml": `name: Tasks
hierarchical: true
fields:
  - {name: Наименование, type: string}
  - {name: Author, type: string}
  - {name: Marker, type: string}
  - {name: Visible, type: bool}
`,
			"forms/tasks/list.form.yaml": `schema: onebase.form/v1
form:
  name: TaskList
  kind: list
  entity: Tasks
  filter:
    - query: ВЫБРАТЬ З.Ссылка КАК Ссылка ИЗ Справочник.Tasks КАК З ГДЕ З.Author = &ТекущийПользователь
      roles: [Оператор]
    - field: Marker
      value: keep
      roles: [Оператор]
elements:
  - {kind: ПолеВвода, name: NameColumn, data_path: Наименование}
`,
		} {
			if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := root.WriteFile(name, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if result := configcheck.RunFull(dir); !result.OK {
			t.Fatalf("check: %+v", result.Issues)
		}
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: proj.Entities})
		ctx := context.Background()
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		entity := reg.GetEntity("Tasks")
		for _, row := range []map[string]any{
			{"Наименование": "QUERY-MY-FOLDER", "Author": "u-op", "Visible": true, "is_folder": true},
			{"Наименование": "QUERY-OTHER-FOLDER", "Author": "u-other", "Visible": true, "is_folder": true},
			{"Наименование": "QUERY-DENIED-FOLDER", "Author": "u-op", "Visible": false, "is_folder": true},
			{"Наименование": "QUERY-MY-ROW", "Author": "u-op", "Marker": "keep", "Visible": true},
			{"Наименование": "QUERY-OTHER-ROW", "Author": "u-other", "Marker": "keep", "Visible": true},
			{"Наименование": "QUERY-FIELD-EXCLUDED", "Author": "u-op", "Marker": "skip", "Visible": true},
		} {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), row, entity); err != nil {
				t.Fatal(err)
			}
		}
		s := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		s.Mount(router)
		conditions := append([]metadata.FormListCondition(nil), entity.Forms[0].ListFilter...)
		for _, mode := range []string{"query", "query_and_field", "field"} {
			t.Run(mode, func(t *testing.T) {
				switch mode {
				case "query":
					entity.Forms[0].ListFilter = conditions[:1]
				case "query_and_field":
					entity.Forms[0].ListFilter = conditions
				case "field":
					entity.Forms[0].ListFilter = conditions[1:]
				}
				for _, userID := range []string{"u-op", "u-empty"} {
					t.Run(userID, func(t *testing.T) {
						user := &auth.User{ID: userID, Login: userID, Roles: []*auth.Role{{Name: "Оператор", Permissions: auth.Permission{
							Catalogs: map[string][]string{entity.Name: {"read"}},
							RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{entity.Name: {"read": {
								Field: "Visible", Op: "eq", Value: auth.RowValue{Literal: true},
							}}}},
						}}}}
						for _, view := range []string{"list", "tree"} {
							req := httptest.NewRequest(http.MethodGet, "/ui/catalog/Tasks?view="+view, nil)
							req = req.WithContext(auth.ContextWithUser(req.Context(), user))
							rec := httptest.NewRecorder()
							router.ServeHTTP(rec, req)
							if rec.Code != http.StatusOK {
								t.Fatalf("%s: HTTP %d: %.500s", view, rec.Code, rec.Body.String())
							}
							wantMine := mode == "field" || userID == "u-op"
							for label, want := range map[string]bool{
								"QUERY-MY-FOLDER": wantMine, "QUERY-MY-ROW": wantMine,
								"QUERY-OTHER-FOLDER": mode == "field", "QUERY-OTHER-ROW": mode == "field",
								"QUERY-DENIED-FOLDER":  false,
								"QUERY-FIELD-EXCLUDED": mode == "query" && userID == "u-op",
							} {
								if got := strings.Contains(rec.Body.String(), label); got != want {
									t.Errorf("%s %s: present=%t, want %t", view, label, got, want)
								}
							}
						}
					})
				}
			})
		}
	})
}
