package ui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
)

func TestReportResultFilterMountedHTTPAndExports(t *testing.T) {
	entity := &metadata.Entity{Name: "FilterProbe", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, Fields: []metadata.Field{{Name: "Label", Type: "string"}, {Name: "Bucket", Type: "string"}, {Name: "Amount", Type: "number"}, {Name: "Secret", Type: "string", PII: true}}}
	rep := &reportpkg.Report{Name: "FilterReport", Query: "ВЫБРАТЬ П.Bucket КАК bucket, П.Amount КАК amount, П.Secret КАК contact, П.Ссылка КАК rowid ИЗ Справочник.FilterProbe КАК П УПОРЯДОЧИТЬ ПО П.Bucket"}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	t.Cleanup(s.Close)
	if err := s.store.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Reports: []*reportpkg.Report{rep}})
	s.cfg.Limits.ReportMaxRows = 1
	s.cfg.Limits.ExportMaxRows = 100
	for i, name := range []string{"DROP_A", "DROP_B", "KEEP_C", "KEEP_D"} {
		if err := s.store.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Bucket": name, "Amount": i + 1, "Secret": "DO_NOT_DISCLOSE"}, entity); err != nil {
			t.Fatal(err)
		}
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "admin", IsAdmin: true})))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	settings := `{"filters":[{"field":"bucket","op":"contains","value":"KEEP"}]}`
	path := "/ui/report/FilterReport"
	w := request("POST", path, url.Values{"__settings": {settings}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "KEEP_C") || strings.Contains(w.Body.String(), "DROP_A") || strings.Contains(w.Body.String(), "DO_NOT_DISCLOSE") {
		t.Fatalf("filtered HTML: %d %s", w.Code, w.Body.String())
	}
	marker := `id="report-result-filter-fields">`
	begin := strings.Index(w.Body.String(), marker)
	if begin < 0 {
		t.Fatal("missing typed field schema")
	}
	schema := strings.SplitN(w.Body.String()[begin+len(marker):], "</script>", 2)[0]
	if strings.Contains(schema, "contact") {
		t.Fatalf("protected output selectable: %s", schema)
	}
	q := url.Values{"__settings": {settings}}.Encode()
	xlsx := request("GET", path+"/excel?"+q, nil)
	checkXLSX := func(data []byte) {
		t.Helper()
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		var all strings.Builder
		for _, f := range z.File {
			if strings.HasPrefix(f.Name, "xl/") && strings.HasSuffix(f.Name, ".xml") {
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(r)
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				if err != nil {
					t.Fatal(err)
				}
				all.Write(b)
			}
		}
		text := all.String()
		if !strings.Contains(text, "KEEP_C") || !strings.Contains(text, "KEEP_D") || strings.Contains(text, "DROP_A") || strings.Contains(text, "DO_NOT_DISCLOSE") {
			t.Fatalf("filtered workbook differs: %s", text)
		}
	}
	if xlsx.Code != 200 {
		t.Fatalf("xlsx %d %s", xlsx.Code, xlsx.Body.String())
	}
	checkXLSX(xlsx.Body.Bytes())
	pdf := request("GET", path+"/pdf?"+q, nil)
	if pdf.Code != 200 || !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("pdf %d %s", pdf.Code, pdf.Body.String())
	}
	saved := request("POST", path+"/settings/save", url.Values{"__settings": {settings}, "__preset_action": {"save_as"}, "__preset_name": {"Filter test"}})
	if saved.Code != 303 {
		t.Fatalf("save preset %d %s", saved.Code, saved.Body.String())
	}
	target, err := url.Parse(saved.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	preset := target.Query().Get("__preset")
	if preset == "" {
		t.Fatal("missing preset id")
	}
	persisted := request("GET", path+"?__run=1&__preset="+url.QueryEscape(preset), nil)
	if persisted.Code != 200 || !strings.Contains(persisted.Body.String(), "KEEP_C") || strings.Contains(persisted.Body.String(), "DROP_A") {
		t.Fatalf("preset not applied %d", persisted.Code)
	}
	bad := `{"filters":[{"field":"contact","op":"eq","value":"DO_NOT_DISCLOSE"}]}`
	for _, route := range []string{path, path + "/settings/save"} {
		got := request("POST", route, url.Values{"__settings": {bad}})
		if got.Code != 400 || strings.Contains(got.Body.String(), "DO_NOT_DISCLOSE") {
			t.Fatalf("forged field accepted/echoed %s %d", route, got.Code)
		}
	}
	malformed := request("POST", path, url.Values{"__settings": {"{"}})
	if malformed.Code != 400 {
		t.Fatal("broken settings silently ignored", malformed.Code)
	}
	start := request("GET", path+"/export/excel?"+q, nil)
	if start.Code != 303 {
		t.Fatalf("job %d %s", start.Code, start.Body.String())
	}
	jobID := strings.TrimPrefix(start.Header().Get("Location"), "/ui/export-jobs/")
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := s.exportJobStore().get(jobID)
		if ok && job.Status == exportJobDone {
			checkXLSX(job.Data)
			break
		}
		if ok && job.Status == exportJobError {
			t.Fatal(job.Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("background filter timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(schema), &parsed); err == nil {
		t.Fatal("field schema must be an array")
	}
}
