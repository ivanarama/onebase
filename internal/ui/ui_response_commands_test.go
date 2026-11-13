package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestFormHTTPCommandsFallbackCommitAndScope(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		dir := t.TempDir()
		yaml := "schema: onebase.form/v1\nform:\n  name: Main\n  kind: object\n  entity: Probe\nelements:\n"
		module := ""
		for _, mode := range []string{"Commit", "Rollback", "Fail"} {
			yaml += fmt.Sprintf("  - kind: Кнопка\n    name: %s\n    events:\n      Нажатие: %s\n", mode, mode)
			module += "Процедура " + mode + "()\nНачатьТранзакцию();\nОбъект.Наименование = \"synthetic\";\nОбъект.Записать();\n" +
				"Д = Новый Структура(\"вид,сущность,id\", \"catalog\", \"Probe\", \"synthetic\");\n" +
				"ОтправитьУведомление(\"anna\", \"ui.открытьФорму\", Д);\n" +
				"ОтправитьУведомление(\"anna\", \"ui.закрытьФорму\", Д);\n" +
				"ОтправитьУведомление(\"other\", \"ui.открытьФорму\", Д);\n" +
				"ОтправитьУведомление(\"*\", \"ui.оповещение\", \"broadcast\");\n"
			switch mode {
			case "Commit":
				module += "ЗафиксироватьТранзакцию();\n"
			case "Rollback":
				module += "ОтменитьТранзакцию();\n"
			case "Fail":
				module += "ВызватьИсключение \"test rollback\";\n"
			}
			module += "КонецПроцедуры\n"
		}
		for name, contents := range map[string]string{"Main.form.yaml": yaml, "Main.form.os": module} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
		}
		fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "Main.form.yaml"), "Probe")
		if err != nil {
			t.Fatal(err)
		}
		ent := &metadata.Entity{Name: "Probe", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{fm}}
		if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		reg := orun.NewRegistry()
		reg.Load(orun.LoadOptions{Entities: []*metadata.Entity{ent}})
		interp := interpreter.New()
		interp.LookupProc = reg.GetModuleProc
		srv := New(reg, db, interp, nil, Config{}, nil)
		defer srv.Close()
		router := chi.NewRouter()
		router.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "own-user", Login: "anna", IsAdmin: true})))
			})
		})
		srv.Mount(router)
		for _, mode := range []string{"Commit", "Rollback", "Fail", "Legacy"} {
			t.Run(mode, func(t *testing.T) {
				_, events, cancel := srv.hub.SubscribeShellSince("own-user", "anna", nil, "shell-A", 0)
				defer cancel()
				button := mode
				if mode == "Legacy" {
					button = "Commit"
				}
				body := url.Values{"_element": {button}, "_event": {"Нажатие"}, "Наименование": {"synthetic"}}
				req := httptest.NewRequest(http.MethodPost, "/ui/catalog/probe/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set(shellHeader, "shell-A")
				if mode != "Legacy" {
					req.Header.Set("X-OneBase-UI-Commands", "1")
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var response formEventResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.OK != (mode != "Fail") {
					t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
				}
				if mode == "Commit" {
					if len(response.UICommands) != 2 || !response.RefreshLive {
						t.Fatalf("missing committed self commands/invalidation: %s", w.Body.String())
					}
					for _, command := range response.UICommands {
						select {
						case ev := <-events:
							if ev.Name != command.Name || ev.CommandID == "" || ev.CommandID != command.CommandID || ev.Shell != "shell-A" {
								t.Fatalf("SSE and HTTP differ: %+v / %+v", ev, command)
							}
						default:
							t.Fatal("SSE copy missing")
						}
					}
				} else if len(response.UICommands) != 0 {
					t.Fatalf("commands leaked into %s: %s", mode, w.Body.String())
				}
				if mode == "Legacy" && response.RefreshLive {
					t.Fatal("legacy response changed")
				}
			})
		}
	})
}

func TestFormHTTPCommandsClientDeduplicatesAndRefreshes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node missing")
	}
	raw, err := os.ReadFile("static/ui.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	a := strings.Index(s, "  // BEGIN onebase-ui-response-commands")
	b := strings.Index(s, "  // END onebase-ui-response-commands")
	if a < 0 || b < a {
		t.Fatal("delivery markers missing")
	}
	snippet, _ := json.Marshal(s[a:b])
	code := fmt.Sprintf(`const assert=require('node:assert/strict'),vm=require('node:vm');const code=%s;
function harness(embedded=false){const events=[],forwarded=[],listeners={},posts=[],child={};const win={__obEmbedded:embedded,location:{origin:'http://own'},parent:{postMessage:(m,o)=>posts.push({m,o})},addEventListener:(n,f)=>listeners[n]=f};const ctx={window:win,document:{querySelectorAll:()=>[{contentWindow:child}]},Set,emitOnebaseEvent:(name,data)=>events.push({name,data}),forwardOnebaseEvent:m=>forwarded.push(m),toast:()=>{}};vm.runInNewContext(code,ctx);return {ctx,win,events,forwarded,listeners,posts,child}}
const open={name:'ui.открытьФорму',data:{entity:'Synthetic'},commandId:'12345678-1234-1234-1234-123456789012'};
const close={name:'ui.закрытьФорму',data:{entity:'Synthetic'},commandId:'12345678-1234-1234-1234-123456789013'};
for(const httpFirst of [true,false]){const h=harness();h.win.__obEvents={readyState:1};if(httpFirst)h.win.obApplyUIResponse([open,close],true);h.ctx.deliverServerMessage(open);h.ctx.deliverServerMessage(close);h.win.obApplyUIResponse([open,close],true);assert.equal(h.events.length,2);assert.equal(h.forwarded.length,2)}
{const h=harness();h.win.obApplyUIResponse([open],true);assert.equal(h.events.length,2);assert.equal(h.events[1].name,'onebase:__oblive_refresh_all__');h.ctx.deliverServerMessage(open);assert.equal(h.events.length,2);h.win.obApplyUIResponse([{name:'arbitrary',data:'no'}],false);assert.equal(h.events.length,2)}
{const h=harness();h.listeners.message({origin:'http://evil',source:h.child,data:{source:'obUIResponse',commands:[open]}});h.listeners.message({origin:'http://own',source:{},data:{source:'obUIResponse',commands:[open]}});assert.equal(h.events.length,0);h.listeners.message({origin:'http://own',source:h.child,data:{source:'obUIResponse',commands:[open]}});assert.equal(h.events.length,1)}
{const h=harness(true);h.win.obApplyUIResponse([open],true);assert.equal(h.events.length,0);assert.equal(h.posts.length,1);assert.equal(h.posts[0].o,'http://own')}
console.log('HTTP/SSE delivery PASS');`, snippet)
	if output, err := exec.Command(node, "-e", code).CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
}
