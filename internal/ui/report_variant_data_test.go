package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/xuri/excelize/v2"
)

func TestReportVariantDataScreenAndExports(t *testing.T) {
	rep, err := reportpkg.ParseBytes([]byte(`name: Rows
query: SELECT 'A-excluded' AS Имя, 10 AS Повторка UNION ALL SELECT 'Z-wanted' AS Имя, 0 AS Повторка
variants:
  - name: filtered
    filters: [{field: Повторка, op: eq, value: 0}]
  - name: custom
    query: SELECT 'Z-wanted' AS Имя, 0 AS Повторка
`))
	if err != nil {
		t.Fatal(err)
	}
	s := newReportExportTestServer(t, rep)
	s.cfg.Limits.ReportMaxRows = 1
	s.cfg.Limits.ExportMaxRows = 1
	router := chi.NewRouter()
	router.Get("/ui/report/{name}", s.reportForm)
	router.Get("/ui/report/{name}/excel", s.reportExcel)
	router.Get("/ui/report/{name}/pdf", s.reportPDF)
	router.Get("/ui/report/{name}/export/{format}", s.reportExportJobStart)
	router.Get("/ui/export-jobs/{id}/download", s.exportJobDownload)
	request := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		return rec
	}
	checkExcel := func(data []byte) {
		t.Helper()
		book, err := excelize.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer book.Close()
		rows, err := book.GetRows(book.GetSheetName(0))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[1][0] != "Z-wanted" {
			t.Fatalf("wrong export rows: %v", rows)
		}
	}
	for _, variant := range []string{"filtered", "custom"} {
		suffix := "?__variant=" + variant
		screen := request("/ui/report/Rows" + suffix + "&__run=1")
		if screen.Code != 200 || !strings.Contains(screen.Body.String(), ">Z-wanted<") || strings.Contains(screen.Body.String(), ">A-excluded<") {
			t.Fatalf("screen %s: %d %s", variant, screen.Code, screen.Body.String())
		}
		for _, format := range []string{"excel", "pdf"} {
			rec := request("/ui/report/Rows/" + format + suffix)
			if rec.Code != 200 {
				t.Fatalf("%s %s: %d %s", variant, format, rec.Code, rec.Body.String())
			}
			if format == "excel" {
				checkExcel(rec.Body.Bytes())
			} else if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF")) {
				t.Fatal("not PDF")
			}
			started := request("/ui/report/Rows/export/" + format + suffix)
			if started.Code != http.StatusSeeOther {
				t.Fatalf("start: %d %s", started.Code, started.Body.String())
			}
			location := started.Header().Get("Location")
			id := strings.TrimPrefix(location, "/ui/export-jobs/")
			deadline := time.Now().Add(10 * time.Second)
			var job exportJob
			for time.Now().Before(deadline) {
				job, _ = s.exportJobStore().get(id)
				if job.Status == exportJobDone || job.Status == exportJobError {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if job.Status != exportJobDone {
				t.Fatalf("background %s: %s %s", format, job.Status, job.Error)
			}
			downloaded := request(location + "/download")
			if downloaded.Code != 200 {
				t.Fatalf("download: %d", downloaded.Code)
			}
			if format == "excel" {
				checkExcel(downloaded.Body.Bytes())
			} else if !bytes.HasPrefix(downloaded.Body.Bytes(), []byte("%PDF")) {
				t.Fatal("background not PDF")
			}
		}
	}
}
