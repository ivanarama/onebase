package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/xlsxadvisory"
	"strings"
)

func TestProcessorBinaryParam_PublicAdvisories(t *testing.T) {
	for _, scenario := range xlsxadvisory.Cases[:3] {
		t.Run(scenario, func(t *testing.T) {
			xlsxadvisory.Run(t, func() {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					s, ctx, unused, cat := binaryImportFixture(t, db)
					unused.Close()
					var paths []string
					s.createProcessorTempFile = func(dir, pattern string) (processorUploadFile, error) {
						f, err := os.CreateTemp(dir, pattern)
						if err == nil {
							paths = append(paths, f.Name())
						}
						return f, err
					}
					router := chi.NewRouter()
					s.Mount(router)
					finished := make(chan struct{})
					ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(finished); router.ServeHTTP(w, r) }))
					defer ts.Close()
					_, page := postBinaryProcessor(t, ts, "ЗагрузкаУчеников", "Файл", "bad.xlsx", xlsxadvisory.Book(t, scenario), "ru")
					<-finished
					if !strings.Contains(page, "ПрочитатьExcel") || strings.Contains(page, "путь: ") {
						t.Fatalf("did not reject in DSL reader: %s", page)
					}
					rows, err := s.store.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
					if err != nil || len(rows) != 0 {
						t.Fatalf("partial catalog import: %v, %v", rows, err)
					}
					if len(paths) != 1 {
						t.Fatalf("upload not reached: %v", paths)
					}
					for _, path := range paths {
						if _, err := os.Stat(path); !os.IsNotExist(err) {
							t.Fatalf("upload file survived: %v", err)
						}
					}
				})
			})
		})
	}
}

func TestPrintDocumentXLSX_PublicAdvisories(t *testing.T) {
	for _, scenario := range xlsxadvisory.Cases {
		t.Run(scenario, func(t *testing.T) {
			xlsxadvisory.Run(t, func() {
				ent := &metadata.Entity{Name: "Карточка", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
				s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
				s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
				id := uuid.New()
				if err := s.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "007"}, ent); err != nil {
					t.Fatal(err)
				}
				s.reg.LoadLayoutForms([]*printform.LayoutForm{{Name: "Бланк", Document: ent.Name, Layout: &printform.LayoutTemplate{}, XLSXTemplate: xlsxadvisory.Book(t, scenario)}})
				router := chi.NewRouter()
				s.Mount(router)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/catalog/Карточка/"+id.String()+"/print/Бланк/xlsx", nil))
				if scenario == "cyclic-array" {
					if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "PK") {
						t.Fatalf("cached workbook render: %d %s", rec.Code, rec.Body.String())
					}
				} else {
					if rec.Code != http.StatusInternalServerError || strings.HasPrefix(rec.Body.String(), "PK") || rec.Header().Get("Content-Disposition") != "" {
						t.Fatalf("bad template produced download: %d %v", rec.Code, rec.Header())
					}
				}
			})
		})
	}
}
