package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestReportResultFilterAPIPaginationAndPresetGate(t *testing.T) {
	entity := &metadata.Entity{Name: "FilterProbe", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, Fields: []metadata.Field{{Name: "Label", Type: "string"}, {Name: "Bucket", Type: "string"}, {Name: "Amount", Type: "number"}, {Name: "Secret", Type: "string", PII: true}}}
	rep := &reportpkg.Report{Name: "FilterReport", Query: "ВЫБРАТЬ П.Bucket КАК bucket, П.Amount КАК amount, П.Secret КАК contact ИЗ Справочник.FilterProbe КАК П УПОРЯДОЧИТЬ ПО П.Bucket"}
	h, ctx := newAPITestHandlerWithReports(t, []*metadata.Entity{entity}, []*reportpkg.Report{rep}, nil)
	if err := h.store.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"DROP_A", "DROP_B", "KEEP_C", "KEEP_D"} {
		if err := h.store.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Bucket": name, "Amount": i + 1, "Secret": "private"}, entity); err != nil {
			t.Fatal(err)
		}
	}
	request := func(q url.Values) *httptest.ResponseRecorder {
		r := reqWithEntity("GET", "/api/v2/report/FilterReport?"+q.Encode(), nil, map[string]string{"name": rep.Name}, nil)
		w := httptest.NewRecorder()
		h.runReportV2().ServeHTTP(w, r)
		return w
	}
	settings := `{"filters":[{"field":"bucket","op":"contains","value":"KEEP"},{"field":"amount","op":"ge","value":"3"}]}`
	for _, tc := range []struct{ page, want string }{{"1", "KEEP_C"}, {"2", "KEEP_D"}} {
		w := request(url.Values{"__settings": {settings}, "limit": {"1"}, "page": {tc.page}})
		if w.Code != http.StatusOK {
			t.Fatalf("api %d %s", w.Code, w.Body.String())
		}
		var got struct {
			Data []map[string]any `json:"data"`
			Meta restV2Meta       `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data) != 1 || got.Data[0]["bucket"] != tc.want || got.Meta.Total != 2 || got.Meta.TotalPages != 2 || got.Data[0]["contact"] != nil {
			t.Fatalf("pagination/PII %#v %+v", got.Data, got.Meta)
		}
	}
	for _, raw := range []string{"{", `{"filters":[{"field":"contact","op":"eq","value":"private"}]}`, `{"filters":[{"field":"amount","op":"contains","value":"1"}]}`} {
		if w := request(url.Values{"__settings": {raw}}); w.Code != 400 {
			t.Fatalf("invalid filter %d %s", w.Code, w.Body.String())
		}
	}
	if w := request(url.Values{"__preset": {"not-owned"}}); w.Code != 400 {
		t.Fatal("unknown preset silently ignored", w.Code)
	}
}
