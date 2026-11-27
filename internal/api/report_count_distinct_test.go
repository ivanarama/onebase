package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReportCountDistinctRESTMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		rep := &report.Report{Name: "Distinct", Query: "SELECT 'a' AS G, 'x' AS ID UNION ALL SELECT 'a' AS G, 'x' AS ID UNION ALL SELECT 'a' AS G, 'y' AS ID UNION ALL SELECT 'b' AS G, 'x' AS ID", Composition: &report.Composition{Groupings: []string{"G"}, Measures: []report.Measure{{Field: "ID", Agg: "count_distinct"}}, Totals: report.Totals{Grand: true}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Reports: []*report.Report{rep}})
		h := &handler{store: db, reg: reg, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		w := httptest.NewRecorder()
		permission := auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}}
		router.ServeHTTP(w, withUser(httptest.NewRequest("GET", "/api/v2/report/Distinct?composition=1", nil), apiUser("report-reader", permission)))
		if w.Code != 200 {
			t.Fatalf("REST: %d %s", w.Code, w.Body.String())
		}
		var got struct {
			Data struct {
				Result compose.Result `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Data.Result.Grand["ID"] != float64(2) || len(got.Data.Result.Groups) != 2 || got.Data.Result.Groups[0].Subtotals["ID"] != float64(2) {
			t.Fatalf("distinct REST: %s", w.Body.String())
		}
	})
}

func TestReportCountDistinctDatesRESTMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name: "СобытиеОтчета",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Группа", Type: metadata.FieldTypeString},
				{Name: "Момент", Type: metadata.FieldTypeDate},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct {
			group string
			date  any
		}{
			{"a", "2026-09-01T23:30:00-04:00"},
			{"a", "2026-09-02T03:30:00Z"},
			{"a", "2026-09-02T03:30:00.000001Z"},
			{"b", "2026-09-02T06:30:00+03:00"},
			{"b", nil},
		} {
			if err := db.Upsert(ctx, entity.Name, uuid.New(),
				map[string]any{"Наименование": "Проверка", "Группа": row.group, "Момент": row.date}, entity); err != nil {
				t.Fatal(err)
			}
		}
		rep := &report.Report{
			Name:  "DistinctDates",
			Query: "ВЫБРАТЬ Группа, Момент ИЗ Справочник.СобытиеОтчета УПОРЯДОЧИТЬ ПО Группа",
			Composition: &report.Composition{
				Groupings: []string{"Группа"},
				Measures:  []report.Measure{{Field: "Момент", Agg: "count_distinct"}},
				Totals:    report.Totals{Grand: true},
			},
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Reports: []*report.Report{rep}})
		h := &handler{store: db, reg: reg, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		w := httptest.NewRecorder()
		permission := auth.Permission{
			Catalogs: map[string][]string{entity.Name: {"read"}},
			Reports:  map[string][]string{rep.Name: {"run"}},
		}
		request := httptest.NewRequest("GET", "/api/v2/report/DistinctDates?composition=1", nil)
		router.ServeHTTP(w, withUser(request, apiUser("report-reader", permission)))
		if w.Code != 200 {
			t.Fatalf("REST: %d %s", w.Code, w.Body.String())
		}
		var got struct {
			Data struct {
				Result compose.Result `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		result := got.Data.Result
		if result.Grand["Момент"] != float64(2) || len(result.Groups) != 2 ||
			result.Groups[0].Subtotals["Момент"] != float64(2) || result.Groups[1].Subtotals["Момент"] != float64(1) {
			t.Fatalf("distinct dates: %s", w.Body.String())
		}
	})
}
