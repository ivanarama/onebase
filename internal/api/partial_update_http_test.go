package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Public REST routes, not Save DTOs: omitted data must reach object hooks as
// persisted state, while presence (including explicit zero values) still wins.
func TestRESTPartialUpdateHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ref := &metadata.Entity{Name: "PatchReference", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		entities := []*metadata.Entity{ref}
		programs := map[string]*ast.Program{}
		for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
			entity := &metadata.Entity{Name: "Patch" + string(kind), Kind: kind, Fields: []metadata.Field{
				{Name: "Note", Type: metadata.FieldTypeString},
				{Name: "Done", Type: metadata.FieldTypeBool},
				{Name: "Flag", Type: metadata.FieldTypeBool},
				{Name: "Count", Type: metadata.FieldTypeNumber},
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Type", Type: metadata.FieldTypeString},
				{Name: "Secret", Type: metadata.FieldTypeString},
				{Name: "Target", Type: "reference:PatchReference", RefEntity: ref.Name},
				{Name: "Date", Type: metadata.FieldTypeDate},
			}, TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}}}
			entities = append(entities, entity)
			programs[entity.Name] = mustParseProgram(t, `Процедура OnWrite()
    Если ServerAssignedField("Done") ИЛИ ПолеНазначеноСервером("Note") Тогда
        ВызватьИсключение("REST must not mint server assignment proof");
    КонецЕсли;
    Если this.Type = "guard" И this.Done = Истина Тогда
        ВызватьИсключение("pending verification");
    КонецЕсли;
    Если НЕ this.Done = Истина Тогда
        this.Done = Ложь;
    КонецЕсли;
КонецПроцедуры`)
		}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		refID := uuid.New()
		if err := db.Upsert(ctx, ref.Name, refID, map[string]any{"Name": "reference"}, ref); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities, Programs: programs})
		interp := interpreter.New()
		svc := &entityservice.Service{Store: db, Reg: reg, Interp: interp}
		h := &handler{reg: reg, store: db, interp: interp, entitySvc: svc}
		router := chi.NewRouter()
		router.Put("/catalogs/{entity}/{id}", h.updateObject(metadata.KindCatalog))
		router.Put("/documents/{entity}/{id}", h.updateObject(metadata.KindDocument))
		h.mountV2(router)
		for _, entity := range entities[1:] {
			for _, version := range []string{"v1", "v1-cas", "v2"} {
				t.Run(string(entity.Kind)+"/"+version, func(t *testing.T) {
					perm := auth.Permission{}
					policies := auth.RowPolicies{"write": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}}
					mask := auth.FieldPolicies{"Secret": {Read: "mask_all"}}
					if entity.Kind == metadata.KindCatalog {
						perm.Catalogs = map[string][]string{entity.Name: {"read", "write"}}
						perm.RowAccess.Catalogs = map[string]auth.RowPolicies{entity.Name: policies}
						perm.FieldAccess.Catalogs = map[string]auth.FieldPolicies{entity.Name: mask}
					} else {
						perm.Documents = map[string][]string{entity.Name: {"read", "write"}}
						perm.RowAccess.Documents = map[string]auth.RowPolicies{entity.Name: policies}
						perm.FieldAccess.Documents = map[string]auth.FieldPolicies{entity.Name: mask}
					}
					user := apiUser("owner", perm)
					seed := func(owner, typ string, done bool) uuid.UUID {
						id := uuid.New()
						fields := map[string]any{"Note": "old", "Done": done, "Flag": true, "Count": 7, "Owner": owner, "Type": typ, "Secret": "real-secret", "Target": refID.String(), "Date": time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}
						if err := db.Upsert(ctx, entity.Name, id, fields, entity); err != nil {
							t.Fatal(err)
						}
						if err := db.UpsertTablePartRows(ctx, entity.Name, "Lines", id, []map[string]any{{"Text": "keep"}}, entity.TableParts[0]); err != nil {
							t.Fatal(err)
						}
						return id
					}
					put := func(id uuid.UUID, patch map[string]any, actor *auth.User, token string) *httptest.ResponseRecorder {
						body, err := json.Marshal(patch)
						if err != nil {
							panic(err)
						}
						prefix := "/" + string(entity.Kind) + "s/"
						if version == "v2" {
							prefix = "/api/v2/" + string(entity.Kind) + "/"
						}
						req := httptest.NewRequest(http.MethodPut, prefix+url.PathEscape(entity.Name)+"/"+id.String(), bytes.NewReader(body))
						req.Header.Set("Content-Type", "application/json")
						if token != "" {
							req.Header.Set("If-Match", token)
						}
						req = req.WithContext(auth.ContextWithUser(req.Context(), actor))
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						return rec
					}
					token := "1"
					if version == "v1" {
						token = ""
					}
					for _, tc := range []struct {
						name       string
						patch      map[string]any
						owner, typ string
						code       int
					}{
						{"omitted-header-and-tableparts", map[string]any{"Note": "new"}, "owner", "ordinary", 200},
						{"explicit-false-zero-empty", map[string]any{"Done": false, "Flag": false, "Count": 0, "Note": ""}, "owner", "ordinary", 200},
						{"explicit-null", map[string]any{"Flag": nil, "Count": nil, "Note": nil, "Target": nil, "Date": nil}, "owner", "ordinary", 200},
						{"masked-crafted-given", map[string]any{"Note": "new", "Secret": "crafted"}, "owner", "ordinary", 200},
						{"masked-absent", map[string]any{"Note": "new"}, "owner", "ordinary", 200},
						{"masked-explicit-null-preserved", map[string]any{"Secret": nil}, "owner", "ordinary", 200},
						{"explicit-empty-tablepart", map[string]any{"__tableparts": map[string]any{"Lines": []any{}}}, "owner", "ordinary", 200},
						{"foreign-row-denied", map[string]any{"Note": "new"}, "other", "ordinary", 403},
						{"given-owner-change-denied", map[string]any{"Owner": "other"}, "owner", "ordinary", 403},
						{"object-guard-sees-omitted-type", map[string]any{"Done": true}, "owner", "guard", 422},
						{"unknown-key-denied", map[string]any{"Unknown": 1}, "owner", "ordinary", 400},
					} {
						t.Run(tc.name, func(t *testing.T) {
							id := seed(tc.owner, tc.typ, tc.typ != "guard")
							before, err := db.GetByID(ctx, entity.Name, id, entity)
							if err != nil {
								t.Fatal(err)
							}
							rec := put(id, tc.patch, user, token)
							if rec.Code != tc.code {
								t.Fatalf("HTTP %d want %d: %s", rec.Code, tc.code, rec.Body.String())
							}
							after, err := db.GetByID(ctx, entity.Name, id, entity)
							if err != nil {
								t.Fatal(err)
							}
							for _, field := range entity.Fields {
								want := before[field.Name]
								if value, given := tc.patch[field.Name]; given && rec.Code == 200 && field.Name != "Secret" {
									want = value
								}
								actual := after[field.Name]
								if field.Type == metadata.FieldTypeBool && want != nil {
									actual = fmt.Sprint(actual) == "true" || fmt.Sprint(actual) == "1"
									want = fmt.Sprint(want) == "true" || fmt.Sprint(want) == "1"
								}
								if fmt.Sprint(actual) != fmt.Sprint(want) {
									t.Errorf("%s=%v want %v", field.Name, actual, want)
								}
							}
							wantVersion := int64(1)
							if rec.Code == 200 {
								wantVersion++
							}
							if after["_version"] != wantVersion {
								t.Fatalf("version=%v want %d", after["_version"], wantVersion)
							}
							rows, err := db.GetTablePartRows(ctx, entity.Name, "Lines", id, entity.TableParts[0])
							_, clearTP := tc.patch["__tableparts"]
							if err != nil || (clearTP && len(rows) != 0) || (!clearTP && (len(rows) != 1 || rows[0]["Text"] != "keep")) {
								t.Fatalf("tableparts=%v, %v", rows, err)
							}
						})
					}
					t.Run("rbac-denied", func(t *testing.T) {
						id := seed("owner", "ordinary", true)
						rec := put(id, map[string]any{"Note": "new"}, apiUser("owner", auth.Permission{}), token)
						if rec.Code != 403 {
							t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
						}
					})
					t.Run("cas-race", func(t *testing.T) {
						id := seed("owner", "ordinary", true)
						var wg sync.WaitGroup
						start := make(chan struct{})
						replies := make(chan *httptest.ResponseRecorder, 2)
						for _, note := range []string{"one", "two"} {
							wg.Add(1)
							go func(note string) {
								defer wg.Done()
								<-start
								replies <- put(id, map[string]any{"Note": note}, user, "1")
							}(note)
						}
						close(start)
						wg.Wait()
						close(replies)
						codes := map[int]int{}
						for rec := range replies {
							codes[rec.Code]++
							if rec.Code != 200 && rec.Code != 409 {
								t.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
							}
						}
						if codes[200] != 1 || codes[409] != 1 {
							t.Fatalf("race results %v", codes)
						}
						row, err := db.GetByID(ctx, entity.Name, id, entity)
						if err != nil || row["_version"] != int64(2) || !(fmt.Sprint(row["Done"]) == "true" || fmt.Sprint(row["Done"]) == "1") {
							t.Fatalf("lost update: %v, %v", row, err)
						}
					})
					t.Run("auto-cas-before-storage", func(t *testing.T) {
						id := seed("owner", "ordinary", true)
						// Mutate the same row/version after hydration, before final CAS.
						// The transaction must roll back this hook-side write as well.
						svc.PrepareHook = func(c context.Context, e *metadata.Entity, obj *runtime.Object) {
							if err := db.Upsert(c, e.Name, obj.ID, map[string]any{"Count": 99}, e); err != nil {
								t.Fatal(err)
							}
						}
						defer func() { svc.PrepareHook = nil }()
						before, _ := db.GetByID(ctx, entity.Name, id, entity)
						rec := put(id, map[string]any{"Note": "new"}, user, token)
						if rec.Code != 409 {
							t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
						}
						after, _ := db.GetByID(ctx, entity.Name, id, entity)
						if !reflect.DeepEqual(before, after) {
							t.Fatalf("hook/CAS rollback changed state: %v -> %v", before, after)
						}
					})
				})
			}
		}
	})
}
