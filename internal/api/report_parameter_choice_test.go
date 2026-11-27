package api

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestAPIV2ReportParameterChoice(t *testing.T) {
	branches := &metadata.Entity{Name: "Branches", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
	directions := &metadata.Entity{Name: "Directions", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Branch", Type: metadata.FieldType("reference:Branches"), RefEntity: "Branches"}}}
	rep := &reportpkg.Report{Name: "Choices", Params: []reportpkg.Param{{Name: "Branch", Type: "reference:Branches"}, {Name: "Direction", Type: "reference:Directions", ChoiceFilter: []reportpkg.ParamChoiceFilter{{Field: "Branch", Param: "Branch"}}}}, Query: "ВЫБРАТЬ Name ИЗ Справочник.Directions"}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{branches, directions}, []*reportpkg.Report{rep}, nil)
	b1, b2, d := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{b1, b2} {
		if err := h.store.Upsert(ctx, branches.Name, id, map[string]any{"Name": "Branch"}, branches); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.store.Upsert(ctx, directions.Name, d, map[string]any{"Name": "Direction", "Branch": b1}, directions); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		branch, direction string
		denied            bool
		want              int
	}{
		{b1.String(), d.String(), false, 200}, {b2.String(), d.String(), false, 400},
		{"", d.String(), false, 200}, {b1.String(), "", false, 200},
		{uuid.NewString(), "", false, 400}, {b1.String(), d.String(), true, 400},
	} {
		q := url.Values{"Branch": {tc.branch}, "Direction": {tc.direction}}
		r := reqWithEntity("GET", "/api/v2/report/Choices?"+q.Encode(), nil, map[string]string{"name": rep.Name}, nil)
		if tc.denied {
			r = withUser(r, apiUser("runner", auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}, Catalogs: map[string][]string{directions.Name: {"read"}}}))
		}
		w := httptest.NewRecorder()
		h.runReportV2().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
}
