package api

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
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Predicate metadata names the resource Количество; the SQL source exposes
// количествоостаток. Check the mounted report endpoint, including the SQLite
// page wrapper that can rename a duplicate to количествоостаток:1.
func TestAPIV2_ReportMaskVirtualStarAliasCollisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		secret := &metadata.Entity{Name: "СекретВТ", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}, {Name: "Телефон", Type: metadata.FieldTypeString}}}
		reg := &metadata.Register{Name: "СкладВТ",
			Dimensions: []metadata.Field{{Name: "Ключ", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}}
		if err := db.Migrate(ctx, []*metadata.Entity{secret}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatal(err)
		}
		const raw = "5555551234"
		if err := db.Upsert(ctx, secret.Name, uuid.New(), map[string]any{"Ключ": "one", "Телефон": raw}, secret); err != nil {
			t.Fatal(err)
		}
		period := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Пост", uuid.New(), []map[string]any{{"ВидДвижения": "Приход", "Ключ": "one", "Количество": float64(7)}}, reg, &period); err != nil {
			t.Fatal(err)
		}
		for _, join := range []struct{ name, from string }{
			{"secret_first", "Справочник.СекретВТ КАК Л ВНУТРЕННЕЕ СОЕДИНЕНИЕ РегистрНакопления.СкладВТ.Остатки() КАК П ПО Л.Ключ = П.Ключ"},
			{"virtual_first", "РегистрНакопления.СкладВТ.Остатки() КАК П ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.СекретВТ КАК Л ПО П.Ключ = Л.Ключ"},
		} {
			for _, alias := range []string{"КоличествоОстаток", "Контакт"} {
				for _, starFirst := range []bool{true, false} {
					for _, policy := range []auth.FieldPolicy{{Read: "hide"}, {Read: "mask_tail", Keep: 4}} {
						order := "star_last"
						projection := "Л.Телефон КАК " + alias + ", П.*"
						if starFirst {
							order = "star_first"
							projection = "П.*, Л.Телефон КАК " + alias
						}
						t.Run(join.name+"/"+alias+"/"+order+"/"+policy.Read, func(t *testing.T) {
							rep := &reportpkg.Report{Name: "КоллизияВТ", Query: "ВЫБРАТЬ " + projection + " ИЗ " + join.from}
							registry := runtime.NewRegistry()
							registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{secret}, Registers: []*metadata.Register{reg}, Reports: []*reportpkg.Report{rep}})
							h := &handler{reg: registry, store: db, interp: interpreter.New()}
							router := chi.NewRouter()
							h.mountV2(router)
							user := apiUser("reader", auth.Permission{
								Reports: map[string][]string{rep.Name: {"run"}}, Catalogs: map[string][]string{secret.Name: {"read"}}, Registers: map[string][]string{reg.Name: {"read"}},
								FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{secret.Name: {"Телефон": policy}}},
							})
							rec := httptest.NewRecorder()
							router.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, "/api/v2/report/"+url.PathEscape(rep.Name)+"?limit=1", nil), user))
							if strings.Contains(rec.Body.String(), raw) {
								t.Errorf("raw secret in response: %s", rec.Body.String())
							}
							if alias == "КоличествоОстаток" {
								if rec.Code != http.StatusForbidden {
									t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
								}
								return
							}
							if rec.Code != http.StatusOK {
								t.Fatalf("safe alias status %d: %s", rec.Code, rec.Body.String())
							}
							var response struct {
								Data []map[string]any `json:"data"`
								Meta restV2Meta       `json:"meta"`
							}
							if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
								t.Fatal(err)
							}
							if len(response.Data) != 1 || response.Meta.Total != 1 || response.Meta.Page != 1 || response.Meta.TotalPages != 1 || response.Meta.Limit != 1 || response.Meta.Truncated {
								t.Fatalf("page: %s", rec.Body.String())
							}
							var want any
							if policy.Read == "mask_tail" {
								want = "••••••1234"
							}
							if got, ok := response.Data[0]["контакт"]; !ok || got != want {
								t.Fatalf("contact=%#v, present=%v, want %#v", got, ok, want)
							}
							if response.Data[0]["ключ"] != "one" || fmt.Sprint(response.Data[0]["количествоостаток"]) != "7" {
								t.Fatalf("virtual star: %s", rec.Body.String())
							}
						})
					}
				}
			}
		}
	})
}
