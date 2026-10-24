package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

// Один и тот же отбор и его переключатель действуют в списке, Excel и дереве.
func TestUI_ListFormFilterEntryPointsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		// Load the combined contracts through the same project loader as the CLI.
		dir := t.TempDir()
		for name, body := range map[string]string{
			"catalogs/заявка.yaml": `name: Заявка
hierarchical: true
list_filters: [Автор]
fields:
  - {name: Наименование, type: string}
  - {name: Автор, type: string}
  - {name: Метка, type: string}
`,
			"forms/заявка/списка.form.yaml": `schema: onebase.form/v1
form:
  name: ФормаСписка
  kind: list
  entity: Заявка
  ref_card_button_admin_only: true
  enter_submits_form: true
  filter:
    - query: ВЫБРАТЬ З.Ссылка КАК Ссылка ИЗ Справочник.Заявка КАК З ГДЕ З.Автор = &ТекущийПользователь
      roles: [Оператор]
      removable: true
      label: {ru: Только мои}
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
		p, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Entities) != 1 || len(p.Entities[0].Forms) != 1 {
			t.Fatal("project lost the list form")
		}
		entity := p.Entities[0]
		form := entity.Forms[0]
		if !form.RefCardButtonAdminOnly || !form.EnterSubmitsForm || len(form.ListFilter) != 1 || !form.ListFilter[0].Removable || form.ListFilter[0].Query != listQueryMine {
			t.Fatalf("combined YAML keys lost: %+v", form)
		}
		condition := form.ListFilter[0]
		s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		s.store = db
		s.entitySvc.Store = db
		parent := uuid.New()
		if err := db.Upsert(ctx, entity.Name, parent, map[string]any{"Наименование": "Folder", "Автор": "u-op", "Метка": "keep", "is_folder": true}, entity); err != nil {
			t.Fatal(err)
		}
		for _, row := range []map[string]any{
			{"Наименование": "first-child", "Автор": "u-op", "Метка": "keep", "parent_id": parent.String(), "is_folder": true},
			{"Наименование": "second-child", "Автор": "u-other", "Метка": "keep", "parent_id": parent.String()},
			{"Наименование": "excluded-child", "Автор": "u-op", "Метка": "skip", "parent_id": parent.String()},
		} {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), row, entity); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			name      string
			removable bool
			off       string
			admin     bool
			otherRole bool
			wantOther bool
		}{
			{name: "permanent"},
			{name: "permanent_cannot_be_removed", off: "0"},
			{name: "default_on", removable: true},
			{name: "default_off", removable: true, off: "0", wantOther: true},
			{name: "administrator", admin: true, wantOther: true},
			{name: "other_role", removable: true, otherRole: true, wantOther: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				condition.Removable = tc.removable
				form.ListFilter = []metadata.FormListCondition{condition}
				u := listQueryUser("u-op")
				u.IsAdmin = tc.admin
				if tc.otherRole {
					u.Roles[0].Name = "Контроль"
				}
				back := "/ui/catalog/заявка?view=tree&f.Метка=keep"
				if tc.off != "" {
					back += "&lf_off=" + tc.off
				}
				saved := listFiltersPost(t, s, u, entity.Name, back, "Метка")
				if saved.Code != http.StatusSeeOther || location(saved) != back {
					t.Fatalf("save panel fields: status=%d location=%q", saved.Code, location(saved))
				}
				qs := url.Values{"parent": {parent.String()}, "f.Метка": {"keep"}}
				if tc.off != "" {
					qs.Set(listFormFilterOffParam, tc.off)
				}
				check := func(body string) {
					t.Helper()
					if !strings.Contains(body, "first-child") || strings.Contains(body, "second-child") != tc.wantOther || strings.Contains(body, "excluded-child") {
						t.Fatal("list entries differ from the selected form filter")
					}
				}
				// Follow the rendered root-folder button and the URL base used by
				// Enter/context-menu navigation, rather than supplying lf_off ourselves.
				rootQuery := url.Values{"view": {"tree"}, "f.Метка": {"keep"}}
				if tc.off != "" {
					rootQuery.Set(listFormFilterOffParam, tc.off)
				}
				listPage := func(t *testing.T, target string) string {
					t.Helper()
					r := reqWithChi(http.MethodGet, target, nil, map[string]string{"kind": "catalog", "entity": entity.Name})
					r = r.WithContext(auth.ContextWithUser(r.Context(), u))
					w := httptest.NewRecorder()
					s.list(w, r)
					if w.Code != http.StatusOK {
						t.Fatalf("list: HTTP %d", w.Code)
					}
					body := w.Body.String()
					if !strings.Contains(body, `name="f.Метка"`) || !strings.Contains(body, `name="field" value="Метка" checked`) {
						t.Fatal("saved extra panel field lost alongside form filter toggles")
					}
					if tc.removable && !tc.admin && !tc.otherRole {
						pressed := "true"
						if tc.off != "" {
							pressed = "false"
						}
						if !strings.Contains(body, "Только мои") || !strings.Contains(body, `aria-pressed="`+pressed+`"`) {
							t.Fatal("form toggle lost alongside saved panel fields")
						}
					}
					return body
				}
				doc, err := html.Parse(strings.NewReader(listPage(t, "/ui/catalog/"+entity.Name+"?"+rootQuery.Encode())))
				if err != nil {
					t.Fatal(err)
				}
				var buttonURL, rowBaseURL, toggleURL string
				var walk func(*html.Node)
				walk = func(n *html.Node) {
					if n.Type == html.ElementNode {
						if value, ok := keyboardHTMLAttr(n, "data-ob-row-list-url"); ok {
							rowBaseURL = value
						}
						if n.Data == "a" {
							value, _ := keyboardHTMLAttr(n, "href")
							if _, ok := keyboardHTMLAttr(n, "aria-pressed"); ok {
								toggleURL = value
							}
							if link, err := url.Parse(value); err == nil && link.Query().Get("parent") == parent.String() {
								buttonURL = value
							}
						}
					}
					for child := n.FirstChild; child != nil; child = child.NextSibling {
						walk(child)
					}
				}
				walk(doc)
				if tc.removable && !tc.admin && !tc.otherRole {
					if toggleURL == "" {
						t.Fatal("removable toggle link missing")
					}
					toggled, err := url.Parse(toggleURL)
					if err != nil {
						t.Fatal(err)
					}
					toggled.Path = "/ui/catalog/" + entity.Name
					q := toggled.Query()
					q.Del("view")
					q.Set("parent", parent.String())
					toggled.RawQuery = q.Encode()
					r := reqWithChi(http.MethodGet, toggled.String(), nil, map[string]string{"kind": "catalog", "entity": entity.Name})
					r = r.WithContext(auth.ContextWithUser(r.Context(), u))
					rec := httptest.NewRecorder()
					s.list(rec, r)
					if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "first-child") || strings.Contains(rec.Body.String(), "second-child") == tc.wantOther || strings.Contains(rec.Body.String(), "excluded-child") {
						t.Fatal("following the rendered toggle lost the panel filter or did not switch the form condition")
					}
				}
				for name, target := range map[string]string{"root_button": buttonURL, "root_row_navigation": rowBaseURL} {
					t.Run(name, func(t *testing.T) {
						if target == "" {
							t.Fatal("root folder navigation URL missing")
						}
						link, err := url.Parse(target)
						if err != nil {
							t.Fatal(err)
						}
						q := link.Query()
						if q.Get(listFormFilterOffParam) != tc.off {
							t.Fatalf("root folder link lost the filter toggle: %s", target)
						}
						if q.Get("view") != "" {
							t.Fatal("entering a folder must leave the root tree view")
						}
						if name == "root_row_navigation" {
							// obRowUrl adds the selected row's parent parameter to this base.
							q.Set("parent", parent.String())
							link.RawQuery = q.Encode()
						} else if q.Get("parent") != parent.String() {
							t.Fatal("root button points to a different folder")
						}
						if q.Get("f.Метка") != "keep" {
							t.Fatal("root folder link lost the panel filter")
						}
						body := listPage(t, link.String())
						if !strings.Contains(body, "first-child") || strings.Contains(body, "second-child") != tc.wantOther || strings.Contains(body, "excluded-child") {
							t.Fatal("root folder navigation changed the selected form filter")
						}
					})
				}
				for _, export := range []bool{false, true} {
					path := "/ui/catalog/" + entity.Name
					if export {
						path += "/excel"
					}
					r := reqWithChi(http.MethodGet, path+"?"+qs.Encode(), nil, map[string]string{"kind": "catalog", "entity": entity.Name})
					r = r.WithContext(auth.ContextWithUser(r.Context(), u))
					w := httptest.NewRecorder()
					if export {
						s.listExcel(w, r)
					} else {
						s.list(w, r)
					}
					if w.Code != http.StatusOK {
						t.Fatalf("export=%v: HTTP %d", export, w.Code)
					}
					if !export {
						check(w.Body.String())
						if !strings.Contains(w.Body.String(), `"listFormOff":"`+tc.off+`"`) {
							t.Fatal("list configuration lost the filter toggle")
						}
						continue
					}
					book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
					if err != nil {
						t.Fatal(err)
					}
					rows, err := book.GetRows(book.GetSheetName(0))
					if closeErr := book.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err != nil {
						t.Fatal(err)
					}
					var text strings.Builder
					for _, row := range rows {
						text.WriteString(strings.Join(row, " "))
						text.WriteByte('\n')
					}
					check(text.String())
				}
				// Lazy tree expansion has no panel-filter contract; exercise the
				// form condition and its toggle on that entry point separately.
				treeQuery := url.Values{"parent": {parent.String()}, "depth": {"0"}}
				if tc.off != "" {
					treeQuery.Set(listFormFilterOffParam, tc.off)
				}
				w := serveTreeChildren(t, s, entity.Name, treeQuery.Encode(), u)
				if w.Code != http.StatusOK {
					t.Fatalf("tree: HTTP %d", w.Code)
				}
				var body treeChildrenResponse
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				for _, row := range body.Rows {
					text.WriteString(strings.Join(row.Cells, " "))
					folder, err := url.Parse(row.FolderURL)
					if err != nil || folder.Query().Get(listFormFilterOffParam) != tc.off {
						t.Fatal("tree folder link lost the filter toggle")
					}
				}
				if !strings.Contains(text.String(), "first-child") || strings.Contains(text.String(), "second-child") != tc.wantOther {
					t.Fatal("lazy tree entries differ from the selected form filter")
				}
			})
		}
	})
}
