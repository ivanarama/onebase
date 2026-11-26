package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// The debugger barrier pauses the real intake DSL after notification, while
// its enclosing storage transaction is still open. No timing-based race.
type notificationCommitBarrier struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*notificationCommitBarrier) HookCheckBreakpoint(_ string, line int, _ func(string) (bool, error)) bool {
	return line == 6
}
func (*notificationCommitBarrier) HookShouldStep(string, int) bool { return false }
func (b *notificationCommitBarrier) HookOnPause(string, int, map[string]any, func(string) (any, error), string) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
}
func (*notificationCommitBarrier) HookPushFrame(string, int) {}
func (*notificationCommitBarrier) HookPopFrame()             {}

func TestIntakeHTTP_NotificationAfterCommit(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, fail := range []bool{false, true} {
			t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
				ctx := context.Background()
				doc := &metadata.Entity{Name: "Звонок", Kind: metadata.KindDocument}
				if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
					t.Fatal(err)
				}
				if err := db.EnsureIntakeSchema(ctx); err != nil {
					t.Fatal(err)
				}
				if err := db.SaveNetworkEnabled(ctx, true); err != nil {
					t.Fatal(err)
				}
				var baseline int
				if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM звонок").Scan(&baseline); err != nil {
					t.Fatal(err)
				}
				src := `Функция Обработать(Конверт) Экспорт
    Док = Документы.Звонок.Создать();
    Док.Записать();
    Данные = Новый Структура("вид,сущность,id", "document", "Звонок", Док.Ссылка.УникальныйИдентификатор());
    ОтправитьУведомление("operator-one", "ui.открытьФорму", Данные);
    Если Конверт.Получить("fail") Тогда
        ВызватьИсключение "test rollback";
    КонецЕсли;
    Возврат Док.Ссылка;
КонецФункции`
				prog, err := parser.New(lexer.New(src, "sitelead.module.os")).ParseProgram()
				if err != nil {
					t.Fatal(err)
				}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
				reg.LoadModules(map[string]*ast.Program{"SiteLead": prog})
				in := &metadata.Intake{Name: "SiteLead", Transport: "http", Endpoint: "/hs/site/lead", Handler: "SiteLead", Auth: "none", Idempotency: metadata.IntakeIdempotency{Key: "event_id"}}
				in.Normalize()
				reg.LoadIntakes([]*metadata.Intake{in})
				barrier := &notificationCommitBarrier{entered: make(chan struct{}), release: make(chan struct{})}
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
				defer release()
				interp := interpreter.New()
				interp.LookupProc = reg.GetModuleProc
				interp.DebugSource = func() interpreter.DebugHook { return barrier }
				hub := realtime.NewHub()
				defer hub.Close()
				s := &Server{store: db, reg: reg, interp: interp, hub: hub, lockMgr: runtime.NewLockManager(), messages: NewMessageStore(), maxFileSizeBytes: 1 << 20}
				s.entitySvc = s.newEntityService(nil)
				_, events, cancel := hub.Subscribe("u1", "operator-one", nil)
				defer cancel()
				_, others, cancelOther := hub.Subscribe("u2", "operator-two", nil)
				defer cancelOther()
				body, _ := json.Marshal(map[string]any{"event_id": t.Name(), "fail": fail})
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					w := httptest.NewRecorder()
					r := httptest.NewRequest(http.MethodPost, "/hs/site/lead", strings.NewReader(string(body)))
					r.Header.Set("Content-Type", "application/json")
					s.serviceDispatch(w, r)
					done <- w
				}()
				select {
				case <-barrier.entered:
				case w := <-done:
					t.Fatalf("intake returned before barrier: %d %s", w.Code, w.Body.String())
				case <-time.After(10 * time.Second):
					t.Fatal("intake did not reach notification barrier")
				}
				select {
				case ev := <-events:
					t.Errorf("notification escaped before commit: %+v", ev)
				default:
				}
				if !db.IsSQLite() {
					// PostgreSQL readers on another connection cannot see the new
					// document yet. SQLite uses a single pooled connection.
					var visible int
					if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM звонок").Scan(&visible); err != nil {
						t.Fatal(err)
					}
					if visible != baseline {
						t.Fatalf("uncommitted document visible: %d, baseline %d", visible, baseline)
					}
				}
				release()
				var w *httptest.ResponseRecorder
				select {
				case w = <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("intake did not finish")
				}
				if fail {
					var count int
					if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM звонок").Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != baseline {
						t.Fatalf("rollback document count=%d, want %d", count, baseline)
					}
					select {
					case ev := <-events:
						t.Fatalf("rollback published notification: %+v", ev)
					default:
					}
				} else {
					if w.Code != http.StatusOK {
						t.Fatalf("intake: %d %s", w.Code, w.Body.String())
					}
					var ev realtime.Event
					select {
					case ev = <-events:
					default:
						t.Fatal("committed intake did not publish")
					}
					if ev.Name != "ui.открытьФорму" {
						t.Fatalf("event=%+v", ev)
					}
					data := ev.Data.(map[string]any)
					id, ok := data["id"].(string)
					if !ok || id == "" {
						t.Fatalf("bad open payload: %+v", data)
					}
					router := chi.NewRouter()
					s.Mount(router)
					get := httptest.NewRecorder()
					router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/ui/document/звонок/"+id, nil))
					if get.Code != http.StatusOK {
						t.Fatalf("GET notification card: %d %s", get.Code, get.Body.String())
					}
					// Same public intake request must not create another card/open.
					dup := httptest.NewRecorder()
					r := httptest.NewRequest(http.MethodPost, "/hs/site/lead", strings.NewReader(string(body)))
					r.Header.Set("Content-Type", "application/json")
					s.serviceDispatch(dup, r)
					if dup.Code != http.StatusOK {
						t.Fatalf("duplicate intake: %d", dup.Code)
					}
					select {
					case ev := <-events:
						t.Fatalf("duplicate published: %+v", ev)
					default:
					}
				}
				select {
				case ev := <-others:
					t.Fatalf("notification leaked to another operator: %+v", ev)
				default:
				}
			})
		}
	})
}
