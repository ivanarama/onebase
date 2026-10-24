package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestRefComboboxSelectedCheckPublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "Выбор", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Область", Type: metadata.FieldTypeString}, {Name: "Аудитория", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{target}); err != nil {
			t.Fatal(err)
		}
		good, other, hidden := uuid.New(), uuid.New(), uuid.New()
		for _, seed := range []struct {
			id         uuid.UUID
			area, user string
		}{{good, "A", "anna"}, {other, "B", "anna"}, {hidden, "A", "bob"}} {
			if err := db.Upsert(ctx, target.Name, seed.id, map[string]any{"Наименование": "needle secret", "Область": seed.area, "Аудитория": seed.user}, target); err != nil {
				t.Fatal(err)
			}
		}
		user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{target.Name: {"read"}}, RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{target.Name: {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}}}}, FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{target.Name: {"Наименование": {Read: "hide"}}}}}}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target}})
		server := &Server{store: db, reg: reg}
		router := chi.NewRouter()
		server.Mount(router)
		for _, test := range []struct {
			id      string
			allowed bool
			status  int
		}{{good.String(), true, 200}, {other.String(), false, 200}, {hidden.String(), false, 200}, {uuid.NewString(), false, 200}, {"bad", false, 400}} {
			q := url.Values{"q": {"needle"}, "limit": {"1"}, "flt": {`{"Область":"A"}`}, "check_selected_id": {test.id}}
			request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(target.Name)+"?"+q.Encode(), nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, request.WithContext(auth.ContextWithUser(ctx, user)))
			if rec.Code != test.status {
				t.Fatalf("check %s: status=%d %s", test.id, rec.Code, rec.Body.String())
			}
			if rec.Code != 200 {
				continue
			}
			var response struct {
				Allowed bool             `json:"selected_allowed"`
				Items   []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Allowed != test.allowed {
				t.Fatalf("selected %s allowed=%v want=%v", test.id, response.Allowed, test.allowed)
			}
			if len(response.Items) > 1 || strings.Contains(rec.Body.String(), "needle secret") {
				t.Fatalf("unbounded or unmasked response: %s", rec.Body.String())
			}
		}
	})
}

// Exercise the public route and inspect raw JSON: decoding null into a Go
// slice would otherwise hide the distinction the browser relies on.
func TestRefComboboxEmptyPagePublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "ПустойВыбор", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Область", Type: metadata.FieldTypeString},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{target}); err != nil {
			t.Fatal(err)
		}
		selected := uuid.New()
		if err := db.Upsert(ctx, target.Name, selected, map[string]any{"Наименование": "needle", "Область": "A"}, target); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target}})
		server := &Server{store: db, reg: reg}
		router := chi.NewRouter()
		server.Mount(router)
		user := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{target.Name: {"read"}}}}}}
		for _, tc := range []struct {
			name, search, area string
			offset, total      int
			check, allowed     bool
		}{
			{name: "no matches", search: "absent", area: "A"},
			{name: "search does not revoke selection", search: "absent", area: "A", check: true, allowed: true},
			{name: "empty context revokes selection", area: "empty", check: true},
			{name: "offset beyond end preserves selection", area: "A", offset: 50, total: 1, check: true, allowed: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := url.Values{"q": {tc.search}, "limit": {"50"}, "offset": {fmt.Sprint(tc.offset)}, "flt": {`{"Область":"` + tc.area + `"}`}}
				if tc.check {
					q.Set("check_selected_id", selected.String())
				}
				req := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(target.Name)+"?"+q.Encode(), nil)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req.WithContext(auth.ContextWithUser(ctx, user)))
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
				}
				var got struct {
					Items           json.RawMessage `json:"items"`
					Total           int             `json:"total"`
					Limit           int             `json:"limit"`
					Offset          int             `json:"offset"`
					SelectedAllowed *bool           `json:"selected_allowed"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if string(got.Items) != "[]" || got.Total != tc.total || got.Limit != 50 || got.Offset != tc.offset {
					t.Fatalf("wrong empty page: %s", rec.Body.String())
				}
				if tc.check {
					if got.SelectedAllowed == nil || *got.SelectedAllowed != tc.allowed {
						t.Fatalf("wrong membership: %s", rec.Body.String())
					}
				} else if got.SelectedAllowed != nil {
					t.Fatalf("unexpected membership check: %s", rec.Body.String())
				}
			})
		}
	})
}

// Seed at the size of the motivating address catalog. Only test setup uses
// raw SQL; acceptance uses the public form and protected search HTTP routes.
func TestRefComboboxLargeCatalogPublicHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("2.1 million row acceptance")
	}
	f := newChoiceHTTPFixture(t)
	ctx := context.Background()
	started := time.Now()
	_, err := f.server.store.Exec(ctx, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<2100000)
 INSERT INTO "неисправность" (id,"наименование","направление_id","аудитория")
 SELECT printf('40000000-0000-0000-0000-%012d',i),'address '||i,$1,'anna' FROM n`, f.rootA.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("seed 2.1M: %s", time.Since(started))
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request.WithContext(auth.ContextWithUser(ctx, f.user)))
	if rec.Code != 200 || rec.Body.Len() > 100000 {
		t.Fatalf("form unbounded: status=%d bytes=%d", rec.Code, rec.Body.Len())
	}
	q := f.contextQuery(f.rootA)
	q.Set("q", "address")
	q.Set("limit", "50")
	started = time.Now()
	response := f.serveRefOptions(t, f.target, q)
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(page.Items) != 50 || page.Total != 2100000 || response.Body.Len() > 30000 {
		t.Fatalf("unbounded page: status=%d len=%d total=%d bytes=%d", response.Code, len(page.Items), page.Total, response.Body.Len())
	}
	t.Logf("2.1M protected search: %s; HTML=%d bytes JSON=%d bytes", time.Since(started), rec.Body.Len(), response.Body.Len())
}

