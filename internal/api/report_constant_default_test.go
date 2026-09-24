package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestAPIV2_ReportStoredDateConstant(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Срок", Type: metadata.FieldTypeDate},
		}}
		constant := &metadata.Constant{Name: "НачалоУчета", Type: metadata.FieldTypeDate, Default: "2026-05-05"}
		rep := &reportpkg.Report{Name: "Просроченные",
			Query:  "ВЫБРАТЬ Наименование ИЗ Справочник.Задача ГДЕ Срок < &НаДату",
			Params: []reportpkg.Param{{Name: "НаДату", Type: "date", Default: "{{constant:НачалоУчета|minus_days:5}}"}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateConstants(ctx, []*metadata.Constant{constant}); err != nil {
			t.Fatal(err)
		}
		for name, day := range map[string]int{"Просрочена": 29, "ПослеГраницы": 30} {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
				"Наименование": name, "Срок": time.Date(2026, 4, day, 12, 0, 0, 0, time.Local),
			}, entity); err != nil {
				t.Fatal(err)
			}
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Constants: []*metadata.Constant{constant}, Reports: []*reportpkg.Report{rep}})
		h := &handler{reg: reg, store: db}
		router := chi.NewRouter()
		h.mountV2(router)
		for _, tc := range []struct {
			name   string
			stored any
			status int
		}{
			{name: "migration default", status: http.StatusOK},
			{name: "form date", stored: "2026-05-05", status: http.StatusOK},
			{name: "serialized time", stored: time.Date(2026, 5, 5, 0, 0, 0, 0, time.Local), status: http.StatusOK},
			{name: "invalid date", stored: "не дата", status: http.StatusBadRequest},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.stored != nil {
					if err := db.SetConstant(ctx, constant.Name, tc.stored); err != nil {
						t.Fatal(err)
					}
				}
				req := httptest.NewRequest(http.MethodGet, "/api/v2/report/Просроченные", nil)
				req = req.WithContext(auth.ContextWithOpenAccess(req.Context()))
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				if response.Code != tc.status {
					t.Fatalf("HTTP %d, ожидался %d: %s", response.Code, tc.status, response.Body.String())
				}
				if tc.status != http.StatusOK {
					if !strings.Contains(response.Body.String(), constant.Name) {
						t.Fatalf("ошибка не называет константу: %s", response.Body.String())
					}
					return
				}
				var result struct {
					Data []map[string]any `json:"data"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Data) != 1 || result.Data[0]["наименование"] != "Просрочена" {
					t.Fatalf("неверная граница отчёта: %s", response.Body.String())
				}
			})
		}
	})
}
