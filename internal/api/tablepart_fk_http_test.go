package api

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestTablePartFKHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{Name: "ЦельТЧ", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		entity := &metadata.Entity{Name: "ЗаписьТЧ", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Ссылка", Type: "reference:ЦельТЧ", RefEntity: target.Name}}}}}
		entities := []*metadata.Entity{target, entity}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		known, parent, missing := uuid.New(), uuid.New(), uuid.New()
		if err := db.Upsert(ctx, target.Name, known, map[string]any{"Наименование": "Цель"}, target); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, entity.Name, parent, map[string]any{"Наименование": "Исходное"}, entity); err != nil {
			t.Fatal(err)
		}
		tp := entity.TableParts[0]

		// Default migrations constrain only parent_id. Model an existing schema
		// with real reference constraints without changing the migration contract.
		if _, err := db.Exec(ctx, "DROP TABLE "+metadata.TablePartTableName(entity.Name, tp.Name)); err != nil {
			t.Fatal(err)
		}
		ddl := strings.TrimSuffix(storage.CreateTablePartSQL(db.Dialect(), entity, tp), "\n)")
		for _, f := range tp.Fields {
			ddl += ", FOREIGN KEY (" + metadata.ColumnName(f) + ") REFERENCES " + metadata.TableName(f.RefEntity) + "(id)"
		}
		if _, err := db.Exec(ctx, ddl+"\n)"); err != nil {
			t.Fatal(err)
		}

		if err := db.UpsertTablePartRows(ctx, entity.Name, tp.Name, parent, []map[string]any{{"Ссылка": known.String()}}, tp); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities})
		interp := interpreter.New()
		svc := &entityservice.Service{Store: db, Reg: reg, Interp: interp}
		h := &handler{reg: reg, store: db, interp: interp, entitySvc: svc}
		router := chi.NewRouter()
		router.Post("/catalogs/{entity}", h.createObject(metadata.KindCatalog))
		router.Put("/catalogs/{entity}/{id}", h.updateObject(metadata.KindCatalog))
		h.mountV2(router)
		for _, version := range []string{"/catalogs", "/api/v2/catalog"} {
			for _, create := range []bool{true, false} {
				name := version + "/update"
				if create {
					name = version + "/create"
				}
				t.Run(name, func(t *testing.T) {
					payload := map[string]any{"Наименование": "Новое", "__tableparts": map[string]any{"Строки": []map[string]any{{"Ссылка": known.String()}, {"Ссылка": missing.String()}}}}
					body, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					method, path := http.MethodPost, version+"/"+url.PathEscape(entity.Name)
					if !create {
						method = http.MethodPut
						path += "/" + parent.String()
					}
					req := httptest.NewRequest(method, path, bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					if !create {
						req.Header.Set("If-Match", "1")
					}
					req = req.WithContext(auth.ContextWithOpenAccess(req.Context()))
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					if rec.Code != http.StatusUnprocessableEntity {
						t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
					}
					var response errorResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					// Exact public message excludes driver text, UUIDs and schema/constraint names.
					if response.Error != "ссылка на несуществующий объект" || response.File != "" || response.Line != 0 {
						t.Fatalf("unsafe response: %#v", response)
					}
					got, err := db.GetByID(ctx, entity.Name, parent, entity)
					if err != nil {
						t.Fatal(err)
					}
					if got["Наименование"] != "Исходное" {
						t.Errorf("header not rolled back: %#v", got)
					}
					rows, err := db.GetTablePartRows(ctx, entity.Name, tp.Name, parent, tp)
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) != 1 || rows[0]["Ссылка"] != known.String() {
						t.Errorf("rows not rolled back: %#v", rows)
					}
					list, err := db.List(ctx, entity.Name, entity, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					if len(list) != 1 {
						t.Errorf("failed create persisted a header: %#v", list)
					}
				})
			}
		}
	})
}
