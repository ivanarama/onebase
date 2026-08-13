package ui

// DSL-глобал ВебСокет (план 120B): отправка в исходящее WS-соединение приёмки
// и Подключён(). Тесты идут путём пользователя: настоящий сервер, настоящее
// соединение, DSL через buildDSLVars + interp.Run (правило #611).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
)

// runWSDSL исполняет тело процедуры на собранном сервере s.
func runWSDSL(t *testing.T, s *Server, body string) ([]string, error) {
	t.Helper()
	prog := mustParse(t, "Процедура Тест()\n"+body+"\nКонецПроцедуры")
	var proc *ast.ProcedureDecl
	for _, p := range prog.Procedures {
		proc = p
	}
	var msgs []string
	vars, txState := s.buildDSLVarsWithMessagesTx(context.Background(), nil, &msgs)
	defer interpreter.RollbackTxExecution(txState)
	err := s.interp.Run(proc, nil, vars)
	return msgs, err
}

func TestWebSocketDSL_SendAndConnected(t *testing.T) {
	fromClient := make(chan string, 1)
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) {
		_, data, err := c.Read(ctx)
		if err == nil {
			fromClient <- string(data)
		}
		<-ctx.Done()
	})
	s := newWSIntakeServer(t, wsTestURL(srv), true)
	s.ResyncWSIntakes()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c := s.wsIntakeClient("WSLead"); c != nil && c.Status().Connected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	msgs, err := runWSDSL(t, s, `
  Если ВебСокет.WSLead.Подключён() Тогда
    ВебСокет.WSLead.Отправить("привет");
    Сообщить("ушло");
  Иначе
    Сообщить("не подключено");
  КонецЕсли;`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "ушло" {
		t.Fatalf("сообщения: %v", msgs)
	}
	select {
	case got := <-fromClient:
		if got != "привет" {
			t.Fatalf("сервер получил %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("сервер не получил сообщение")
	}
}

// Соединение не запущено (supervisor не стартовал — например, headless-прогон):
// Отправить — перехватываемая ошибка, Подключён — Ложь, «тихой» отправки нет.
func TestWebSocketDSL_SendWithoutConnection(t *testing.T) {
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) { <-ctx.Done() })
	s := newWSIntakeServer(t, wsTestURL(srv), true) // ResyncWSIntakes намеренно не зовём

	msgs, err := runWSDSL(t, s, `
  Если ВебСокет.WSLead.Подключён() Тогда
    Сообщить("подключено");
  КонецЕсли;
  Попытка
    ВебСокет.WSLead.Отправить("x");
    Сообщить("ушло");
  Исключение
    Сообщить("поймано");
  КонецПопытки;`)
	if err != nil {
		t.Fatalf("DSL: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != "поймано" {
		t.Fatalf("ожидали перехваченную ошибку отправки: %v", msgs)
	}
}

// Аргумент не строка — ошибка сразу, а не тихая сериализация внутреннего
// представления объекта наружу.
func TestWebSocketDSL_SendRequiresString(t *testing.T) {
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) { <-ctx.Done() })
	s := newWSIntakeServer(t, wsTestURL(srv), true)
	s.ResyncWSIntakes()

	_, err := runWSDSL(t, s, `ВебСокет.WSLead.Отправить(123);`)
	if err == nil || !strings.Contains(err.Error(), "строк") {
		t.Fatalf("ожидали ошибку про строку, получили: %v", err)
	}
}

// Шлюз существует, но transport: http — понятная ошибка вместо «метод у
// Неопределено».
func TestWebSocketDSL_HTTPIntakeHint(t *testing.T) {
	srv := wsIntakeTestServer(t, func(ctx context.Context, n int64, c *websocket.Conn) { <-ctx.Done() })
	s := newWSIntakeServer(t, wsTestURL(srv), true)
	httpIn := &metadata.Intake{
		Name: "SiteLead", Transport: "http", Endpoint: "/hs/site/lead", Handler: "WSLead", Auth: "none",
		Idempotency: metadata.IntakeIdempotency{Key: "event_id"},
	}
	httpIn.Normalize()
	s.reg.LoadIntakes([]*metadata.Intake{wsTestIntake(t, wsTestURL(srv)), httpIn})

	_, err := runWSDSL(t, s, `ВебСокет.SiteLead.Отправить("x");`)
	if err == nil || !strings.Contains(err.Error(), "transport") {
		t.Fatalf("ожидали подсказку про transport, получили: %v", err)
	}
}
