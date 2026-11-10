package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// The debugger supplies a deterministic execution barrier after the public
// notification builtin; the intake transport, transaction and DSL are real.
type notifyCommitBarrier struct {
	file    string
	line    int
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *notifyCommitBarrier) HookCheckBreakpoint(file string, line int, _ func(string) (bool, error)) bool {
	if strings.HasSuffix(file, b.file) && line == b.line {
		b.once.Do(func() { close(b.arrived) })
		<-b.release
	}
	return false
}
func (*notifyCommitBarrier) HookShouldStep(string, int) bool { return false }
func (*notifyCommitBarrier) HookOnPause(string, int, map[string]any, func(string) (any, error), string) {
}
func (*notifyCommitBarrier) HookPushFrame(string, int) {}
func (*notifyCommitBarrier) HookPopFrame()             {}

func TestIntakeHTTP_NotificationFollowsCommit(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("rollback=%v", rollback), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				entity := &metadata.Entity{Name: "NotifyCall", Kind: metadata.KindDocument,
					Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
				s := newSubmitTestServerOn(t, db, []*metadata.Entity{entity})
				s = New(s.reg, db, s.interp, nil, Config{}, nil)
				defer s.Close()
				beforeCount, err := db.CountList(ctx, entity.Name, entity, storage.ListParams{})
				if err != nil {
					t.Fatal(err)
				}
				s.maxFileSizeBytes = 1 << 20
				if err := db.EnsureIntakeSchema(ctx); err != nil {
					t.Fatal(err)
				}
				if err := db.SaveNetworkEnabled(ctx, true); err != nil {
					t.Fatal(err)
				}
				const source = `Функция Обработать(Конверт) Экспорт
 Док = Документы.NotifyCall.Создать();
 Док.Наименование = "synthetic notification";
 Док.Записать();
 ОтправитьУведомление("recipient", "ui.открытьФорму", Новый Структура("вид,сущность,id,scope", "document", "NotifyCall", Док.Ссылка, "shell-synthetic"));
 Сообщить("commit barrier");
 Если Конверт.Получить("fail") Тогда
  ВызватьИсключение "synthetic rollback";
 КонецЕсли;
 Возврат Док.Ссылка;
КонецФункции`
				program, err := parser.New(lexer.New(source, "notify-intake.module.os")).ParseProgram()
				if err != nil {
					t.Fatal(err)
				}
				s.reg.LoadModules(map[string]*ast.Program{"NotifyIntake": program})
				in := &metadata.Intake{Name: "NotifyIntake", Transport: "http", Endpoint: "/hs/notify/call", Handler: "NotifyIntake", Auth: "none",
					Idempotency: metadata.IntakeIdempotency{Key: "event_id", Scope: []string{"source"}}}
				in.Normalize()
				s.reg.LoadIntakes([]*metadata.Intake{in})
				barrier := &notifyCommitBarrier{file: "notify-intake.module.os", line: 6, arrived: make(chan struct{}), release: make(chan struct{})}
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(barrier.release) }) }
				s.interp.DebugSource = func() interpreter.DebugHook { return barrier }
				_, events, unsubscribe := s.hub.Subscribe("recipient", "recipient", nil)
				defer unsubscribe()
				_, otherEvents, unsubscribeOther := s.hub.Subscribe("bystander", "bystander", nil)
				defer unsubscribeOther()
				router := chi.NewRouter()
				router.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "recipient", IsAdmin: true})))
					})
				})
				s.Mount(router)
				router.HandleFunc("/hs/*", s.serviceDispatch)
				server := httptest.NewServer(router)
				defer server.Close()
				defer unblock()
				streamRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/ui/events", nil)
				stream, err := http.DefaultClient.Do(streamRequest)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = stream.Body.Close() }()
				body := fmt.Sprintf(`{"event_id":"notification-%v","source":"synthetic","fail":%v}`, rollback, rollback)
				type result struct {
					code int
					body string
					err  error
				}
				post := func() result {
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+in.Endpoint, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					response, err := http.DefaultClient.Do(req)
					if err != nil {
						return result{err: err}
					}
					defer func() { _ = response.Body.Close() }()
					data, err := io.ReadAll(response.Body)
					return result{code: response.StatusCode, body: string(data), err: err}
				}
				done := make(chan result, 1)
				go func() { done <- post() }()
				select {
				case <-barrier.arrived:
				case completed := <-done:
					t.Fatalf("intake exited before commit barrier: %+v", completed)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var premature *realtime.Event
				select {
				case event := <-events:
					premature = &event
				default:
				}
				if premature != nil && db.IsPostgres() {
					data := premature.Data.(map[string]any)
					get, err := http.Get(server.URL + "/ui/document/NotifyCall/" + fmt.Sprint(data["id"]))
					if err != nil {
						t.Fatal(err)
					}
					_ = get.Body.Close()
					t.Logf("premature notification GET status: %d", get.StatusCode)
				}
				unblock()
				completed := <-done
				if completed.err != nil {
					t.Fatal(completed.err)
				}
				if premature != nil {
					t.Fatalf("notification published before intake commit (rollback=%v)", rollback)
				}
				if rollback {
					if strings.Contains(completed.body, `"status":"Принято"`) {
						t.Fatalf("rollback accepted: %s", completed.body)
					}
					select {
					case event := <-events:
						t.Fatalf("rolled-back notification: %+v", event)
					default:
					}
				} else {
					if completed.code != http.StatusOK {
						t.Fatalf("intake: %+v", completed)
					}
					var frame struct {
						Name string         `json:"name"`
						Data map[string]any `json:"data"`
					}
					if err := json.Unmarshal([]byte(readDataFrame(t, stream.Body)), &frame); err != nil {
						t.Fatal(err)
					}
					if frame.Name != "ui.открытьФорму" || frame.Data["scope"] != "shell-synthetic" {
						t.Fatalf("notification payload/scoping changed: %+v", frame)
					}
					get, err := http.Get(server.URL + "/ui/document/NotifyCall/" + fmt.Sprint(frame.Data["id"]))
					if err != nil {
						t.Fatal(err)
					}
					page, readErr := io.ReadAll(get.Body)
					_ = get.Body.Close()
					if get.StatusCode != http.StatusOK || readErr != nil || !strings.Contains(string(page), "synthetic notification") {
						t.Fatalf("committed notification GET status=%d err=%v page=%s", get.StatusCode, readErr, page)
					}
					<-events
					duplicate := post()
					if duplicate.err != nil || duplicate.code != http.StatusOK || !strings.Contains(duplicate.body, "Дубль") {
						t.Fatalf("duplicate intake: %+v", duplicate)
					}
					select {
					case event := <-events:
						t.Fatalf("duplicate intake notification: %+v", event)
					default:
					}
				}
				select {
				case event := <-otherEvents:
					t.Fatalf("notification leaked to another login: %+v", event)
				default:
				}
				count, err := db.CountList(ctx, entity.Name, entity, storage.ListParams{})
				wantCount := beforeCount
				if !rollback {
					wantCount++
				}
				if err != nil || count != wantCount {
					t.Fatalf("document count=%d want=%d err=%v", count, wantCount, err)
				}
			})
		}
	})
}

