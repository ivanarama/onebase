package wsclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsServer поднимает настоящий WS-сервер (httptest + апгрейд): поведение на
// разрыве проверяется на живом соединении, а не на моке (план 120, тесты).
func wsServer(t *testing.T, session func(ctx context.Context, c *websocket.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		session(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// waitFor поллит условие до таймаута — события приходят из чужих горутин.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

func TestClient_ReceivesAndReconnects(t *testing.T) {
	var sessions atomic.Int64
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		n := sessions.Add(1)
		if n == 1 {
			_ = c.Write(ctx, websocket.MessageText, []byte(`первое`))
			// Рвём соединение без прощания — как это делает упавший сервер.
			_ = c.CloseNow()
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`второе`))
		<-ctx.Done()
	})

	var got []string
	gotCh := make(chan string, 8)
	c := New(Config{
		Name:             "тест",
		URL:              wsURL(srv),
		ReconnectInitial: 10 * time.Millisecond,
		ReconnectMax:     50 * time.Millisecond,
		OnMessage: func(_ context.Context, raw []byte) error {
			gotCh <- string(raw)
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()

	waitFor(t, "оба сообщения через реконнект", func() bool {
		for {
			select {
			case m := <-gotCh:
				got = append(got, m)
			default:
				return len(got) >= 2
			}
		}
	})
	if got[0] != "первое" || got[1] != "второе" {
		t.Fatalf("сообщения: %v", got)
	}
	st := c.Status()
	if st.Reconnects < 1 {
		t.Fatalf("ожидали хотя бы один реконнект, статус: %+v", st)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился по отмене контекста")
	}
}

func TestClient_HandlerErrorKeepsConnection(t *testing.T) {
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`плохое`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`хорошее`))
		<-ctx.Done()
	})

	gotCh := make(chan string, 8)
	c := New(Config{
		Name: "тест",
		URL:  wsURL(srv),
		OnMessage: func(_ context.Context, raw []byte) error {
			gotCh <- string(raw)
			if string(raw) == "плохое" {
				return context.DeadlineExceeded // любая ошибка обработчика
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	var got []string
	waitFor(t, "оба сообщения несмотря на ошибку обработчика", func() bool {
		for {
			select {
			case m := <-gotCh:
				got = append(got, m)
			default:
				return len(got) >= 2
			}
		}
	})
	st := c.Status()
	if !st.Connected || st.HandlerErrors != 1 {
		t.Fatalf("ожидали живое соединение и одну ошибку обработчика: %+v", st)
	}
}

func TestClient_GateBlocksDial(t *testing.T) {
	var accepted atomic.Int64
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		accepted.Add(1)
		<-ctx.Done()
	})

	var open atomic.Bool // false = предохранитель выключен
	c := New(Config{
		Name:             "тест",
		URL:              wsURL(srv),
		ReconnectInitial: 10 * time.Millisecond,
		ReconnectMax:     20 * time.Millisecond,
		Gate: func() string {
			if open.Load() {
				return ""
			}
			return "сеть отключена предохранителем"
		},
		OnMessage: func(context.Context, []byte) error { return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	waitFor(t, "статус «заблокировано»", func() bool { return c.Status().BlockedReason != "" })
	if n := accepted.Load(); n != 0 {
		t.Fatalf("при закрытом предохранителе было %d подключений", n)
	}

	open.Store(true) // включили предохранитель — подключение без рестарта
	waitFor(t, "подключение после открытия предохранителя", func() bool { return c.Status().Connected })
	if got := c.Status().BlockedReason; got != "" {
		t.Fatalf("причина блокировки не сброшена: %q", got)
	}
}

func TestClient_SendRequiresConnection(t *testing.T) {
	c := New(Config{Name: "тест", URL: "ws://127.0.0.1:1", OnMessage: func(context.Context, []byte) error { return nil }})
	// Клиент не запущен: отправка обязана упасть сразу, без буферизации.
	if err := c.Send(context.Background(), []byte("x")); err == nil {
		t.Fatal("Send без соединения должен возвращать ошибку")
	}
	if st := c.Status(); st.SendErrors != 1 {
		t.Fatalf("SendErrors: %+v", st)
	}
}

func TestClient_SendDelivers(t *testing.T) {
	fromClient := make(chan string, 1)
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		_, data, err := c.Read(ctx)
		if err == nil {
			fromClient <- string(data)
		}
		<-ctx.Done()
	})

	c := New(Config{Name: "тест", URL: wsURL(srv), OnMessage: func(context.Context, []byte) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, "подключение", func() bool { return c.Status().Connected })

	if err := c.Send(ctx, []byte("привет")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-fromClient:
		if got != "привет" {
			t.Fatalf("сервер получил %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("сервер не получил сообщение")
	}
	if st := c.Status(); st.Sent != 1 {
		t.Fatalf("Sent: %+v", st)
	}
}

func TestClient_SubscribeSentOnEveryConnect(t *testing.T) {
	subs := make(chan string, 4)
	var sessions atomic.Int64
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		n := sessions.Add(1)
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		subs <- string(data)
		if n == 1 {
			_ = c.CloseNow() // первая сессия рвётся — подписка должна уйти и во второй
			return
		}
		<-ctx.Done()
	})

	c := New(Config{
		Name:             "тест",
		URL:              wsURL(srv),
		Subscribe:        []byte(`{"type":"subscribe"}`),
		ReconnectInitial: 10 * time.Millisecond,
		ReconnectMax:     50 * time.Millisecond,
		OnMessage:        func(context.Context, []byte) error { return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	for i := 0; i < 2; i++ {
		select {
		case got := <-subs:
			if got != `{"type":"subscribe"}` {
				t.Fatalf("подписка №%d: %q", i+1, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("подписка №%d не пришла", i+1)
		}
	}
}

// Паника обработчика не должна валить процесс и рвать соединение: recover в
// callOnMessage превращает её в ошибку обработчика (по http-пути ту же роль
// играет per-request recover HTTP-сервера).
func TestClient_HandlerPanicSurvives(t *testing.T) {
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		_ = c.Write(ctx, websocket.MessageText, []byte(`взрыв`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`штатное`))
		<-ctx.Done()
	})

	gotCh := make(chan string, 8)
	c := New(Config{
		Name: "тест",
		URL:  wsURL(srv),
		OnMessage: func(_ context.Context, raw []byte) error {
			gotCh <- string(raw)
			if string(raw) == "взрыв" {
				panic("паника обработчика в тесте") // настоящая Go-паника, не ошибка
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	var got []string
	waitFor(t, "оба сообщения несмотря на панику обработчика", func() bool {
		for {
			select {
			case m := <-gotCh:
				got = append(got, m)
			default:
				return len(got) >= 2
			}
		}
	})
	st := c.Status()
	if !st.Connected || st.HandlerErrors != 1 || !strings.Contains(st.LastError, "паника обработчика") {
		t.Fatalf("ожидали живое соединение и зафиксированную панику: %+v", st)
	}
}

// «Заблокировано предохранителем» снимается, как только предохранитель открыт,
// даже если сервер недоступен: дальше причина простоя — ошибка подключения, и
// монитор должен показывать её, а не устаревшую блокировку.
func TestClient_BlockedReasonClearsWhenGateOpens(t *testing.T) {
	var open atomic.Bool
	c := New(Config{
		Name:             "тест",
		URL:              "ws://127.0.0.1:1", // порт закрыт: dial всегда падает
		ReconnectInitial: 10 * time.Millisecond,
		ReconnectMax:     20 * time.Millisecond,
		Gate: func() string {
			if open.Load() {
				return ""
			}
			return "сеть отключена предохранителем"
		},
		OnMessage: func(context.Context, []byte) error { return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	waitFor(t, "статус «заблокировано»", func() bool { return c.Status().BlockedReason != "" })
	open.Store(true)
	waitFor(t, "блокировка снята, причина — ошибка подключения", func() bool {
		st := c.Status()
		return st.BlockedReason == "" && st.LastError != ""
	})
}

// Отмена контекста вызвавшего не убивает общее соединение: в coder/websocket
// отмена ctx во время записи закрывает соединение целиком, поэтому Send
// проверяет отмену до записи и пишет на отвязанном контексте.
func TestClient_SendCancelledCallerKeepsConnection(t *testing.T) {
	srv := wsServer(t, func(ctx context.Context, c *websocket.Conn) {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	})
	c := New(Config{Name: "тест", URL: wsURL(srv), OnMessage: func(context.Context, []byte) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, "подключение", func() bool { return c.Status().Connected })

	cctx, ccancel := context.WithCancel(context.Background())
	ccancel() // вызвавший уже бросил ждать
	if err := c.Send(cctx, []byte("x")); err == nil {
		t.Fatal("Send с отменённым контекстом должен вернуть ошибку")
	}
	if err := c.Send(context.Background(), []byte("y")); err != nil {
		t.Fatalf("соединение не должно пострадать от отменённого вызвавшего: %v", err)
	}
	waitFor(t, "соединение живо", func() bool { return c.Status().Connected })
}
