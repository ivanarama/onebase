package ui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// YAML -> check -> mounted HTTP routes -> real SQL on both dialects. Comparing
// to the direct reference also protects NULL/empty semantics without joining
// arbitrary attributes of the platform's private user table.
func TestUIListFormSystemUserIDMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		mine, err := repo.Create(ctx, "system-filter-mine", "SecurePassword123!", "Mine", true)
		if err != nil {
			t.Fatal(err)
		}
		other, err := repo.Create(ctx, "system-filter-other", "SecurePassword123!", "Other", false)
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
hierarchical: true
fields:
  - name: Наименование
    type: string
  - name: Author
    type: reference:_users
  - name: Инициатор
    type: reference:People
  - name: Visible
    type: bool
`)
		write("catalogs/people.yaml", `name: People
fields:
  - name: Наименование
    type: string
  - name: УчётнаяЗапись
    type: reference:_users
`)
		form := func(field, op, value, roles string) {
			t.Helper()
			write("forms/tasks/list.form.yaml", fmt.Sprintf(`schema: onebase.form/v1
form:
  name: TaskList
  kind: list
  entity: Tasks
  filter:
    - field: %s
      op: %s
      value: %q
      roles: %s
elements:
  - kind: ПолеВвода
    name: NameColumn
    data_path: Наименование
