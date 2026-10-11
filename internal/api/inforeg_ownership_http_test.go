package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoOwnershipRESTConflict(t *testing.T) {
	for _, version := range []string{"v1", "v2"} {
		for _, operation := range []string{"create", "update", "post"} {
			t.Run(version+"/"+operation, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ctx := context.Background()
					doc := &metadata.Entity{Name: "OwnershipDoc", Kind: metadata.KindDocument, Posting: true,
						Fields:     []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}, {Name: "Value", Type: metadata.FieldTypeNumber}},
						TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}},
					}
					ir := &metadata.InfoRegister{Name: "OwnershipInfo", Recorder: true,
						Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}}, Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeNumber}}}
					if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
						t.Fatal(err)
					}
					if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
						t.Fatal(err)
					}
					// OnWrite covers create/update without posting; OnPost covers /post.
					prog := mustParseProgram(t, `Процедура OnWrite()
       Дв = Движения.OwnershipInfo.Добавить(); Дв.Key = this.Key; Дв.Value = this.Value;
      КонецПроцедуры
      Процедура OnPost()
       Дв = Движения.OwnershipInfo.Добавить(); Дв.Key = this.Key; Дв.Value = this.Value;
      КонецПроцедуры`)
					reg := runtime.NewRegistry()
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}, InfoRegs: []*metadata.InfoRegister{ir}, Programs: map[string]*ast.Program{doc.Name: prog}})
					interp := interpreter.New()
					interp.LookupProc = reg.GetModuleProc
					svc := &entityservice.Service{Store: db, Reg: reg, Interp: interp, BuildVars: func(c context.Context, mc *runtime.MovementsCollector, _ *[]string) (map[string]any, *interpreter.TxState) {
						return dslvars.Common{Ctx: c, Reg: reg, Store: db, Movements: mc}.Build(), nil
					}}
					h := &handler{reg: reg, store: db, interp: interp, entitySvc: svc}
					router := chi.NewRouter()
					h.mountV2(router)
					router.Post("/api/documents/{entity}", h.createObject(metadata.KindDocument))
					router.Put("/api/documents/{entity}/{id}", h.updateObject(metadata.KindDocument))
					router.Post("/api/documents/{entity}/{id}/post", h.postDocument())
					base := "/api/documents/" + doc.Name
					if version == "v2" {
						base = "/api/v2/document/" + doc.Name
					}
					request := func(method, target, body string) *httptest.ResponseRecorder {
						req := httptest.NewRequest(method, target, strings.NewReader(body))
						req.Header.Set("Content-Type", "application/json")
						if method != http.MethodPost || strings.HasSuffix(target, "/post") {
							req.Header.Set("If-Match", `"1"`)
						}
						req = req.WithContext(auth.ContextWithOpenAccess(req.Context()))
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						return rec
					}
					owner := uuid.New()
					result, err := svc.Save(ctx, entityservice.SaveRequest{Entity: doc, ID: owner, IsNew: true, Action: "post", Fields: map[string]any{"Key": "occupied", "Value": float64(10)}})
					if err != nil || result.DSLError != "" {
						t.Fatalf("seed owner: %v %s", err, result.DSLError)
					}
					second := uuid.New()
					method, target := http.MethodPost, base
					if operation != "create" {
						// Preserve a document and its own movement to prove the failed change rolls back both.
						result, err = svc.Save(ctx, entityservice.SaveRequest{Entity: doc, ID: second, IsNew: true, Fields: map[string]any{"Key": "free", "Value": float64(5)}, TablePartRows: map[string][]map[string]any{"Lines": {{"Text": "before"}}}})
						if err != nil || result.DSLError != "" {
							t.Fatalf("seed second: %v %s", err, result.DSLError)
						}
						method, target = http.MethodPut, base+"/"+second.String()
						if operation == "post" {
							method, target = http.MethodPost, target+"/post"
						}
					}
					rec := request(method, target, `{"Key":"occupied","Value":20,"__tableparts":{"Lines":[{"Text":"attempt"}]}}`)
					if rec.Code != http.StatusConflict {
						t.Fatalf("status=%d, want 409: %s", rec.Code, rec.Body.String())
					}
					var response errorResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					for _, part := range []string{ir.Name, "occupied", doc.Name, owner.String()} {
						if !strings.Contains(response.Error, part) {
							t.Errorf("message missing %q: %s", part, response.Error)
						}
					}
					var value, recorder string
					if err := db.QueryRow(ctx, "SELECT CAST(value AS TEXT), CAST(recorder AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)+" WHERE key = 'occupied'").Scan(&value, &recorder); err != nil {
						t.Fatal(err)
					}
					if value != "10" || recorder != owner.String() {
						t.Fatalf("owner changed: %s %s", value, recorder)
					}
					docs, err := db.List(ctx, doc.Name, doc, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					want := 1
					if operation != "create" {
						want = 2
					}
					if len(docs) != want {
						t.Fatalf("document rollback: %d, want %d", len(docs), want)
					}
					if operation != "create" {
						row, err := db.GetByID(ctx, doc.Name, second, doc)
						if err != nil {
							t.Fatal(err)
						}
						if row["Key"] != "free" || fmt.Sprint(row["_version"]) != "1" || row["posted"] == true {
							t.Fatalf("second changed: %#v", row)
						}
						rows, err := db.GetTablePartRows(ctx, doc.Name, "Lines", second, doc.TableParts[0])
						if err != nil {
							t.Fatal(err)
						}
						if len(rows) != 1 || rows[0]["Text"] != "before" {
							t.Fatalf("table part changed: %#v", rows)
						}
						var own string
						if err := db.QueryRow(ctx, "SELECT CAST(value AS TEXT) FROM "+metadata.InfoRegTableName(ir.Name)+" WHERE key = 'free'").Scan(&own); err != nil || own != "5" {
							t.Fatalf("second movement lost: %q %v", own, err)
						}
					}
					// A real database failure must remain 500, not become a user conflict.
					if _, err := db.Exec(ctx, "DROP TABLE "+metadata.InfoRegTableName(ir.Name)); err != nil {
						t.Fatal(err)
					}
					rec = request(method, target, `{"Key":"occupied","Value":20}`)
					if rec.Code != http.StatusInternalServerError {
						t.Fatalf("database failure status=%d: %s", rec.Code, rec.Body.String())
					}
				})
			})
		}
	}
}
