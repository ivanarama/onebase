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
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUIListFormValueQueryFoldersMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		for name, body := range map[string]string{
			"catalogs/departments.yaml": "name: Departments\n",
			"catalogs/assignments.yaml": `name: Assignments
fields:
  - {name: Account, type: string}
  - {name: Department, type: "reference:Departments"}
`,
			"catalogs/tasks.yaml": `name: Tasks
hierarchical: true
fields:
  - {name: Наименование, type: string}
  - {name: Department, type: "reference:Departments"}
  - {name: Visible, type: bool}
`,
			"forms/tasks/list.form.yaml": `schema: onebase.form/v1
form:
  name: TaskList
  kind: list
  entity: Tasks
  filter:
    - field: Department
      op: eq
      value_query: ВЫБРАТЬ А.Department ИЗ Справочник.Assignments КАК А ГДЕ А.Account = &ТекущийПользователь
      roles: [Operator]
elements:
  - {kind: ПолеВвода, name: NameColumn, data_path: Наименование}
`,
		} {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
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
		department := uuid.New()
		put := func(entity string, id uuid.UUID, fields map[string]any) {
			t.Helper()
			if err := db.Upsert(ctx, entity, id, fields, reg.GetEntity(entity)); err != nil {
				t.Fatal(err)
			}
		}
		put("Departments", department, nil)
		assigned := uuid.NewString()
		put("Assignments", uuid.New(), map[string]any{"Account": assigned, "Department": department.String()})
		for _, row := range []struct {
			name    string
			folder  bool
			visible bool
		}{
			{"VALUEQUERY-FOLDER", true, true},
			{"VALUEQUERY-ROW", false, true},
			{"VALUEQUERY-DENIED-FOLDER", true, false},
		} {
			put("Tasks", uuid.New(), map[string]any{
				"Наименование": row.name, "Department": department.String(),
				"is_folder": row.folder, "Visible": row.visible,
			})
		}
		s := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		s.Mount(router)
		for _, tc := range []struct {
			name, id string
			want     bool
		}{{"assigned", assigned, true}, {"unassigned", uuid.NewString(), false}} {
			t.Run(tc.name, func(t *testing.T) {
				user := &auth.User{ID: tc.id, Login: tc.name, Roles: []*auth.Role{{Name: "Operator", Permissions: auth.Permission{
					Catalogs: map[string][]string{"Tasks": {"read"}, "Departments": {"read"}, "Assignments": {"read"}},
					RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{"Tasks": {"read": {
						Field: "Visible", Op: "eq", Value: auth.RowValue{Literal: true},
					}}}},
				}}}}
				for _, path := range []string{"/ui/catalog/Tasks?view=list", "/ui/catalog/Tasks?view=tree", "/ui/_tree-children/Tasks"} {
					t.Run(path, func(t *testing.T) {
						req := httptest.NewRequest(http.MethodGet, path, nil)
						req = req.WithContext(auth.ContextWithUser(req.Context(), user))
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						if rec.Code != http.StatusOK {
							t.Fatalf("HTTP %d: %.500s", rec.Code, rec.Body.String())
						}
						for label, want := range map[string]bool{
							"VALUEQUERY-FOLDER": tc.want, "VALUEQUERY-ROW": tc.want,
							"VALUEQUERY-DENIED-FOLDER": false,
						} {
							if got := strings.Contains(rec.Body.String(), label); got != want {
								t.Errorf("%s: present=%t, want %t", label, got, want)
							}
						}
					})
				}
			})
		}
	})
}
