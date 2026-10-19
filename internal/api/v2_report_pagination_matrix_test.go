package api

import (
	"context"
	"encoding/json"
	"fmt"
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

// SQL pagination is a dialect boundary: the same mounted HTTP route must keep
// report parameters, row access, field masks and an author-supplied LIMIT on both engines.
func TestAPIV2_ReportPaginationMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name: "ТоварПагинации",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Владелец", Type: metadata.FieldTypeString},
				{Name: "Телефон", Type: metadata.FieldTypeString},
				{Name: "Число", Type: metadata.FieldTypeNumber},
				{Name: "Флаг", Type: metadata.FieldTypeBool},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, row := range []map[string]any{
			{"Наименование": "A1", "Владелец": "runner"},
			{"Наименование": "A2", "Владелец": "runner"},
			{"Наименование": "A2-hidden", "Владелец": "other"},
			{"Наименование": "A3", "Владелец": "runner"},
			{"Наименование": "A4", "Владелец": "runner"},
		} {
			row["Телефон"] = "1234567890"
			row["Число"] = 42
			row["Флаг"] = true
			if err := db.Upsert(ctx, entity.Name, uuid.New(), row, entity); err != nil {
				t.Fatalf("запись: %v", err)
			}
		}

		report := &reportpkg.Report{
			Name:   "СтраницаТоваров",
			Params: []reportpkg.Param{{Name: "Начало", Type: "string"}},
			Query: `ВЫБРАТЬ ПЕРВЫЕ 3 Наименование, Телефон, Число КАК СкрытоеЧисло, Флаг КАК СкрытыйФлаг
ИЗ Справочник.ТоварПагинации
ГДЕ Наименование >= &Начало
УПОРЯДОЧИТЬ ПО Наименование`,
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Reports: []*reportpkg.Report{report}})
		h := &handler{reg: registry, store: db, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)

		user := apiUser("runner", auth.Permission{
			Reports:  map[string][]string{report.Name: {"run"}},
			Catalogs: map[string][]string{entity.Name: {"read"}},
			FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
				entity.Name: {"Телефон": {Read: "mask_tail", Keep: 4}, "Число": {Read: "hide"}, "Флаг": {Read: "hide"}},
			}},
			RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
				entity.Name: {"read": {Field: "Владелец", Op: "eq", Value: auth.RowValue{User: "login"}}},
			}},
		})
		for _, c := range []struct {
			page      string
			want      []string
			truncated bool
		}{
			{"1", []string{"A1", "A2"}, true},
			{"2", []string{"A3"}, false},
			{"3", nil, false},
		} {
			t.Run("page="+c.page, func(t *testing.T) {
				target := "/api/v2/report/" + url.PathEscape(report.Name) + "?Начало=A1&limit=2&page=" + c.page
				req := withUser(httptest.NewRequest(http.MethodGet, target, nil), user)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				var response struct {
					Data []map[string]any `json:"data"`
					Meta restV2Meta       `json:"meta"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Data) != len(c.want) {
					t.Fatalf("page data=%#v, want %v", response.Data, c.want)
				}
				for i, row := range response.Data {
					if row["наименование"] != c.want[i] || row["телефон"] != "••••••7890" {
						t.Fatalf("page row=%#v, want %s with masked phone", row, c.want[i])
					}
					for _, key := range []string{"скрытоечисло", "скрытыйфлаг"} {
						if value, present := row[key]; !present || value != nil {
							t.Fatalf("hidden typed column %q = %#v, present=%v; want null", key, value, present)
						}
					}
				}
				if response.Meta.Total != 3 || response.Meta.Limit != 2 || response.Meta.TotalPages != 2 || response.Meta.Truncated != c.truncated {
					t.Fatalf("page meta=%+v", response.Meta)
				}
				if fmt.Sprint(response.Meta.Page) != c.page {
					t.Fatalf("page meta=%+v, want page %s", response.Meta, c.page)
				}
			})
		}
	})
}
