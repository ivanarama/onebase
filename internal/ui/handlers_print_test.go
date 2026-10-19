package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/xuri/excelize/v2"
)

// newRedirectReq собирает запрос с chi-route-параметрами для redirectDSLPrint
// и возвращает записанный ответ.
func newRedirectReq(path string) *httptest.ResponseRecorder {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "documents")
	rctx.URLParams.Add("entity", "sale")
	rctx.URLParams.Add("id", "00000000-0000-0000-0000-000000000001")
	rctx.URLParams.Add("pfName", "upd")

	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	s := &Server{}
	s.redirectDSLPrint(rec, req)
	return rec
}

// TestRedirectDSLPrintKeepsQuery: 301 со старого /print-dsl/ должен сохранять
// строку запроса (минор-фикс плана 64, этап 3).
func TestRedirectDSLPrintKeepsQuery(t *testing.T) {
	rec := newRedirectReq("/ui/documents/sale/00000000-0000-0000-0000-000000000001/print-dsl/upd?form=upd&x=1")
	if rec.Code != 301 {
		t.Fatalf("status = %d (want 301)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	want := "/ui/documents/sale/00000000-0000-0000-0000-000000000001/print/upd?form=upd&x=1"
	if loc != want {
		t.Fatalf("Location = %q\nwant     %q", loc, want)
	}
}

// TestRedirectDSLPrintNoQuery: без query строка запроса не приклеивается
// (нет висящего «?»).
func TestRedirectDSLPrintNoQuery(t *testing.T) {
	rec := newRedirectReq("/ui/documents/sale/00000000-0000-0000-0000-000000000001/print-dsl/upd")
	loc := rec.Header().Get("Location")
	want := "/ui/documents/sale/00000000-0000-0000-0000-000000000001/print/upd"
	if loc != want {
		t.Fatalf("Location = %q\nwant     %q", loc, want)
	}
}

// TestRedirectDSLPrintPDFKeepsQuery: PDF-хвост и query сохраняются одновременно.
func TestRedirectDSLPrintPDFKeepsQuery(t *testing.T) {
	rec := newRedirectReq("/ui/documents/sale/00000000-0000-0000-0000-000000000001/print-dsl/upd/pdf?form=upd")
	loc := rec.Header().Get("Location")
	want := "/ui/documents/sale/00000000-0000-0000-0000-000000000001/print/upd/pdf?form=upd"
	if loc != want {
		t.Fatalf("Location = %q\nwant     %q", loc, want)
	}
}

func TestPrintDocumentXLSXThroughHTTPHandler(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})

	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "A1", "{{Контрагент.Наименование}}"); err != nil {
		t.Fatal(err)
	}
	var template bytes.Buffer
	if err := f.Write(&template); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	formNames := []string{"Карточка", "Карточка 20%", "Карточка %41"}
	var forms []*printform.LayoutForm
	for _, name := range formNames {
		forms = append(forms, &printform.LayoutForm{
			Name: name, Document: entity.Name,
			Layout: &printform.LayoutTemplate{}, XLSXTemplate: template.Bytes(),
		})
	}
	s.reg.LoadLayoutForms(forms)
	router := chi.NewRouter()
	s.Mount(router)
	mounted := chi.NewRouter()
	mounted.Mount("/mounted", router)

	id := uuid.New()
	if err := s.store.Upsert(ctx, entity.Name, id, map[string]any{
		"Наименование": "ООО Ромашка",
		"Номер":        "К-7",
	}, entity); err != nil {
		t.Fatal(err)
	}
	for _, name := range formNames {
		for _, rawPath := range []bool{false, true} {
			for _, prefix := range []string{"", "/mounted"} {
				source := "Path"
				encodedName := url.PathEscape(name)
				if rawPath {
					source = "RawPath"
					encodedName = strings.ReplaceAll(encodedName, "%D0", "%d0")
				}
				t.Run(name+"/"+source+prefix, func(t *testing.T) {
					target := prefix + "/ui/catalog/" + url.PathEscape(entity.Name) + "/" + id.String() + "/print/" + encodedName + "/xlsx"
					req := httptest.NewRequest(http.MethodGet, target, nil)
					if got := req.URL.RawPath != ""; got != rawPath {
						t.Fatalf("RawPath set = %v, want %v", got, rawPath)
					}
					rec := httptest.NewRecorder()
					if prefix == "" {
						router.ServeHTTP(rec, req)
					} else {
						mounted.ServeHTTP(rec, req)
					}
					if rec.Code != http.StatusOK {
						t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
					}
					if got := rec.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
						t.Errorf("Content-Type = %q", got)
					}
					if got := rec.Header().Get("Content-Disposition"); !bytes.Contains([]byte(got), []byte(".xlsx")) {
						t.Errorf("Content-Disposition = %q", got)
					}
					out, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := out.Close(); err != nil {
							t.Errorf("Close XLSX: %v", err)
						}
					}()
					if got, err := out.GetCellValue("Sheet1", "A1"); err != nil || got != "ООО Ромашка" {
						t.Errorf("A1 = %q, %v", got, err)
					}
				})
			}
		}
	}
}
