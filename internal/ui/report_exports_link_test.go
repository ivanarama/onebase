package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestReportMyExportsLinkHTTP(t *testing.T) {
	for _, tc := range []struct {
		name        string
		composition *reportpkg.Composition
		run         bool
		result      string
	}{
		{name: "parameters"},
		{name: "table", run: true, result: "<table>"},
		{
			name: "composed", run: true, result: `data-block="data"`,
			composition: &reportpkg.Composition{
				Groupings: []string{"Число"}, Detail: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := &reportpkg.Report{
				Name: "exportslink", Query: "ВЫБРАТЬ &Число КАК Число",
				Params:      []reportpkg.Param{{Name: "Число", Type: "number", Default: "1"}},
				Composition: tc.composition,
			}
			s := newReportExportTestServer(t, rep)
			router := chi.NewRouter()
			s.Mount(router)
			path := "/ui/report/exportslink"
			if tc.run {
				path += "?__run=1"
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("report returned %d: %s", w.Code, w.Body.String())
			}
			page := w.Body.String()
			if count := strings.Count(page, `href="/ui/export-jobs"`); count != 1 {
				t.Errorf("report has %d links to My exports, want one", count)
			}
			if !strings.Contains(page, `data-block="params"`) {
				t.Fatal("report parameter form is missing")
			}
			for _, format := range []string{"excel", "pdf"} {
				hasButton := strings.Contains(page, "/ui/report/exportslink/export/"+format)
				if hasButton != tc.run {
					t.Errorf("%s export button present = %v, want %v", format, hasButton, tc.run)
				}
			}
			if tc.run && !strings.Contains(page, tc.result) {
				t.Fatalf("expected report result %q is missing", tc.result)
			}
			// Follow the report's link through the same mounted HTTP router.
			list := httptest.NewRecorder()
			router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/ui/export-jobs", nil))
			if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Доступных выгрузок пока нет.") {
				t.Fatalf("My exports returned %d: %s", list.Code, list.Body.String())
			}
		})
	}
}
