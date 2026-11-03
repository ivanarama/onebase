package ui

import (
	"bytes"
	"context"
	"fmt"
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
	"github.com/xuri/excelize/v2"
)

// Check every permitted kind of UUID leaf through YAML, public validation and
// mounted list/export routes. A missing outer reference must not match a test
// on a related row, while NULL and empty strings remain equivalent for text.
func TestUIListFormNestedReferenceEmptyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		user, err := repo.Create(ctx, "empty-filter-user", "SecurePassword123!", "User", true)
		if err != nil {
			t.Fatal(err)
		}
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
		write := func(name, body string) {
			t.Helper()
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := root.WriteFile(name, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("catalogs/tasks.yaml", `name: Tasks
fields:
  - name: Наименование
    type: string
  - name: Person
    type: reference:People
  - name: Note
    type: string
`)
		write("catalogs/people.yaml", `name: People
hierarchical: true
fields:
  - name: Наименование
    type: string
  - name: Owner
    type: reference:_users
  - name: Manager
    type: reference:People
`)
		form := func(field, op string) {
			t.Helper()
			write("forms/tasks/list.form.yaml", fmt.Sprintf(`schema: onebase.form/v1
form:
  name: TaskList
  kind: list
  entity: Tasks
  filter:
    - field: %s
      op: %s
elements:
  - kind: ПолеВвода
    name: NameColumn
    data_path: Наименование
`, field, op))
		}
		form("Person", "empty")
		proj, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer proj.Close()
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: proj.Entities})
		if err := db.Migrate(ctx, proj.Entities); err != nil {
			t.Fatal(err)
		}
		put := func(entity string, id uuid.UUID, row map[string]any) {
			t.Helper()
			if err := db.Upsert(ctx, entity, id, row, reg.GetEntity(entity)); err != nil {
				t.Fatal(err)
			}
		}
		emptyPerson, fullPerson := uuid.New(), uuid.New()
		put("People", emptyPerson, map[string]any{"Наименование": "Empty person", "is_folder": true})
		put("People", fullPerson, map[string]any{"Наименование": "Full person", "Owner": user.ID, "Manager": emptyPerson.String(), "parent_id": emptyPerson})
		labels := []string{"TASK-NONE", "TASK-EMPTY", "TASK-FULL"}
		put("Tasks", uuid.New(), map[string]any{"Наименование": labels[0]})
		put("Tasks", uuid.New(), map[string]any{"Наименование": labels[1], "Person": emptyPerson.String(), "Note": ""})
		put("Tasks", uuid.New(), map[string]any{"Наименование": labels[2], "Person": fullPerson.String(), "Note": "text"})
		server := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		server.Mount(router)
		for _, tc := range []struct {
			field           string
			empty, nonempty [3]bool
		}{
			{"Person", [3]bool{true, false, false}, [3]bool{false, true, true}},
			{"Person.Owner", [3]bool{false, true, false}, [3]bool{false, false, true}},
			{"person.MANAGER", [3]bool{false, true, false}, [3]bool{false, false, true}},
			{"Person.parent_id", [3]bool{false, true, false}, [3]bool{false, false, true}},
			{"Person.id", [3]bool{}, [3]bool{false, true, true}},
			{"Person.Ссылка", [3]bool{}, [3]bool{false, true, true}},
			{"id", [3]bool{}, [3]bool{true, true, true}},
			{"Ссылка", [3]bool{}, [3]bool{true, true, true}},
			{"Note", [3]bool{true, true, false}, [3]bool{false, false, true}},
		} {
			for _, op := range []string{"empty", "not_empty"} {
				t.Run(tc.field+"/"+op, func(t *testing.T) {
					form(tc.field, op)
					if result := configcheck.RunFull(dir); !result.OK {
						t.Fatalf("check: %+v", result.Issues)
					}
					loaded, err := project.Load(dir)
					if err != nil {
						t.Fatal(err)
					}
					for _, entity := range loaded.Entities {
						if entity.Name == "Tasks" {
							reg.GetEntity("Tasks").Forms = entity.Forms
						}
					}
					loaded.Close()
					want := tc.empty
					if op == "not_empty" {
						want = tc.nonempty
					}
					for _, path := range []string{"/ui/catalog/Tasks?view=list", "/ui/catalog/Tasks/excel"} {
						req := httptest.NewRequest(http.MethodGet, path, nil)
						req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: user.ID, IsAdmin: true}))
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						if rec.Code != http.StatusOK {
							t.Fatalf("%s: %d %.500s", path, rec.Code, rec.Body.String())
						}
						body := rec.Body.String()
						if strings.HasSuffix(path, "/excel") {
							book, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
							if err != nil {
								t.Fatal(err)
							}
							rows, err := book.GetRows(book.GetSheetName(0))
							closeErr := book.Close()
							if err != nil {
								t.Fatal(err)
							}
							if closeErr != nil {
								t.Fatal(closeErr)
							}
							body = fmt.Sprint(rows)
						}
						for i, label := range labels {
							if got := strings.Contains(body, label); got != want[i] {
								t.Errorf("%s %s present=%v want=%v", path, label, got, want[i])
							}
						}
					}
				})
			}
		}
	})
}
