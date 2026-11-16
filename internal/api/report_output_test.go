package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestAPIV2ReportOutputPrivacy(t *testing.T) {
	entity := &metadata.Entity{Name: "Protected", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, Fields: []metadata.Field{
		{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Phone", Type: metadata.FieldTypeString, PII: true}, {Name: "Address", Type: metadata.FieldTypeString, ReportPII: true}, {Name: "Amount", Type: metadata.FieldTypeNumber},
	}}
	rep := &reportpkg.Report{Name: "Privacy", Query: "ВЫБРАТЬ Ссылка КАК Doc, Phone КАК Contact, Address, Label, Amount ИЗ Справочник.Protected", Composition: &reportpkg.Composition{Groupings: []string{"Doc"}, Measures: []reportpkg.Measure{{Field: "Amount", Agg: "sum"}}, Detail: true}}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{entity}, []*reportpkg.Report{rep}, nil)
	id := uuid.New()
	if err := h.store.Upsert(ctx, entity.Name, id, map[string]any{"Label": "79990009987 HOUSE_987 FLAT_654", "Phone": "79990009987", "Address": "HOUSE_987 FLAT_654", "Amount": 12345}, entity); err != nil {
		t.Fatal(err)
	}
	permission := auth.Permission{Catalogs: map[string][]string{entity.Name: {"read", "disclose"}}, Reports: map[string][]string{rep.Name: {"run"}}, FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{entity.Name: {"Phone": {Read: "full"}, "Address": {Read: "full"}}}}}
	for _, user := range []*auth.User{{IsAdmin: true}, {Roles: []*auth.Role{{Permissions: permission}}}} {
		for _, suffix := range []string{"", "?composition=true"} {
			w := httptest.NewRecorder()
			r := reqWithEntity("GET", "/api/v2/report/Privacy"+suffix, nil, map[string]string{"name": rep.Name}, nil)
			h.runReportV2().ServeHTTP(w, withUser(r, user))
			text := w.Body.String()
			if w.Code != 200 || !strings.Contains(text, id.String()) || !strings.Contains(text, "12345") {
				t.Fatalf("missing report identity/measure: %d %s", w.Code, text)
			}
			for _, secret := range []string{"79990009987", "HOUSE_987", "FLAT_654"} {
				if strings.Contains(text, secret) {
					t.Fatalf("PII in API report: %s", text)
				}
			}
		}
		old := rep.Query
		rep.Query = "ВЫБРАТЬ Amount ИЗ Справочник.Protected ГДЕ Phone = &Secret"
		rep.Params = []reportpkg.Param{{Name: "Secret", Type: "string"}}
		w := httptest.NewRecorder()
		r := reqWithEntity("GET", "/api/v2/report/Privacy?Secret=79990009987", nil, map[string]string{"name": rep.Name}, nil)
		h.runReportV2().ServeHTTP(w, withUser(r, user))
		if w.Code != 403 {
			t.Fatalf("PII filter allowed: %d %s", w.Code, w.Body.String())
		}
		rep.Query = old
	}
}
