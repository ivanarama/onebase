package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestAPIReportDiscloseDoesNotCrossJoinedSources(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		client := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString, PII: true}}}
		request := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString, PII: true}}}
		entities := []*metadata.Entity{client, request}
		if err := db.Migrate(t.Context(), entities); err != nil {
			t.Fatal(err)
		}
		for _, entity := range entities {
			if err := db.Upsert(t.Context(), entity.Name, uuid.New(), map[string]any{"Телефон": "SECRET-PHONE"}, entity); err != nil {
				t.Fatal(err)
			}
		}
		reports := []*reportpkg.Report{
			{Name: "БезСоединения", Query: `ВЫБРАТЬ Телефон ИЗ Документ.Заявка ГДЕ Телефон = "SECRET-PHONE"`},
			{Name: "СоСоединением", Query: `ВЫБРАТЬ З.Телефон ИЗ Документ.Заявка КАК З ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Клиент КАК К ПО ИСТИНА ГДЕ З.Телефон = "SECRET-PHONE"`},
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: entities, Reports: reports})
		h := &handler{reg: registry, store: db}
		user := apiUser("operator", auth.Permission{Catalogs: map[string][]string{client.Name: {"read", "disclose"}}, Documents: map[string][]string{request.Name: {"read"}}, Reports: map[string][]string{"БезСоединения": {"run"}, "СоСоединением": {"run"}}})
		for _, allowed := range []bool{false, true} {
			if allowed {
				user.Roles[0].Permissions.Documents[request.Name] = []string{"read", "disclose"}
			}
			for _, rep := range reports {
				rec := httptest.NewRecorder()
				req := withUser(reqWithEntity(http.MethodGet, "/api/v2/report/"+rep.Name, nil, map[string]string{"name": rep.Name}, nil), user)
				h.runReportV2().ServeHTTP(rec, req)
				want := http.StatusForbidden
				if allowed {
					want = http.StatusOK
				}
				if rec.Code != want {
					t.Fatalf("%s disclose=%v: %d %s", rep.Name, allowed, rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "SECRET-PHONE") {
					t.Fatalf("unmasked response: %s", rec.Body.String())
				}
				if allowed && !strings.Contains(rec.Body.String(), `"total":1`) {
					t.Fatalf("allowed query lost its row: %s", rec.Body.String())
				}
			}
		}
	})
}
