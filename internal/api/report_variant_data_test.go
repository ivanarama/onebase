package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportVariantDataMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "VariantRows", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Category", Type: metadata.FieldTypeString},
			{Name: "Repeat", Type: metadata.FieldTypeNumber}, {Name: "Flag", Type: metadata.FieldTypeBool},
			{Name: "Owner", Type: metadata.FieldTypeString},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []map[string]any{
			{"Наименование": "A-excluded", "Category": "free", "Repeat": 10, "Flag": true, "Owner": "runner"},
			{"Наименование": "Z-wanted", "Category": "paid", "Repeat": 0, "Flag": false, "Owner": "runner"},
			{"Наименование": "hidden", "Category": "paid", "Repeat": 0, "Flag": false, "Owner": "other"},
		} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
				t.Fatal(err)
			}
		}
		rep, err := reportpkg.ParseBytes([]byte(`name: Rows
query: SELECT Наименование, Category, Repeat, Flag FROM Catalog.VariantRows ORDER BY Наименование
composition:
  groupings: [Наименование]
variants:
  - name: legacy
    composition:
      groupings: [Наименование]
  - name: filtered
    filters:
      - {field: Category, op: eq, value: paid}
      - {field: Repeat, op: eq, value: 0}
      - {field: Flag, op: eq, value: false}
    composition:
      groupings: [Наименование]
  - name: custom
    query: SELECT Наименование, Flag FROM Catalog.VariantRows WHERE Category = 'paid'
    filters:
      - {field: Flag, op: eq, value: false}
  - name: invalid
    filters:
      - {field: Flag, op: bogus, value: false}
`))
		if err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}, Reports: []*reportpkg.Report{rep}})
		h := &handler{reg: reg, store: db, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		permission := auth.Permission{Reports: map[string][]string{"Rows": {"run"}}, Catalogs: map[string][]string{ent.Name: {"read"}}, RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{ent.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}}}}}
		for _, tc := range []struct {
			variant, want string
			total         int
		}{{"filtered", "Z-wanted", 1}, {"custom", "Z-wanted", 1}, {"legacy", "A-excluded", 2}, {"", "A-excluded", 2}} {
			for _, selector := range []string{"variant", "__variant"} {
				q := url.Values{selector: {tc.variant}, "limit": {"1"}}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, withUser(httptest.NewRequest("GET", "/api/v2/report/Rows?"+q.Encode(), nil), apiUser("runner", permission)))
				if rec.Code != http.StatusOK {
					t.Fatalf("%s %s: %d %s", selector, tc.variant, rec.Code, rec.Body.String())
				}
				var got struct {
					Data []map[string]any `json:"data"`
					Meta restV2Meta       `json:"meta"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Data) != 1 || got.Data[0]["наименование"] != tc.want || got.Meta.Total != tc.total {
					t.Fatalf("%s: data=%v meta=%+v", tc.variant, got.Data, got.Meta)
				}
			}
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, withUser(httptest.NewRequest("GET", "/api/v2/report/Rows?variant=invalid", nil), apiUser("runner", permission)))
		if rec.Code != 400 {
			t.Fatalf("invalid filter: %d %s", rec.Code, rec.Body.String())
		}
		permission.FieldAccess = auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{ent.Name: {"Flag": {Read: "hide"}}}}
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, withUser(httptest.NewRequest("GET", "/api/v2/report/Rows?variant=filtered", nil), apiUser("runner", permission)))
		if rec.Code != 403 {
			t.Fatalf("masked predicate must be denied: %d %s", rec.Code, rec.Body.String())
		}
	})
}
