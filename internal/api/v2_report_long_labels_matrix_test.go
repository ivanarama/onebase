package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
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
)

// Exercise the mounted report route: pagination must restore logical labels
// before field policies and typed-column normalization, including empty pages.
func TestAPIV2_ReportLongLabelsPaginationMatrix(t *testing.T) {
	const longField = "ДлинныйСтроковыйРеквизитДляПроверкиСтраницыОтчета"
	const longAlias = "ДлинныйВыходнойПсевдонимДляПроверкиСтраницыОтчета"
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "СтрокаОтчета", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Владелец", Type: metadata.FieldTypeString},
			{Name: "Краткое", Type: metadata.FieldTypeString},
			{Name: longField, Type: metadata.FieldTypeString},
		}}
		entities := []*metadata.Entity{entity}
		if err := metadata.Validate(entities, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		// The middle row must be excluded before counting and pagination by RLS.
		for i, name := range []string{"A1", "A2-hidden", "A3"} {
			owner := "reader"
			if i == 1 {
				owner = "other"
			}
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
				"Наименование": name, "Владелец": owner,
				"Краткое": "short-1234", longField: "sample-5678",
			}, entity); err != nil {
				t.Fatal(err)
			}
		}
		for _, projection := range []struct {
			name, sql       string
			wildcard, alias bool
		}{
			{"star", "*", true, false},
			{"qualified-star", "С.*", true, false},
			{"explicit", "С.Наименование, С.Краткое, С." + longField, false, false},
			{"alias", "С.Наименование, С.Краткое, С." + longField + " КАК " + longAlias, false, true},
		} {
			t.Run(projection.name, func(t *testing.T) {
				rep := &reportpkg.Report{Name: "МеткиСтраницы", Query: "ВЫБРАТЬ " + projection.sql + " ИЗ Справочник.СтрокаОтчета КАК С УПОРЯДОЧИТЬ ПО С.Наименование"}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: entities, Reports: []*reportpkg.Report{rep}})
				h := &handler{reg: reg, store: db, interp: interpreter.New()}
				router := chi.NewRouter()
				h.mountV2(router)
				for _, policy := range []string{"full", "hide", "mask_tail"} {
					t.Run(policy, func(t *testing.T) {
						user := apiUser("reader", auth.Permission{
							Reports:  map[string][]string{rep.Name: {"run"}},
							Catalogs: map[string][]string{entity.Name: {"read"}},
							FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{entity.Name: {
								"Краткое": {Read: policy, Keep: 4}, longField: {Read: policy, Keep: 4},
							}}},
							RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{entity.Name: {
								"read": {Field: "Владелец", Op: "eq", Value: auth.RowValue{User: "login"}},
							}}},
						})
						longKey := strings.ToLower(longField)
						if projection.alias {
							longKey = strings.ToLower(longAlias)
						}
						wantKeys := []string{"наименование", "краткое", longKey}
						if projection.wildcard {
							wantKeys = append(wantKeys, "id", "_version", "deletion_mark", "владелец")
						}
						sort.Strings(wantKeys)
						for page, name := range []string{"A1", "A3", ""} {
							t.Run(fmt.Sprintf("page=%d", page+1), func(t *testing.T) {
								rec := httptest.NewRecorder()
								target := "/api/v2/report/" + url.PathEscape(rep.Name) + fmt.Sprintf("?limit=1&page=%d", page+1)
								router.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), user))
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
								if response.Meta.Total != 2 || response.Meta.TotalPages != 2 || response.Meta.Page != page+1 || response.Meta.Limit != 1 || response.Meta.Truncated != (page == 0) {
									t.Fatalf("meta=%+v", response.Meta)
								}
								if policy != "full" && (strings.Contains(rec.Body.String(), "short-1234") || strings.Contains(rec.Body.String(), "sample-5678")) {
									t.Fatal("response contains an unmasked sample value")
								}
								columns := append([]string(nil), response.Meta.Columns...)
								sort.Strings(columns)
								if !reflect.DeepEqual(columns, wantKeys) {
									t.Fatalf("columns=%v, want %v", columns, wantKeys)
								}
								if name == "" {
									if len(response.Data) != 0 {
										t.Fatalf("empty page=%v", response.Data)
									}
									return
								}
								if len(response.Data) != 1 || response.Data[0]["наименование"] != name {
									t.Fatalf("page=%v, want %s", response.Data, name)
								}
								row := response.Data[0]
								keys := make([]string, 0, len(row))
								for key := range row {
									keys = append(keys, key)
								}
								sort.Strings(keys)
								if !reflect.DeepEqual(keys, wantKeys) {
									t.Fatalf("keys=%v, want %v", keys, wantKeys)
								}
								shortWant, longWant := any("short-1234"), any("sample-5678")
								if policy == "hide" {
									shortWant, longWant = nil, nil
								}
								if policy == "mask_tail" {
									shortWant, longWant = "••••••1234", "•••••••5678"
								}
								if row["краткое"] != shortWant || row[longKey] != longWant {
									t.Fatalf("masked values=%v / %v, want %v / %v", row["краткое"], row[longKey], shortWant, longWant)
								}
							})
						}
					})
				}
			})
		}
	})
}
