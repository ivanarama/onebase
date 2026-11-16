package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
	"github.com/xuri/excelize/v2"
)

// Public routes share one enum domain: UI choice/default/current/empty, exports
// and REST. Labels and metadata order deliberately differ from the identifiers.
func TestReportEnumParamsHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		file := filepath.Join(t.TempDir(), "enum.yaml")
		if err := os.WriteFile(file, []byte(`name: ServiceChoice
values:
  - name: Visit
    titles: {ru: Выезд, en: On site}
  - name: Office
    titles: {ru: В офисе, en: At office}
`), 0o600); err != nil {
			t.Fatal(err)
		}
		en, err := metadata.LoadEnumFile(file)
		if err != nil {
			t.Fatal(err)
		}
		entity := &metadata.Entity{Name: "EnumReportRow", Kind: metadata.KindCatalog, Fields: []metadata.Field{
			{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Service", Type: "enum:ServiceChoice", EnumName: en.Name},
		}}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		for _, v := range en.Values {
			if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Label": "ENUM_ROW_" + v, "Service": v}, entity); err != nil {
				t.Fatal(err)
			}
		}
		rep := &reportpkg.Report{Name: "EnumChoice", Params: []reportpkg.Param{
			{Name: "Pick", Type: "enum:ServiceChoice", Default: "Visit"},
			{Name: "Ref", Type: "reference:EnumReportRow"},
		}, Query: "ВЫБРАТЬ Label ИЗ Справочник.EnumReportRow ГДЕ Service=&Pick"}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Enums: []*metadata.Enum{en}, Reports: []*reportpkg.Report{rep}})
		user := &auth.User{ID: uuid.NewString(), Login: "enum-report-user", Roles: []*auth.Role{{Name: "EnumRunner", Permissions: auth.Permission{
			Reports: map[string][]string{rep.Name: {"run"}}, Catalogs: map[string][]string{entity.Name: {"read"}},
		}}}}
		bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, lang := range []string{"ru", "en"} {
			t.Run(lang, func(t *testing.T) {
				front := ui.New(reg, db, interpreter.New(), nil, ui.Config{Lang: lang, Bundle: bundle}, nil)
				t.Cleanup(front.Close)
				router := chi.NewRouter()
				front.Mount(router)
				(&handler{reg: reg, store: db}).mountV2(router)
				call := func(method, target string, values url.Values) *httptest.ResponseRecorder {
					t.Helper()
					r := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r = r.WithContext(auth.ContextWithUser(r.Context(), user))
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					return w
				}
				t.Run("metadata-choice-and-reference", func(t *testing.T) {
					w := call("GET", "/ui/report/EnumChoice", nil)
					if w.Code != 200 {
						t.Fatalf("form HTTP %d: %s", w.Code, w.Body.String())
					}
					body := w.Body.String()
					label := "Выезд"
					if lang == "en" {
						label = "On site"
					}
					for _, fragment := range []string{`<select name="Pick">`, `<option value="Visit" selected>` + label, `data-ref-entity="EnumReportRow"`, `data-ob-ref-picker="rp-Ref"`} {
						if !strings.Contains(body, fragment) {
							t.Fatalf("missing %q in form", fragment)
						}
					}
					a, b := strings.Index(body, `<option value="Visit"`), strings.Index(body, `<option value="Office"`)
					if a < 0 || b <= a || !strings.Contains(body, `<option value=""`) {
						t.Fatal("enum metadata order or empty choice lost")
					}
				})
				for _, tc := range []struct {
					name    string
					values  url.Values
					want    string
					invalid bool
				}{
					{name: "default", want: "Visit"},
					{name: "selected", values: url.Values{"Pick": {"Office"}}, want: "Office"},
					{name: "cleared", values: url.Values{"Pick": {""}}},
					{name: "free-text", values: url.Values{"Pick": {"Invented"}}, invalid: true},
					{name: "translated-label", values: url.Values{"Pick": {"Выезд"}}, invalid: true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						for _, route := range []struct{ name, method, path string }{
							{"GET", "GET", "/ui/report/EnumChoice?__run=1&"},
							{"POST", "POST", "/ui/report/EnumChoice"},
							{"REST", "GET", "/api/v2/report/EnumChoice?"},
							{"XLSX", "GET", "/ui/report/EnumChoice/excel?"},
							{"PDF", "GET", "/ui/report/EnumChoice/pdf?"},
						} {
							t.Run(route.name, func(t *testing.T) {
								target := route.path
								if route.method == "GET" {
									target += tc.values.Encode()
								}
								w := call(route.method, target, tc.values)
								if tc.invalid {
									if w.Code != 400 {
										t.Fatalf("invalid enum HTTP %d, want 400", w.Code)
									}
									return
								}
								if w.Code != 200 {
									t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
								}
								var labels []string
								switch route.name {
								case "REST":
									var data struct {
										Data []map[string]any `json:"data"`
									}
									if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
										t.Fatal(err)
									}
									for _, row := range data.Data {
										for _, v := range row {
											if s, ok := v.(string); ok {
												labels = append(labels, s)
											}
										}
									}
								case "XLSX":
									book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
									if err != nil {
										t.Fatal(err)
									}
									rows, readErr := book.GetRows(book.GetSheetList()[0])
									closeErr := book.Close()
									if readErr != nil {
										t.Fatal(readErr)
									}
									if closeErr != nil {
										t.Fatal(closeErr)
									}
									for _, row := range rows {
										labels = append(labels, row...)
									}
								case "PDF":
									if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
										t.Fatal("not a PDF")
									}
									return
								default:
									body := w.Body.String()
									if tc.want != "" && !strings.Contains(body, `<option value="`+tc.want+`" selected>`) {
										t.Fatal("current enum selection lost")
									}
									for _, v := range en.Values {
										if strings.Contains(body, "<td>ENUM_ROW_"+v+"</td>") {
											labels = append(labels, "ENUM_ROW_"+v)
										}
									}
								}
								got := strings.Join(labels, "|")
								for _, v := range en.Values {
									if strings.Contains(got, "ENUM_ROW_"+v) != (tc.want == v) {
										t.Fatalf("canonical value mismatch: want %q, got %q", tc.want, got)
									}
								}
							})
						}
					})
				}
			})
		}
	})
}
