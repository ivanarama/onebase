package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the actual GET config and POST transport: each list sends only its
// own exact source paths, even when another list names the same field differently.
func TestManagedEntityListSelectedSourcesMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := entityListFixture(t)
		ctx := context.Background()
		f.owner.Fields = append(f.owner.Fields, metadata.Field{Name: "Другое", Type: metadata.FieldType("reference:" + f.direction.Name), RefEntity: f.direction.Name})
		f.owner.TableParts = []metadata.TablePart{{Name: "Items", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}}
		entities := []*metadata.Entity{f.direction, f.target, f.owner}
		if err := f.server.store.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		for _, entity := range entities {
			rows, err := f.server.store.List(ctx, entity.Name, entity, storage.ListParams{Sort: "id", RowFilterEvaluated: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				_, id, ok := uuidFromValue(row["id"])
				if !ok {
					t.Fatal("invalid fixture id")
				}
				if err := db.Upsert(ctx, entity.Name, id, row, entity); err != nil {
					t.Fatal(err)
				}
			}
		}
		f.server.store = db
		f.server.entitySvc = f.server.newEntityService(nil)
		tp := f.owner.TableParts[0]
		if err := db.UpsertTablePartRows(ctx, f.owner.Name, tp.Name, f.ownerID, []map[string]any{{"Text": "keep"}}, tp); err != nil {
			t.Fatal(err)
		}
		before, err := db.GetByID(ctx, f.owner.Name, f.ownerID, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		beforeTP, err := db.GetTablePartRows(ctx, f.owner.Name, tp.Name, f.ownerID, tp)
		if err != nil {
			t.Fatal(err)
		}
		original := append([]*metadata.FormElement(nil), f.owner.Forms[0].Elements[:2]...)
		router := chi.NewRouter()
		f.server.Mount(router)
		request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			return w
		}
		for _, prefix := range []string{"Объект.", "Форма."} {
			for _, reverse := range []bool{false, true} {
				form := f.owner.Forms[0]
				name := "Направление"
				if prefix == "Форма." {
					name = "Поиск"
				}
				path := prefix + name
				alias := strings.ToLower(path)
				otherName := "Другое"
				if prefix == "Форма." {
					otherName = "ДругойПоиск"
				}
				other := prefix + otherName
				form.Elements = append([]*metadata.FormElement(nil), original...)
				form.Attributes = nil
				if prefix == "Форма." {
					form.Attributes = []*metadata.FormAttribute{{Name: name, TypeRef: "CatalogRef." + f.direction.Name}, {Name: otherName, TypeRef: "CatalogRef." + f.direction.Name}}
					form.Elements = append(form.Elements, &metadata.FormElement{ID: "search", Name: "Search", Kind: metadata.FormElementField, DataPath: path})
				}
				form.Elements = append(form.Elements, &metadata.FormElement{ID: "other-source", Name: "Other", Kind: metadata.FormElementField, DataPath: other})
				lists := []*metadata.FormElement{}
				for i, p := range []string{path, alias, other} {
					id := []string{"history", "alias-history", "other-history"}[i]
					lists = append(lists, &metadata.FormElement{ID: id, Name: id, Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: f.target.Name, Columns: []string{"Наименование"}, OrderBy: []string{"Наименование asc"}, Limit: 2, Filter: []metadata.FormEntityListCondition{{Field: "Направление", Op: "eq", Source: p}}}})
				}
				if reverse {
					lists[0], lists[2] = lists[2], lists[0]
				}
				form.Elements = append(form.Elements, lists...)
				if err := metadata.ValidateFormEntityLists(f.owner, form, f.server.reg.Entities()); err != nil {
					t.Fatal(err)
				}
				for _, login := range []string{"anna", "bob"} {
					f.user.Login = login
					for _, id := range []string{f.ownerID.String(), ""} {
						urlID := id
						if urlID == "" {
							urlID = "new"
						}
						page := request(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+urlID, nil)
						if page.Code != 200 {
							t.Fatalf("GET %d: %s", page.Code, page.Body.String())
						}
						_, config, ok := strings.Cut(page.Body.String(), `id="ob-entity-list-config">`)
						if !ok {
							t.Fatal("missing config")
						}
						config, _, ok = strings.Cut(config, "</script>")
						if !ok {
							t.Fatal("unterminated config")
						}
						var state struct {
							State string `json:"state"`
						}
						if err := json.Unmarshal([]byte(config), &state); err != nil || state.State == "" {
							t.Fatalf("config %v", err)
						}
						refresh := func(element string, sources map[string]string, token string) *httptest.ResponseRecorder {
							t.Helper()
							b, err := json.Marshal(sources)
							if err != nil {
								t.Fatal(err)
							}
							return request(http.MethodPost, "/ui/document/"+url.PathEscape(f.owner.Name)+"/form-list-refresh", url.Values{"form": {form.Name}, "element": {element}, "id": {id}, "sources": {string(b)}, "state": {token}})
						}
						for _, el := range lists {
							p := el.EntityList.Filter[0].Source
							for _, value := range []string{f.rootA.String(), f.rootB.String(), "", "missing"} {
								sources := map[string]string{p: value}
								if value == "missing" {
									sources = map[string]string{}
								}
								// Repeat identical requests to catch the former map-order dependent loss.
								for attempt := 0; attempt < 8; attempt++ {
									w := refresh(el.ID, sources, state.State)
									var body struct {
										OK    bool                      `json:"ok"`
										Lists map[string]entityListData `json:"entityLists"`
									}
									if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || !body.OK || len(body.Lists) != 1 {
										t.Fatalf("refresh %d %s: %v", w.Code, w.Body.String(), err)
									}
									got := body.Lists[el.ID]
									want := 2
									if value == "" || value == "missing" || login == "bob" && value == f.rootB.String() {
										want = 0
									} else if login == "bob" {
										want = 1
									}
									if len(got.Rows) != want || want == 0 && got.Status != "empty" || want > 0 && got.Status != "ready" {
										t.Fatalf("%s reverse=%v %s id=%s %s=%s attempt=%d: %+v", prefix, reverse, login, id, p, value, attempt, got)
									}
									for _, r := range got.Rows {
										text := strings.Join(r.Cells, " ")
										if (login == "anna" && strings.Contains(text, "hidden")) || (value == f.rootB.String() && !strings.Contains(text, " B")) || (value == f.rootA.String() && strings.Contains(text, " B")) {
											t.Fatalf("source/RLS: %+v", got)
										}
									}
								}
							}
						}
						for _, bad := range []map[string]string{{other: f.rootA.String()}, {alias: f.rootA.String()}, {"unexpected": f.rootA.String()}} {
							if w := refresh("history", bad, state.State); w.Code != 403 {
								t.Fatalf("foreign source accepted: %d %s", w.Code, w.Body.String())
							}
						}
						// A readonly source uses saved object state or the signed GET state;
						// another list's editable control must not grant write access to it.
						for _, el := range form.Elements {
							if strings.EqualFold(el.DataPath, path) {
								el.ReadOnly = true
							}
						}
						page = request(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+urlID, nil)
						_, config, ok = strings.Cut(page.Body.String(), `id="ob-entity-list-config">`)
						if !ok {
							t.Fatal("missing readonly config")
						}
						config, _, _ = strings.Cut(config, "</script>")
						if err := json.Unmarshal([]byte(config), &state); err != nil {
							t.Fatal(err)
						}
						for _, elID := range []string{"history", "alias-history"} {
							p := path
							if elID == "alias-history" {
								p = alias
							}
							w := refresh(elID, map[string]string{p: f.rootB.String()}, state.State)
							var body struct {
								Lists map[string]entityListData `json:"entityLists"`
							}
							if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
								t.Fatalf("readonly: %d %s", w.Code, w.Body.String())
							}
							got := body.Lists[elID]
							want := 0
							if prefix == "Объект." && id != "" {
								want = 2
								if login == "bob" {
									want = 1
								}
							}
							if len(got.Rows) != want {
								t.Fatalf("forged readonly: %+v", got)
							}
							for _, row := range got.Rows {
								if strings.Contains(strings.Join(row.Cells, " "), " B") {
									t.Fatalf("readonly source changed to forged B: %+v", got)
								}
							}
						}
						if id == "" || prefix == "Форма." {
							otherUser := "bob"
							if login == "bob" {
								otherUser = "anna"
							}
							f.user.Login = otherUser
							if w := refresh("history", map[string]string{path: f.rootB.String()}, state.State); w.Code != 403 {
								t.Fatalf("cross-user state: %d %s", w.Code, w.Body.String())
							}
							f.user.Login = login
							if w := refresh("history", map[string]string{path: f.rootB.String()}, state.State+"x"); w.Code != 403 {
								t.Fatalf("forged state: %d %s", w.Code, w.Body.String())
							}
						}
						for _, el := range form.Elements {
							if strings.EqualFold(el.DataPath, path) {
								el.ReadOnly = false
							}
						}
					}
				}
			}
		}
		after, err := db.GetByID(ctx, f.owner.Name, f.ownerID, f.owner)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("refresh wrote owner: %v -> %v (%v)", before, after, err)
		}
		afterTP, err := db.GetTablePartRows(ctx, f.owner.Name, tp.Name, f.ownerID, tp)
		if err != nil || !reflect.DeepEqual(beforeTP, afterTP) {
			t.Fatalf("refresh wrote table part: %v -> %v (%v)", beforeTP, afterTP, err)
		}
	})
}
