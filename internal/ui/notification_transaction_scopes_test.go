package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestProcessorRun_NotificationTransactionScopes(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if err := db.Migrate(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		src := `Процедура Выполнить()
    ОтправитьУведомление("operator-one", "before");
    НачатьТранзакцию();
    Данные = Новый Структура("значение", "original");
    ОтправитьУведомление("operator-one", "outer", Данные);
    Данные.значение = "changed";
    НачатьТранзакцию();
    ОтправитьУведомление("operator-one", "rolled-back-scope");
    ОтменитьТранзакцию();
    НачатьТранзакцию();
    ПоказатьОповещениеПользователя("operator-one", "title", "text");
    ЗафиксироватьТранзакцию();
    Если Параметры.Режим = "commit" Тогда
        ЗафиксироватьТранзакцию();
    ИначеЕсли Параметры.Режим = "rollback" Тогда
        ОтменитьТранзакцию();
    КонецЕсли;
    ОтправитьУведомление("operator-one", "after");
КонецПроцедуры`
		prog, err := parser.New(lexer.New(src, "NotifyScopes.proc.os")).ParseProgram()
		if err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{"NotifyScopes": prog}})
		reg.LoadProcessors([]*processor.Processor{{Name: "NotifyScopes", Params: []processor.Param{{Name: "Режим", Type: "string"}}}})
		i := interpreter.New()
		i.LookupProc = reg.GetModuleProc
		hub := realtime.NewHub()
		defer hub.Close()
		s := &Server{store: db, reg: reg, interp: i, hub: hub, messages: NewMessageStore(), lockMgr: runtime.NewLockManager()}
		s.entitySvc = s.newEntityService(nil)
		router := chi.NewRouter()
		s.Mount(router)
		for _, mode := range []string{"commit", "rollback", "unclosed"} {
			t.Run(mode, func(t *testing.T) {
				_, events, cancel := hub.Subscribe("one", "operator-one", nil)
				defer cancel()
				body := url.Values{"Режим": {mode}}
				r := httptest.NewRequest(http.MethodPost, "/ui/processor/notifyscopes", strings.NewReader(body.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("processor: %d %s", w.Code, w.Body.String())
				}
				var got []string
				for {
					select {
					case ev := <-events:
						got = append(got, ev.Name)
						if ev.Name == "outer" && ev.Data.(map[string]any)["значение"] != "original" {
							t.Fatalf("notification payload changed after send: %+v", ev)
						}
					default:
						goto drained
					}
				}
			drained:
				want := "before,outer,ui.оповещение,after"
				if mode == "rollback" {
					want = "before,after"
				}
				if mode == "unclosed" {
					want = "before"
				}
				if strings.Join(got, ",") != want {
					t.Fatalf("events=%v, want %s", got, want)
				}
			})
		}
	})
}
