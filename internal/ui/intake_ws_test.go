package ui

// Интеграционные тесты WS-транспорта приёмки (план 120A): настоящий WS-сервер
// (httptest + апгрейд), настоящая приёмка, DSL-обработчик. Проверяется путь
// пользователя целиком: ResyncWSIntakes → соединение → конверт → Ingest.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Обработчик: обычные события возвращают ссылку, boom в payload — исключение
// (путь карантина handler_error).
const wsIntakeHandlerSrc = `
Функция Обработать(Конверт) Экспорт
    Если Конверт.Получить("payload").Получить("boom") = Истина Тогда
        ВызватьИсключение "бум";
    КонецЕсли;
    Возврат "ref-" + Строка(Конверт.Получить("event_id"));
КонецФункции
`

// newWSIntakeServer собирает ui.Server с одним ws-шлюзом, направленным на url.
func newWSIntakeServer(t *testing.T, url string, netOn bool) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureIntakeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, netOn); err != nil {
		t.Fatal(err)
	}

	prog, err := parser.New(lexer.New(wsIntakeHandlerSrc, "wslead.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.LoadModules(map[string]*ast.Program{"WSLead": prog})
	registry.LoadIntakes([]*metadata.Intake{wsTestIntake(t, url)})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
	}
	t.Cleanup(func() { wsClose(t, s) })
	return s
}

func wsTestIntake(t *testing.T, url string) *metadata.Intake {
	t.Helper()
	in := &metadata.Intake{
		Name: "WSLead", Transport: "ws", URL: url, Handler: "WSLead", Auth: "none",
		Idempotency: metadata.IntakeIdempotency{Key: "event_id", Scope: []string{"source"}},
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	return in
}

// wsClose гоняет полный graceful shutdown и следит, что он не завис: Shutdown
// ждёт горутины соединений через backgroundWG — утечка проявится таймаутом.
func wsClose(t *testing.T, s *Server) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); s.Close() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown не дождался горутин WS-соединений")
	}
}

// wsIntakeTestServer — WS-сервер стороны «внешнего хаба».
func wsIntakeTestServer(t *testing.T, session func(ctx context.Context, n int64, c *websocket.Conn)) *httptest.Server {
	t.Helper()
	var sessions atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		session(r.Context(), sessions.Add(1), c)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsTestURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") }

func wsWaitStats(t *testing.T, s *Server, what string, cond func(st storage.IntakeStats) bool) storage.IntakeStats {
	t.Helper()
	var st storage.IntakeStats
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		st, err = s.store.IntakeLogStats(context.Background(), "WSLead")
		if err != nil {
			t.Fatal(err)
		}
		if cond(st) {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("не дождались (%s), статистика: %+v", what, st)
	return st
}

// Счастливый путь + повторная доставка после реконнекта: то, ради чего ws
// посажен на приёмку — дубль после обрыва не создаёт второй бизнес-объект.
func TestIntakeWS_HappyThenDuplicateAfterReconnect(t *testing.T) {
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) {
		if n == 1 {
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"event_id":"A1","source":"hub","payload":{"x":1}}`))
			_ = c.CloseNow() // обрыв: доставленное до ack переедет в следующую сессию
			return
		}
		// Сервер не знает, что A1 дошло, и шлёт снова — норма для WS.
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event_id":"A1","source":"hub","payload":{"x":1}}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event_id":"A2","source":"hub","payload":{"x":2}}`))
		<-ctx.Done()
	})
	s := newWSIntakeServer(t, wsTestURL(srv), true)
	s.ResyncWSIntakes()

	st := wsWaitStats(t, s, "обработаны A1 и A2", func(st storage.IntakeStats) bool { return st.Processed >= 2 })
	if st.Processed != 2 || st.Quarantined != 0 {
		t.Fatalf("повторная доставка A1 создала лишнюю запись: %+v", st)
	}
	client := s.wsIntakeClient("WSLead")
	if client == nil {
		t.Fatal("клиент шлюза не найден")
	}
	if got := client.Status(); got.Received != 3 {
		// 3 доставки (A1, A1-повтор, A2) → 2 обработки: дубль погашен приёмкой.
		t.Fatalf("ожидали 3 принятых сообщения, статус: %+v", got)
	}
}

// Ошибка обработчика: событие в карантине, соединение живо, поток не встал.
func TestIntakeWS_HandlerErrorQuarantinesAndKeepsStream(t *testing.T) {
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event_id":"B1","source":"hub","payload":{"boom":true}}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"event_id":"B2","source":"hub","payload":{"x":1}}`))
		<-ctx.Done()
	})
	s := newWSIntakeServer(t, wsTestURL(srv), true)
	s.ResyncWSIntakes()

	st := wsWaitStats(t, s, "B1 в карантине, B2 обработано", func(st storage.IntakeStats) bool {
		return st.Quarantined == 1 && st.Processed == 1
	})
	if st.OpenDLQ != 1 {
		t.Fatalf("ожидали открытую запись карантина: %+v", st)
	}
	if client := s.wsIntakeClient("WSLead"); !client.Status().Connected {
		t.Fatal("соединение должно пережить ошибку обработчика")
	}
}

// Предохранитель сети: выключен — соединение не поднимается и причина видна;
// включили — поднялось без рестарта. Проверяется через публичную точку
// (ResyncWSIntakes), а не вызовом guard-а (правило #611).
func TestIntakeWS_NetworkGuard(t *testing.T) {
	var accepted atomic.Int64
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) {
		accepted.Add(1)
		<-ctx.Done()
	})
	s := newWSIntakeServer(t, wsTestURL(srv), false)
	s.ResyncWSIntakes()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c := s.wsIntakeClient("WSLead"); c != nil && c.Status().BlockedReason != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	client := s.wsIntakeClient("WSLead")
	if client == nil || client.Status().BlockedReason == "" {
		t.Fatal("при выключенной сети ожидали явную причину блокировки")
	}
	if accepted.Load() != 0 {
		t.Fatal("при выключенной сети было подключение")
	}

	if err := s.store.SaveNetworkEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if client.Status().Connected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("после включения сети соединение не поднялось")
}

// Resync: шлюз исчез из конфигурации → клиент остановлен и недоступен.
func TestIntakeWS_ResyncDropsRemoved(t *testing.T) {
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) { <-ctx.Done() })
	s := newWSIntakeServer(t, wsTestURL(srv), true)
	s.ResyncWSIntakes()
	if s.wsIntakeClient("WSLead") == nil {
		t.Fatal("клиент должен подняться")
	}

	s.reg.LoadIntakes(nil) // конфигурация без шлюзов (горячая перезагрузка)
	s.ResyncWSIntakes()
	if s.wsIntakeClient("WSLead") != nil {
		t.Fatal("удалённый шлюз должен быть остановлен")
	}
}
