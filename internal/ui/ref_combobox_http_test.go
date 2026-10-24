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
