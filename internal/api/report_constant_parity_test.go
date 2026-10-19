package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/xuri/excelize/v2"
)

// Проверяем одну сохранённую константу через HTTP, включая содержимое XLSX:
// текстовый адаптер формы не должен менять результат запроса относительно REST.
func TestReportStoredConstantParity(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "ReportItem", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		const rowValue = "ROW-PARITY-1509"
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Наименование": rowValue}, entity); err != nil {
			t.Fatal(err)
		}
		for i, tc := range []struct {
			name     string
			typ      metadata.FieldType
			def      string
			stored   any
			where    string
			explicit url.Values
			wantRows int
		}{
			{name: "number migration low", typ: metadata.FieldTypeNumber, def: "0.5", where: "&Значение > 10"},
			{name: "number migration high", typ: metadata.FieldTypeNumber, def: "12.5", where: "&Значение > 10", wantRows: 1},
			{name: "number form low", typ: metadata.FieldTypeNumber, stored: "0,5", where: "&Значение > 10"},
			{name: "number form high", typ: metadata.FieldTypeNumber, stored: "12,5", where: "&Значение > 10", wantRows: 1},
			{name: "number typed low", typ: metadata.FieldTypeNumber, stored: 0.5, where: "&Значение > 10"},
			{name: "number typed high", typ: metadata.FieldTypeNumber, stored: 12.5, where: "&Значение > 10", wantRows: 1},
			{name: "number null", typ: metadata.FieldTypeNumber, where: "&Значение ЕСТЬ NULL", wantRows: 1},
			{name: "string null", typ: metadata.FieldTypeString, where: "&Значение ЕСТЬ NULL", wantRows: 1},
			{name: "string literal nil", typ: metadata.FieldTypeString, stored: "<nil>", where: "&Значение ЕСТЬ NULL"},
			{name: "number override", typ: metadata.FieldTypeNumber, def: "0.5", where: "&Значение > 10", explicit: url.Values{"Значение": {"12.5"}}, wantRows: 1},
			{name: "number cleared", typ: metadata.FieldTypeNumber, def: "12.5", where: "&Значение ЕСТЬ NULL", explicit: url.Values{"Значение": {""}}, wantRows: 1},
			{name: "string cleared", typ: metadata.FieldTypeString, def: "задано", where: "&Значение ЕСТЬ NULL", explicit: url.Values{"Значение": {""}}, wantRows: 1},
			{name: "bool true", typ: metadata.FieldTypeBool, def: "true", where: "&Значение", wantRows: 1},
			{name: "bool false", typ: metadata.FieldTypeBool, def: "false", where: "&Значение"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				constant := &metadata.Constant{Name: fmt.Sprintf("ReportValue%d", i), Type: tc.typ, Default: tc.def}
				if err := db.MigrateConstants(ctx, []*metadata.Constant{constant}); err != nil {
					t.Fatal(err)
				}
				if tc.def == "" {
					if err := db.SetConstant(ctx, constant.Name, tc.stored); err != nil {
						t.Fatal(err)
					}
				}
				rep := &reportpkg.Report{Name: "ConstantParity",
					Query:  "ВЫБРАТЬ Наименование ИЗ Справочник.ReportItem ГДЕ " + tc.where,
					Params: []reportpkg.Param{{Name: "Значение", Type: string(tc.typ), Default: "{{constant:" + constant.Name + "}}"}},
				}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Constants: []*metadata.Constant{constant}, Reports: []*reportpkg.Report{rep}})
				frontend := ui.New(reg, db, interpreter.New(), nil, ui.Config{Lang: "ru"}, nil)
				t.Cleanup(frontend.Close)
				router := chi.NewRouter()
				frontend.Mount(router)
				h := &handler{reg: reg, store: db}
				h.mountV2(router)
				for _, route := range []struct {
					name   string
					method string
					path   string
				}{
					{"UI GET", http.MethodGet, "/ui/report/ConstantParity?__run=1&"},
					{"UI POST", http.MethodPost, "/ui/report/ConstantParity?"},
					{"Excel", http.MethodGet, "/ui/report/ConstantParity/excel?"},
					{"REST", http.MethodGet, "/api/v2/report/ConstantParity?"},
				} {
					t.Run(route.name, func(t *testing.T) {
						target, body := route.path, ""
						if route.method == http.MethodPost {
							body = tc.explicit.Encode()
						} else {
							target += tc.explicit.Encode()
						}
						req := httptest.NewRequest(route.method, target, strings.NewReader(body))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						req = req.WithContext(auth.ContextWithOpenAccess(req.Context()))
						response := httptest.NewRecorder()
						router.ServeHTTP(response, req)
						if response.Code != http.StatusOK {
							t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
						}
						var gotRows int
						switch route.name {
						case "REST":
							var result struct {
								Data []map[string]any `json:"data"`
							}
							if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
								t.Fatal(err)
							}
							gotRows = len(result.Data)
						case "Excel":
							book, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								if err := book.Close(); err != nil {
									t.Error(err)
								}
							})
							rows, err := book.GetRows(book.GetSheetList()[0])
							if err != nil {
								t.Fatal(err)
							}
							for _, row := range rows {
								if len(row) == 1 && row[0] == rowValue {
									gotRows++
								}
							}
						default:
							page := response.Body.String()
							if strings.Contains(page, `<div class="error">`) {
								t.Fatalf("ошибка отчёта: %s", page)
							}
							gotRows = strings.Count(page, "<td>"+rowValue+"</td>")
						}
						if gotRows != tc.wantRows {
							t.Fatalf("получено строк %d, ожидалось %d", gotRows, tc.wantRows)
						}
					})
				}
			})
		}
	})
}
