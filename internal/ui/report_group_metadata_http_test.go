package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/xuri/excelize/v2"
)

func TestReportGroupMetadataSourceKindHTTP(t *testing.T) {
	for _, collision := range []bool{false, true} {
		name := "known_source"
		if collision {
			name = "catalog_inforeg_same_name"
		}
		t.Run(name, func(t *testing.T) {
			const title = "Подпись регистра сведений"
			ir := &metadata.InfoRegister{Name: "Shared", Dimensions: []metadata.Field{{Name: "Code", Type: "string", Title: title}}, Resources: []metadata.Field{{Name: "Amount", Type: "number"}}}
			rep := &reportpkg.Report{Name: "metadata_levels", Query: "ВЫБРАТЬ Code КАК Группа, Amount КАК Сумма ИЗ РегистрСведений.Shared",
				Composition: &reportpkg.Composition{Groupings: []string{"Группа"}, Measures: []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}}}}
			s := newReportExportTestServer(t, rep)
			opts := runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}, Reports: []*reportpkg.Report{rep}}
			if collision {
				opts.Entities = []*metadata.Entity{{Name: "Shared", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Code", Type: "string", Title: "Чужая подпись справочника"}}}}
			}
			s.reg.Load(opts)
			ctx := context.Background()
			if err := s.store.MigrateInfoRegisters(ctx, opts.InfoRegs); err != nil {
				t.Fatal(err)
			}
			if err := s.store.WriteInfoMovements(ctx, ir.Name, "Synthetic", uuid.New(), []map[string]any{{"Code": "PUBLIC_GROUP", "Amount": float64(12.5)}}, ir, nil); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			s.Mount(router)
			for _, format := range []string{"html", "excel"} {
				path := "/ui/report/metadata_levels?__run=1"
				if format == "excel" {
					path = "/ui/report/metadata_levels/excel"
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: HTTP%d: %.300s", format, w.Code, w.Body.String())
				}
				if format == "html" {
					if !strings.Contains(w.Body.String(), "<th>"+title+"</th>") {
						t.Fatalf("group header lost source kind: %.300s", w.Body.String())
					}
					continue
				}
				book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				got, readErr := book.GetCellValue(book.GetSheetName(0), "A1")
				closeErr := book.Close()
				if readErr != nil || closeErr != nil || got != title {
					t.Fatalf("XLSX header=%q read=%v close=%v", got, readErr, closeErr)
				}
			}
			if rep.Composition.Groupings[0] != "Группа" || ir.Dimensions[0].Title != title {
				t.Fatal("presentation mutated trusted metadata")
			}
		})
	}
}

func TestReportGroupMetadataCompilerProvenance(t *testing.T) {
	const title = "Подпись доказанного поля"
	for _, tc := range []struct {
		name, text, want string
		opts             runtime.LoadOptions
		accounts         []*metadata.AccountRegister
	}{
		{name: "table_part", text: "ВЫБРАТЬ L.Code КАК Группа ИЗ Справочник.Shared.Lines КАК L", want: title,
			opts: runtime.LoadOptions{Entities: []*metadata.Entity{{Name: "Shared", Kind: metadata.KindCatalog,
				Fields:     []metadata.Field{{Name: "Code", Type: "string", Title: "Подпись шапки"}},
				TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{{Name: "Code", Type: "string", Title: title}}}}}}}},
		{name: "qualified_join", text: "ВЫБРАТЬ L.Code КАК Группа ИЗ Справочник.First КАК L ЛЕВОЕ СОЕДИНЕНИЕ Справочник.Second КАК R ПО 1 = 1", want: title,
			opts: runtime.LoadOptions{Entities: []*metadata.Entity{
				{Name: "First", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Code", Type: "string", Title: title}}},
				{Name: "Second", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "L", Type: "reference:Third", RefEntity: "Third"}}},
				{Name: "Third", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Code", Type: "string", Title: "Чужое поле"}}},
			}}},
		{name: "ambiguous_register", text: "ВЫБРАТЬ Code КАК Группа ИЗ РегистрНакопления.Shared", want: "Группа",
			opts:     runtime.LoadOptions{Registers: []*metadata.Register{{Name: "Shared", Dimensions: []metadata.Field{{Name: "Code", Type: "string", Title: "РН"}}}}},
			accounts: []*metadata.AccountRegister{{Name: "Shared", Resources: []metadata.Field{{Name: "Code", Type: "number", Title: "Регбух"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := runtime.NewRegistry()
			registry.Load(tc.opts)
			registry.LoadAccountRegisters(tc.accounts, nil)
			s := &Server{reg: registry}
			compiled, err := query.Compile(tc.text, query.CompileOpts{Entities: tc.opts.Entities})
			if err != nil {
				t.Fatal(err)
			}
			spec := &reportpkg.Composition{Groupings: []string{"Группа"}}
			presentation := s.compositionPresentation(&reportpkg.Report{}, spec, &compiled, "ru")
			if got := presentation.groupHeader(spec); got != tc.want {
				t.Fatalf("header=%q, want %q; sources=%+v", got, tc.want, compiled.SourceOccurrences)
			}
		})
	}
}
