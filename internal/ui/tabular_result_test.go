package ui

import (
	"bytes"
	"context"
	"embed"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/project"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
)

//go:embed testdata/tabular-result
var tabularExampleFiles embed.FS

func tabularExampleProject(t *testing.T) *project.Project {
	t.Helper()
	dir := t.TempDir()
	const prefix = "testdata/tabular-result"
	err := fs.WalkDir(tabularExampleFiles, prefix, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == prefix {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, prefix+"/")))
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := tabularExampleFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proj.Close)
	return proj
}

func tabularHTTP(s *Server, actor *auth.User, session, method, path string, body url.Values) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	s.Mount(router)
	r := httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "onebase_session", Value: session})
	r = r.WithContext(auth.ContextWithUser(r.Context(), actor))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestTabularLayoutReportHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		proj := tabularExampleProject(t)
		s, user, _ := tabularFixture(t, db)
		s.reg.Load(runtime.LoadOptions{Reports: proj.Reports, Programs: proj.Programs})
		s.reg.LoadLayoutForms(proj.LayoutForms)
		w := tabularHTTP(s, user, "own-session", "POST", "/ui/report/TabularReport", url.Values{"Период": {"Ноябрь2026"}})
		if w.Code != 303 {
			t.Fatalf("macro report HTTP%d: %s", w.Code, w.Body.String())
		}
		path := w.Header().Get("Location")
		view := tabularHTTP(s, user, "own-session", "GET", path, nil)
		if view.Code != 200 || !strings.Contains(view.Body.String(), "150006,00") || !strings.Contains(view.Body.String(), "Заявка 2") {
			t.Fatalf("module total/detail missing: HTTP%d", view.Code)
		}
		v, ok := s.tabularResults.items[strings.TrimPrefix(path, "/ui/tabular/")]
		if !ok || len(v.doc.Groups) != 20 || v.doc.HeaderArea == nil {
			t.Fatal("native macro grouping/header lost")
		}
	})
}

func tabularFixture(t *testing.T, db *storage.DB) (*Server, *auth.User, *reportpkg.Report) {
	t.Helper()
	rep := &reportpkg.Report{Name: "TabularReport", Title: "KPI", ResultProc: "Build"}
	prog := mustParse(t, `Функция Build()
 Д = Новый ТабличныйДокумент;
 Д.ИмяФайла = "КПИ_период";
 Д.Ячейка(1,1).Текст = "Кириллица <script>bad()</script>";
 Д.Ячейка(2,1).Значение = 12.5;
 Д.Ячейка(2,1).Формат = "ЧДЦ=2";
 Возврат Д;
 КонецФункции`)
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{rep.Name: prog}, Reports: []*reportpkg.Report{rep}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	s := &Server{reg: reg, store: db, interp: interp, messages: NewMessageStore(), lockMgr: runtime.NewLockManager(), ops: newOperationLimiter()}
	s.entitySvc = s.newEntityService(nil)
	user := &auth.User{ID: "own-tabular", Login: "own-tabular", Roles: []*auth.Role{{Name: "Own", Permissions: auth.Permission{Reports: map[string][]string{rep.Name: {"run"}}}}}}
	return s, user, rep
}

func TestTabularReportResultHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"same-instance-export", "foreign-session", "foreign-user", "revoked-role", "expired", "metadata-reloaded"} {
			t.Run(mode, func(t *testing.T) {
				s, user, rep := tabularFixture(t, db)
				w := tabularHTTP(s, user, "own-session", "POST", "/ui/report/"+rep.Name, nil)
				if w.Code != 303 {
					t.Fatalf("generate HTTP%d: %s", w.Code, w.Body.String())
				}
				path := w.Header().Get("Location")
				if !strings.HasPrefix(path, "/ui/tabular/") {
					t.Fatal("no native result")
				}
				switch mode {
				case "foreign-session":
					if tabularHTTP(s, user, "other-session", "GET", path, nil).Code != 404 {
						t.Fatal("foreign session read")
					}
					return
				case "foreign-user":
					other := *user
					other.ID = "other-actor"
					if tabularHTTP(s, &other, "own-session", "GET", path, nil).Code != 404 {
						t.Fatal("foreign user read")
					}
					return
				case "revoked-role":
					user.Roles = nil
					if tabularHTTP(s, user, "own-session", "GET", path+"/xlsx", nil).Code != 404 {
						t.Fatal("revoked user read")
					}
					return
				case "expired":
					s.tabularResults.items[strings.TrimPrefix(path, "/ui/tabular/")].expires = time.Now().Add(-time.Second)
					if tabularHTTP(s, user, "own-session", "GET", path, nil).Code != 404 {
						t.Fatal("expired read")
					}
					return
				case "metadata-reloaded":
					s.reg.Load(runtime.LoadOptions{})
					if tabularHTTP(s, user, "own-session", "GET", path, nil).Code != 404 {
						t.Fatal("removed report read")
					}
					return
				}
				rep.ResultProc = "DoesNotExist" // Exports must use the built instance, not run again.
				view := tabularHTTP(s, user, "own-session", "GET", path, nil)
				if view.Code != 200 || !strings.Contains(view.Body.String(), "12,50") || strings.Contains(view.Body.String(), "<script>bad()") {
					t.Fatal("native viewer content/escaping")
				}
				xlsx := tabularHTTP(s, user, "own-session", "GET", path+"/xlsx", nil)
				if xlsx.Code != 200 {
					t.Fatalf("xlsx HTTP%d", xlsx.Code)
				}
				book, err := excelize.OpenReader(bytes.NewReader(xlsx.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				defer book.Close()
				raw, err := book.GetCellValue("Лист1", "A2", excelize.Options{RawCellValue: true})
				if err != nil || raw != "12.5" {
					t.Fatalf("numeric value %q %v", raw, err)
				}
				pdf := tabularHTTP(s, user, "own-session", "GET", path+"/pdf", nil)
				if pdf.Code != 200 || !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF-")) {
					t.Fatal("native PDF result")
				}
			})
		}
	})
}

func TestTabularManagedFieldHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		s, user, _ := tabularFixture(t, db)
		form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Build", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Build"}}, &metadata.FormElement{Kind: metadata.FormElementSpreadsheet, Name: "Sheet", DataPath: "Объект.Result"})
		form.Attributes = []*metadata.FormAttribute{{Name: "Result", TypeRef: "SpreadsheetDocument", Save: false}}
		form.ProgramAST = mustParse(t, `Процедура Build()
   Д = Новый ТабличныйДокумент;
   Д.Ячейка(1,1).Текст = "Нативный результат";
   Объект.Result = Д;
   КонецПроцедуры`)
		proc := &processor.Processor{Name: "TabularProcessor", Forms: []*metadata.FormModule{form}}
		s.reg.LoadProcessors([]*processor.Processor{proc})
		user.Roles[0].Permissions.Processors = map[string][]string{proc.Name: {"run"}}
		get := tabularHTTP(s, user, "own-session", "GET", "/ui/processor/"+proc.Name, nil)
		if get.Code != 200 || !strings.Contains(get.Body.String(), "data-ob-tabular-field") {
			t.Fatal("native form field not rendered")
		}
		body := url.Values{"_form": {form.Name}, "_element": {"Build"}, "_event": {string(metadata.FormEventOnClick)}}
		post := tabularHTTP(s, user, "own-session", "POST", "/ui/processor/"+proc.Name+"/form-event", body)
		if post.Code != 200 {
			t.Fatalf("event HTTP%d", post.Code)
		}
		resp := decodeFormEventResponse(t, post.Body.Bytes())
		if !resp.OK || resp.Dirty == nil || *resp.Dirty || resp.TabularDocuments["result"] == "" {
			t.Fatalf("native readonly result: %s", post.Body.String())
		}
		view := tabularHTTP(s, user, "own-session", "GET", resp.TabularDocuments["result"], nil)
		if view.Code != 200 || !strings.Contains(view.Body.String(), "Нативный результат") {
			t.Fatal("field result not viewable")
		}
	})
}

func TestTabularDrilldownHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"typed-link", "typed-form-event", "wrong-session", "wrong-cell", "wrong-token", "denied-target"} {
			t.Run(mode, func(t *testing.T) {
				ctx := context.Background()
				s, user, _ := tabularFixture(t, db)
				target := &metadata.Entity{Name: "TabularTarget", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
				if err := db.Migrate(ctx, []*metadata.Entity{target}); err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				ownName := "own-target-" + id.String()
				if err := db.Upsert(ctx, target.Name, id, map[string]any{"Name": ownName}, target); err != nil {
					t.Fatal(err)
				}
				s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target}})
				form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Build", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Build"}}, &metadata.FormElement{Kind: metadata.FormElementSpreadsheet, Name: "Sheet", DataPath: "Объект.Result", Handlers: map[metadata.FormEventType]string{metadata.FormEventDrilldown: "Expand"}})
				form.Attributes = []*metadata.FormAttribute{{Name: "Result", TypeRef: "SpreadsheetDocument"}}
				form.ProgramAST = mustParse(t, strings.ReplaceAll(`Процедура Build()
     Д=Новый ТабличныйДокумент;
     Д.Ячейка(1,1).Текст="Target";
     Д.Ячейка(1,1).Расшифровка=Справочники.TabularTarget.НайтиПоРеквизиту("Name","own-target");
     Объект.Result=Д;
     КонецПроцедуры
     Процедура Expand()
     ОткрытьФорму(Расшифровка);
     КонецПроцедуры`, "own-target", ownName))
				proc := &processor.Processor{Name: "TabularLinks", Forms: []*metadata.FormModule{form}}
				s.reg.LoadProcessors([]*processor.Processor{proc})
				user.Roles[0].Permissions.Processors = map[string][]string{proc.Name: {"run"}}
				user.Roles[0].Permissions.Catalogs = map[string][]string{target.Name: {"read"}}
				path := "/ui/processor/" + proc.Name + "/form-event"
				build := tabularHTTP(s, user, "own-session", "POST", path, url.Values{"_element": {"Build"}, "_event": {"Нажатие"}})
				resp := decodeFormEventResponse(t, build.Body.Bytes())
				if !resp.OK || resp.TabularDocuments["result"] == "" {
					t.Fatalf("build: %s", build.Body.String())
				}
				resultPath := resp.TabularDocuments["result"]
				if mode == "typed-link" {
					w := tabularHTTP(s, user, "own-session", "GET", resultPath+"/cell?row=0&col=0", nil)
					if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), id.String()) {
						t.Fatal("typed link failed")
					}
					return
				}
				body := url.Values{"_element": {"Sheet"}, "_event": {"ОбработкаРасшифровки"}, "_tabular_token": {strings.TrimPrefix(resultPath, "/ui/tabular/")}, "_tabular_row": {"0"}, "_tabular_col": {"0"}}
				session := "own-session"
				switch mode {
				case "wrong-session":
					session = "foreign"
				case "wrong-cell":
					body.Set("_tabular_row", "100")
				case "wrong-token":
					body.Set("_tabular_token", uuid.NewString())
				case "denied-target":
					user.Roles[0].Permissions.Catalogs = nil
				}
				click := tabularHTTP(s, user, session, "POST", path, body)
				got := decodeFormEventResponse(t, click.Body.Bytes())
				if mode == "typed-form-event" {
					if !got.OK || got.Navigation == nil || !strings.Contains(got.Navigation.URL, id.String()) {
						t.Fatalf("native drilldown: %s", click.Body.String())
					}
				} else if got.OK || got.Navigation != nil {
					t.Fatal("unvalidated drilldown reached handler")
				}
			})
		}
	})
}
