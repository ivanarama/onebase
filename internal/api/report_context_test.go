package api

import (
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
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestAPIReportTrustedContextMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		user, err := auth.NewRepo(db).Create(ctx, "context-api-alice", "test-context-password", "Alice", true)
		if err != nil {
			t.Fatal(err)
		}
		card := &metadata.Entity{Name: "APIContextStaff", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Account", Type: "reference:_users", RefEntity: "_users"}, {Name: "Department", Type: metadata.FieldTypeString},
		}}
		entity := &metadata.Entity{Name: "APIContextEntry", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}, {Name: "Department", Type: metadata.FieldTypeString},
			{Name: "OwnerAccount", Type: "reference:_users", RefEntity: "_users"},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{card, entity}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, card.Name, uuid.New(), map[string]any{"Account": user.ID, "Department": "OWN_UNIT"}, card); err != nil {
			t.Fatal(err)
		}
		for _, row := range []map[string]any{
			{"Name": "VISIBLE_API_CONTEXT", "Owner": user.Login, "Department": "OWN_UNIT"},
			{"Name": "FOREIGN_API_USER", "Owner": "bob", "Department": "OWN_UNIT"},
			{"Name": "FOREIGN_API_UNIT", "Owner": user.Login, "Department": "OTHER_UNIT"},
		} {
			row["OwnerAccount"] = user.ID
			if err := db.Upsert(ctx, entity.Name, uuid.New(), row, entity); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "APITrustedContext", Query: `ВЫБРАТЬ Name ИЗ Справочник.APIContextEntry ГДЕ Owner=&Actor И Department=&Unit И OwnerAccount=&ActorID`, Params: []reportpkg.Param{
			{Name: "Actor", Type: "string", Context: &reportpkg.ParamContext{User: "login"}},
			{Name: "ActorID", Type: "reference:_users", Context: &reportpkg.ParamContext{User: "id"}},
			{Name: "Unit", Type: "string", Context: &reportpkg.ParamContext{UserCard: &auth.UserCardSource{Catalog: card.Name, AccountField: "Account", Field: "Department"}}},
		}}
		user.IsAdmin = false
		user.Roles = []*auth.Role{{Name: "ContextRunner", Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}, Catalogs: map[string][]string{entity.Name: {"read"}}}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{card, entity}, Reports: []*reportpkg.Report{rep}})
		h := &handler{reg: reg, store: db, interp: interpreter.New()}
		router := chi.NewRouter()
		h.mountV2(router)
		for _, q := range []url.Values{{}, {"Actor": {"bob"}, "ActorID": {"malformed-client-value"}, "Unit": {"OTHER_UNIT"}}, {"Actor": {""}, "Unit": {""}, "actor": {"bob"}}} {
			t.Run("spoof="+q.Encode(), func(t *testing.T) {
				r := httptest.NewRequest("GET", "/api/v2/report/"+rep.Name+"?"+q.Encode(), nil)
				r = r.WithContext(auth.ContextWithUser(r.Context(), user))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("status%d %s", w.Code, w.Body.String())
				}
				var body struct {
					Data []map[string]any `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Data) != 1 || body.Data[0]["name"] != "VISIBLE_API_CONTEXT" {
					t.Fatalf("wrong actor rows: %#v", body.Data)
				}
			})
		}
		if b, err := json.Marshal(reportOpenAPISchema(rep)); err != nil || strings.Contains(string(b), "Actor") || strings.Contains(string(b), "Unit") {
			t.Fatal("trusted bindings advertised as API inputs")
		}
		user.ID = uuid.NewString()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v2/report/"+rep.Name+"?Actor=bob&Unit=OTHER_UNIT", nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), user))
		router.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("missing user card did not fail closed: %d", w.Code)
		}
	})
}
