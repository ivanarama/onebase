package ui

import (
	"context"
	"encoding/json"
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
	"github.com/ivantit66/onebase/internal/widget"
)

func TestWidgetCellFilterHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "CellRows", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Title", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString},
			{Name: "Amount", Type: metadata.FieldTypeNumber}, {Name: "Flag", Type: metadata.FieldTypeBool},
			{Name: "Day", Type: metadata.FieldTypeDate},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		for i, title := range []string{"A", "Exact%_", "prefixExact%_", "Exact%_", "Hidden"} {
			owner := "anna"
			if i == 4 {
				owner = "boris"
			}
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Title": title, "Owner": owner, "Amount": i, "Flag": i%2 == 0, "Day": time.Date(2026, 11, 13, i, 0, 0, 0, time.UTC)}, entity); err != nil {
				t.Fatal(err)
			}
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
		meta := &metadata.Widget{Name: "CellList", Type: metadata.WidgetTypeList, Limit: 1,
			Query:   "ВЫБРАТЬ Title, Amount, Flag, Day ИЗ Справочник.CellRows ГДЕ (&Owner ЕСТЬ ПУСТО ИЛИ Owner = &Owner) УПОРЯДОЧИТЬ ПО Amount",
			Filters: []metadata.WidgetFilter{{Name: "Owner", Type: "string", Param: "Owner"}}}
		reg.LoadWidgets([]*metadata.Widget{meta})
		reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{meta.Name}}}})
		s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
		s.cfg.UIEnhancements = true
		router := chi.NewRouter()
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				u := &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{entity.Name: {"read"}}, RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{entity.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{Literal: "anna"}}}}}}}}}
				next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), u)))
			})
		})
		s.Mount(router)
		defer s.CloseEventStreams()
		query := url.Values{widgetFilterKey(meta.Name, "Owner"): {"anna"}}
		request := func(path string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			return rec
		}
		partial := "/ui/_widget/" + meta.Name
		bodyOf := func(rec *httptest.ResponseRecorder) string {
			var envelope widgetPartialResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			return envelope.HTML
		}
		if rec := request(partial + "?" + query.Encode()); rec.Code != 200 || !strings.Contains(bodyOf(rec), ">A<") {
			t.Fatalf("baseline: %d %s", rec.Code, rec.Body.String())
		}
		filterJSON := func(field string, value any) string {
			token, _ := widget.CellValue(value)
			b, _ := json.Marshal(widget.CellFilter{Field: field, Value: token})
			return string(b)
		}
		query.Set(widgetCellFilterKey(meta), filterJSON("Title", "Exact%_"))
		for _, path := range []string{partial, "/ui/"} {
			rec := request(path + "?" + query.Encode())
			if rec.Code != 200 {
				t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if path == partial {
				var envelope widgetPartialResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				body = envelope.HTML
			}
			if strings.Count(body, ">Exact%_<") != 1 || strings.Contains(body, ">prefixExact%_<") {
				t.Fatalf("exact before limit: %s", body)
			}
		}
		query.Set(widgetFilterKey(meta.Name, "Owner"), "boris")
		if rec := request(partial + "?" + query.Encode()); rec.Code != 200 || strings.Contains(rec.Body.String(), ">Exact%_<") {
			t.Fatalf("filters must intersect RLS: %d %s", rec.Code, rec.Body.String())
		}
		query.Set(widgetFilterKey(meta.Name, "Owner"), "anna")
		for _, bad := range []string{filterJSON("Owner", "anna"), `{"field":"Title","value":"{}"}`, `{} {}`, `{"field":"Title","value":"null","extra":1}`} {
			query.Set(widgetCellFilterKey(meta), bad)
			if rec := request(partial + "?" + query.Encode()); rec.Code != 400 {
				t.Fatalf("bad filter %s: %d", bad, rec.Code)
			}
		}
		query.Del(widgetCellFilterKey(meta))
		if rec := request(partial + "?" + query.Encode()); rec.Code != 200 || !strings.Contains(bodyOf(rec), ">A<") {
			t.Fatalf("reset/cache: %d %s", rec.Code, rec.Body.String())
		}
	})
}
