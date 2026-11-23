package ui

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

func TestOrdinaryReportXLSXUsedRangeMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := &metadata.Entity{Name: "ListDimensionSource", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Team", Type: metadata.FieldTypeString}, {Name: "Amount", Type: metadata.FieldTypeNumber}}}
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 22; i++ {
			team := "A"
			if i > 11 {
				team = "B"
			}
			if err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Team": team, "Amount": i}, e); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "OrdinaryDimension", Query: `ВЫБРАТЬ Team, Amount ИЗ Справочник.ListDimensionSource`, Composition: &reportpkg.Composition{Groupings: []string{"team"}, Measures: []reportpkg.Measure{{Field: "amount", Agg: "sum"}}, Detail: true, Totals: reportpkg.Totals{Grand: true, Subtotals: true}}}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{e}, Reports: []*reportpkg.Report{rep}})
		s := &Server{store: db, reg: reg, interp: interpreter.New(), messages: NewMessageStore(), lockMgr: runtime.NewLockManager(), ops: newOperationLimiter()}
		t.Cleanup(s.Close)
		router := chi.NewRouter()
		s.Mount(router)
		check := func(data []byte, mode string) {
			t.Helper()
			z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range z.File {
				if p.Name != "xl/worksheets/sheet1.xml" {
					continue
				}
				r, err := p.Open()
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(r)
				if closeErr := r.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if err != nil {
					t.Fatal(err)
				}
				var sh struct {
					Dimension struct {
						Ref string `xml:"ref,attr"`
					} `xml:"dimension"`
					Rows []struct {
						R int `xml:"r,attr"`
					} `xml:"sheetData>row"`
				}
				if err := xml.Unmarshal(raw, &sh); err != nil {
					t.Fatal(err)
				}
				if sh.Dimension.Ref != "A1:B28" || len(sh.Rows) != 28 {
					t.Fatalf("ordinary XLSX dimension=%s rows%d, wantA1:B28/28", sh.Dimension.Ref, len(sh.Rows))
				}
				found = true
			}
			if !found {
				t.Fatal("worksheet missing")
			}
			f, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}()
			rows, err := f.GetRows("Лист1")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 28 || len(rows[27]) < 2 || rows[27][0] != "ВСЕГО" || rows[27][1] != "253" {
				t.Fatal("ordinary report rows/totals changed")
			}
			if dir := os.Getenv("ONEBASE_TEST_XLSX_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil { // #nosec G703 -- Opt-in test artifact directory supplied by the trusted test runner.
					t.Fatal(err)
				}
				name := "ordinary-" + db.Dialect().Name() + "-" + mode + ".xlsx"
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil { // #nosec G703 -- Trusted runner directory; the filename uses only fixed test fixture labels.
					t.Fatal(err)
				}
			}
		}
		t.Run("direct", func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/ui/report/OrdinaryDimension/excel", nil))
			if w.Code != 200 {
				t.Fatalf("export HTTP%d %s", w.Code, w.Body.String())
			}
			check(w.Body.Bytes(), "direct")
		})
		t.Run("background", func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/ui/report/OrdinaryDimension/export/excel", nil))
			if w.Code != 303 {
				t.Fatalf("queueHTTP%d", w.Code)
			}
			jobID := strings.TrimPrefix(w.Header().Get("Location"), "/ui/export-jobs/")
			deadline := time.Now().Add(10 * time.Second)
			for {
				j, ok := s.exportJobStore().get(jobID)
				if ok && j.Status == exportJobDone {
					check(j.Data, "background")
					return
				}
				if ok && j.Status == exportJobError {
					t.Fatal(j.Error)
				}
				if time.Now().After(deadline) {
					t.Fatal("export timeout")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	})
}
