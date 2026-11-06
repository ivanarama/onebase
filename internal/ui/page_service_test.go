package ui

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestPageEmbeddedServiceHTTP(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"/hs/messenger/", true}, {"/hs/messenger/?mode=embedded", true},
		{"https://example.com/", false}, {"//example.com/hs/", false},
		{"javascript:alert(1)", false}, {"/ui/app", false},
		{"/hs/../ui/app", false}, {"/hs/%2e%2e/ui/app", false},
		{"/hs/", false}, {"/hs", false}, {"/hs/\\evil", false}, {"/hs//other", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			prog := mustParse(t, `Процедура ПриФормировании(Страница, Параметры) Экспорт
    Страница.ВстроитьСервис("`+tc.url+`", "Чат <script>");
   КонецПроцедуры`)
			reg := runtime.NewRegistry()
			reg.LoadPages([]*page.Page{{Name: "Chat"}})
			reg.Load(runtime.LoadOptions{PagePrograms: map[string]*ast.Program{"Chat": prog}})
			interp := interpreter.New()
			interp.LookupProc = reg.GetModuleProc
			s := &Server{store: db, reg: reg, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
			s.entitySvc = s.newEntityService(nil)
			router := chi.NewRouter()
			router.Get("/ui/page/{name}", s.page)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/page/Chat", nil))
			body := rec.Body.String()
			hasFrame := strings.Contains(body, `<iframe class="ob-service-frame"`)
			if hasFrame != tc.valid {
				t.Fatalf("valid=%v frame=%v status=%d: %s", tc.valid, hasFrame, rec.Code, body)
			}
			if tc.valid {
				if !strings.Contains(body, `main-list ob-page-application`) {
					t.Fatal("service does not occupy workspace")
				}
				if !strings.Contains(body, `title="Чат &lt;script&gt;"`) {
					t.Fatal("iframe title must be escaped")
				}
			} else if !strings.Contains(body, "Страница.ВстроитьСервис:") {
				t.Fatal("invalid path must report an error")
			}
		})
	}
}