// Processor variables are installed before an explicit DSL transaction starts.
// The notification must consult the live TxState, not its initial context.
func TestProcessorHTTP_NotificationUsesLiveTransaction(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("rollback=%v", rollback), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				s := newSubmitTestServerOn(t, db, nil)
				s = New(s.reg, db, s.interp, nil, Config{}, nil)
				defer s.Close()
				finish := "ЗафиксироватьТранзакцию"
				if rollback {
					finish = "ОтменитьТранзакцию"
				}
				source := `Процедура Выполнить()
 НачатьТранзакцию();
 ОтправитьУведомление("recipient", "transaction-check", "synthetic");
 Сообщить("commit barrier");
 ` + finish + `();
КонецПроцедуры`
				program, err := parser.New(lexer.New(source, "notify-dsl.proc.os")).ParseProgram()
				if err != nil {
					t.Fatal(err)
				}
				s.reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{"NotifyTransaction": program}})
				s.reg.LoadProcessors([]*processor.Processor{{Name: "NotifyTransaction"}})
				barrier := &notifyCommitBarrier{file: "notify-dsl.proc.os", line: 4, arrived: make(chan struct{}), release: make(chan struct{})}
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(barrier.release) }) }
				s.interp.DebugSource = func() interpreter.DebugHook { return barrier }
				_, events, unsubscribe := s.hub.Subscribe("recipient", "recipient", nil)
				defer unsubscribe()
				router := chi.NewRouter()
				s.Mount(router)
				server := httptest.NewServer(router)
				defer server.Close()
				defer unblock()
				done := make(chan error, 1)
				go func() {
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/ui/processor/NotifyTransaction", strings.NewReader(""))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					response, err := http.DefaultClient.Do(req)
					if err == nil {
						defer func() { _ = response.Body.Close() }()
						body, readErr := io.ReadAll(response.Body)
						err = readErr
						if response.StatusCode != http.StatusOK {
							err = fmt.Errorf("processor status=%d body=%s", response.StatusCode, body)
						}
					}
					done <- err
				}()
				select {
				case <-barrier.arrived:
				case err := <-done:
					t.Fatalf("processor exited before barrier: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				var premature bool
				select {
				case <-events:
					premature = true
				default:
				}
				unblock()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if premature {
					t.Fatal("notification ignored the explicitly started DSL transaction")
				}
				select {
				case event := <-events:
					if rollback || event.Name != "transaction-check" || event.Data != "synthetic" {
						t.Fatalf("unexpected transaction notification: %+v", event)
					}
				default:
					if !rollback {
						t.Fatal("committed DSL notification missing")
					}
				}
			})
		}
	})
}