`, field, op, value, roles))
		}
		form("Author", "eq", mine.ID, "[]")
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
		entity := reg.GetEntity("Tasks")
		people := reg.GetEntity("People")
		s := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		s.Mount(router)
		folder := uuid.New()
		put := func(name string, id uuid.UUID, row map[string]any) {
			t.Helper()
			if err := db.Upsert(ctx, name, id, row, reg.GetEntity(name)); err != nil {
				t.Fatal(err)
			}
		}
		put(entity.Name, folder, map[string]any{"Наименование": "FILTER-FOLDER", "is_folder": true, "Visible": true})
		put(entity.Name, uuid.New(), map[string]any{"Наименование": "DENIED-FOLDER", "is_folder": true, "Visible": false})
		for _, item := range []struct {
			name, author string
			visible      bool
		}{
			{"MY-SYSTEM-ROW", mine.ID, true}, {"OTHER-SYSTEM-ROW", other.ID, true},
			{"EMPTY-SYSTEM-ROW", "", true}, {"DENIED-SYSTEM-ROW", mine.ID, false},
		} {
			row := map[string]any{"Наименование": item.name, "parent_id": folder, "Visible": item.visible}
			if item.author != "" {
				person := uuid.New()
				put(people.Name, person, map[string]any{"Наименование": "Person", "УчётнаяЗапись": item.author})
				row["Author"], row["Инициатор"] = item.author, person.String()
			}
			put(entity.Name, uuid.New(), row)
		}
		role := func(name string, rls bool) []*auth.Role {
			p := auth.Permission{Catalogs: map[string][]string{entity.Name: {"read"}, people.Name: {"read"}}}
			if rls {
				p.RowAccess = auth.RowAccess{Catalogs: map[string]auth.RowPolicies{entity.Name: {"read": {Field: "Visible", Op: "eq", Value: auth.RowValue{Literal: true}}}}}
			}
			return []*auth.Role{{Name: name, Permissions: p}}
		}
		operator := &auth.User{ID: mine.ID, Login: mine.Login, Roles: role("оПеРаТоР", false)}
		restricted := &auth.User{ID: mine.ID, Login: mine.Login, Roles: role("Оператор", true)}
		admin := &auth.User{ID: mine.ID, Login: mine.Login, IsAdmin: true}
		outsider := &auth.User{ID: other.ID, Login: other.Login, Roles: role("Контроль", false)}
		labels := []string{"MY-SYSTEM-ROW", "OTHER-SYSTEM-ROW", "EMPTY-SYSTEM-ROW", "DENIED-SYSTEM-ROW"}
		all := []bool{true, true, true, true}
		type scenario struct {
			name, op, value, roles string
			user                   *auth.User
			want                   []bool
		}
		cases := []scenario{
			{"literal-eq", "eq", mine.ID, "[]", operator, []bool{true, false, false, true}},
			{"literal-ne", "ne", mine.ID, "[]", operator, []bool{false, true, false, false}},
			{"literal-empty", "empty", mine.ID, "[]", operator, []bool{false, false, true, false}},
			{"literal-not-empty", "not_empty", mine.ID, "[]", operator, []bool{true, true, false, true}},
			{"current-eq", "eq", "@ТекущийПользователь", "[]", operator, []bool{true, false, false, true}},
			{"current-ne", "ne", "@ТекущийПользователь", "[]", operator, []bool{false, true, false, false}},
			{"current-empty", "empty", "@ТекущийПользователь", "[]", operator, []bool{false, false, true, false}},
			{"current-not-empty", "not_empty", "@ТекущийПользователь", "[]", operator, []bool{true, true, false, true}},
			{"role-match", "eq", "@ТекущийПользователь", "[Оператор]", operator, []bool{true, false, false, true}},
			{"role-other", "eq", "@ТекущийПользователь", "[Оператор]", outsider, all},
			{"role-no-user", "eq", mine.ID, "[Оператор]", nil, all},
			{"current-admin", "eq", "@ТекущийПользователь", "[]", admin, all},
			{"current-no-user", "eq", "@ТекущийПользователь", "[]", nil, all},
			{"literal-admin", "eq", mine.ID, "[]", admin, []bool{true, false, false, true}},
			{"literal-no-user", "eq", mine.ID, "[]", nil, []bool{true, false, false, true}},
			{"rls", "eq", "@ТекущийПользователь", "[]", restricted, []bool{true, false, false, false}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var direct [][]bool
				fields := []string{"Author", "Author.id", "author.ID"}
				if tc.op == "empty" || tc.op == "not_empty" {
					fields = append(fields, "Инициатор", "инициатор")
				}
				for _, field := range fields {
					form(field, tc.op, tc.value, tc.roles)
					if res := configcheck.RunFull(dir); !res.OK {
						t.Fatalf("check %s: %+v", field, res.Issues)
					}
					loaded, err := project.Load(dir)
					if err != nil {
						t.Fatal(err)
					}
					for _, ent := range loaded.Entities {
						if ent.Name == entity.Name {
							entity.Forms = ent.Forms
						}
					}
					loaded.Close()
					var gotViews [][]bool
					for _, path := range []string{
						"/ui/catalog/Tasks?view=list&parent=" + folder.String(),
						"/ui/_tree-children/Tasks?parent=" + folder.String() + "&depth=0",
						"/ui/catalog/Tasks/excel",
						"/ui/catalog/Tasks?view=list", "/ui/catalog/Tasks?view=tree", "/ui/_tree-children/Tasks",
					} {
						req := httptest.NewRequest(http.MethodGet, path, nil)
						if tc.user != nil {
							req = req.WithContext(auth.ContextWithUser(req.Context(), tc.user))
						}
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						if rec.Code != http.StatusOK {
							t.Fatalf("%s %s: %d %.500s", field, path, rec.Code, rec.Body.String())
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
						got := make([]bool, len(labels))
						for i, label := range labels {
							got[i] = strings.Contains(body, label)
						}
						gotViews = append(gotViews, got)
						root := strings.HasSuffix(path, "?view=list") || strings.HasSuffix(path, "?view=tree") || path == "/ui/_tree-children/Tasks"
						if root {
							if !strings.Contains(body, "FILTER-FOLDER") {
								t.Fatalf("%s %s hides navigation folder", field, path)
							}
							if tc.user == restricted && strings.Contains(body, "DENIED-FOLDER") {
								t.Fatalf("%s %s bypasses folder RLS", field, path)
							}
						} else if !reflect.DeepEqual(got, tc.want) {
							t.Fatalf("%s %s: rows=%v want=%v", field, path, got, tc.want)
						}
					}
					if field == "Author" {
						direct = gotViews
					} else if !reflect.DeepEqual(gotViews, direct) {
						t.Fatalf("%s differs from direct reference: %v != %v", field, gotViews, direct)
					}
				}
			})
		}
		// Ordinary configured reference navigation keeps working, including a
		// reference to _users as the final attribute of a configured entity.
		form("Инициатор.УчётнаяЗапись", "eq", mine.ID, "[]")
		if res := configcheck.RunFull(dir); !res.OK {
			t.Fatalf("ordinary navigation: %+v", res.Issues)
		}
		loaded, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer loaded.Close()
		for _, ent := range loaded.Entities {
			if ent.Name == entity.Name {
				entity.Forms = ent.Forms
			}
		}
		req := httptest.NewRequest(http.MethodGet, "/ui/catalog/Tasks?parent="+folder.String(), nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), operator))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "MY-SYSTEM-ROW") || strings.Contains(rec.Body.String(), "OTHER-SYSTEM-ROW") {
			t.Fatalf("ordinary navigation: %d %.500s", rec.Code, rec.Body.String())
		}
	})
}