// Follow the public form metadata into protected search for both entity fields
// and form attributes. Source-owner metadata must coexist with the combobox.
func TestRefComboboxRenderedSourceContextPublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceHTTPFixtureWithStore(t, db, true)
		form := f.owner.Forms[0]
		field, local := form.Elements[1], form.Elements[3]
		field.Required, local.Required = true, true
		local.ChoiceFilter = append([]metadata.FormChoiceCondition(nil), field.ChoiceFilter...)
		field.ChoiceContext = map[string]string{"Направление": "Объект.Направление"}
		local.ChoiceContext = map[string]string{"Направление": "Объект.Направление"}
		router := chi.NewRouter()
		f.server.Mount(router)
		serve := func(path string, user *auth.User) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req.WithContext(auth.ContextWithUser(req.Context(), user)))
			return rec
		}
		rec := serve("/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), f.user)
		if rec.Code != http.StatusOK {
			t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
		}
		doc, err := html.Parse(strings.NewReader(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ name, element, value string }{
			{f.target.Name, field.ID, f.legacySelected.String()},
			{"ЛокальнаяНеисправность", local.ID, ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				node := findSelectByName(doc, tc.name)
				if node == nil {
					t.Fatal("reference select missing")
				}
				for _, marker := range []string{"data-ob-ref-combobox", "data-ob-ref-required", "required"} {
					if _, ok := htmlAttribute(node, marker); !ok {
						t.Fatalf("missing %s", marker)
					}
				}
				for key, want := range map[string]string{
					"data-ob-ref-value": tc.value, "data-ref-source-entity": f.owner.Name,
					"data-ref-entity": f.target.Name, "data-ref-element": map[string]string{field.ID: field.Name, local.ID: local.Name}[tc.element],
				} {
					if got, ok := htmlAttribute(node, key); !ok || got != want {
						t.Fatalf("%s=%q want %q", key, got, want)
					}
				}
				raw, _ := htmlAttribute(node, "data-ref-context")
				var paths map[string]string
				if err := json.Unmarshal([]byte(raw), &paths); err != nil || paths["Направление"] != "Объект.Направление" {
					t.Fatalf("preview context %q: %v", raw, err)
				}
				raw, _ = htmlAttribute(node, "data-ref-choice-context")
				var choice managedChoiceContext
				if err := json.Unmarshal([]byte(raw), &choice); err != nil {
					t.Fatal(err)
				}
				if choice.FormEntity != f.owner.Name || choice.Form != form.Name || choice.Element != tc.element || choice.Sources["Объект.Направление"] != "Направление" {
					t.Fatalf("search context: %#v", choice)
				}
				// The current control value is passed through the rendered source mapping.
				for _, source := range []struct {
					id      string
					allowed bool
				}{
					{f.rootA.String(), true}, {f.rootB.String(), false},
				} {
					values, _ := json.Marshal(map[string]string{"Объект.Направление": source.id})
					q := url.Values{"q": {"needle"}, "limit": {"50"}, "form_entity": {choice.FormEntity}, "form": {choice.Form}, "element": {choice.Element}, "sources": {string(values)}, "check_selected_id": {choiceHTTPUUID(0x20, 1).String()}}
					path := "/ui/_ref-options/" + url.PathEscape(f.target.Name) + "?" + q.Encode()
					response := serve(path, f.user)
					if response.Code != http.StatusOK {
						t.Fatalf("search: %d %s", response.Code, response.Body.String())
					}
					var page struct {
						Items   []map[string]any `json:"items"`
						Allowed bool             `json:"selected_allowed"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
						t.Fatal(err)
					}
					if page.Allowed != source.allowed {
						t.Fatalf("current source %s: allowed=%v", source.id, page.Allowed)
					}
					if strings.Contains(response.Body.String(), "needle hidden") {
						t.Fatal("RLS-hidden record leaked")
					}
					if len(page.Items) == 0 {
						t.Fatal("authorized search unexpectedly empty")
					}
					denied := &auth.User{Login: "outsider", Roles: []*auth.Role{{Permissions: auth.Permission{Documents: map[string][]string{f.owner.Name: {"read"}}}}}}
					if rejected := serve(path, denied); rejected.Code != http.StatusForbidden {
						t.Fatalf("target permission: %d %s", rejected.Code, rejected.Body.String())
					}
					q.Set("form_entity", f.target.Name)
					if rejected := serve("/ui/_ref-options/"+url.PathEscape(f.target.Name)+"?"+q.Encode(), f.user); rejected.Code != http.StatusBadRequest {
						t.Fatalf("forged source: %d %s", rejected.Code, rejected.Body.String())
					}
				}
			})
		}
	})
}
