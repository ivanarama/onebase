package mailer

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func TestSMTPFeatureExampleRealSender(t *testing.T) {
	doc, err := os.ReadFile("../../docs/features.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "## Отправка почты с профилем SMTP, HTML и вложениями\n")
	if !ok {
		t.Fatal("SMTP feature section is missing")
	}
	_, section, ok = strings.Cut(section, "```bsl\n")
	if !ok {
		t.Fatal("SMTP example is missing")
	}
	example, _, ok := strings.Cut(section, "\n```")
	if !ok {
		t.Fatal("SMTP example is not closed")
	}

	srv := startFakeSMTP(t, nil, trustCert(t), "PLAIN")
	host, port, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	// Adapt only the endpoint; keep the published login, sender and STARTTLS.
	const endpoint = `"smtp.example.ru", 587`
	if strings.Count(example, endpoint) != 1 {
		t.Fatal("SMTP example endpoint changed")
	}
	example = strings.Replace(example, endpoint, fmt.Sprintf("%q, %s", host, port), 1)
	src := `Процедура Тест()
Пароль = "Секрет";
ТекстHTML = "<p>Заявки</p>";
Данные = Новый Массив;
Строка = Новый Массив;
Строка.Добавить("Номер");
Данные.Добавить(Строка);
` + example + "\nКонецПроцедуры"
	program, err := parser.New(lexer.New(src, "features-smtp.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	if len(program.Procedures) != 1 {
		t.Fatal("expected one example procedure")
	}
	var result any
	if err := interpreter.New().RunWithResult(program.Procedures[0], nil, &result,
		interpreter.NewEmailFunctions(New(Config{}), nil)); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authUser != "robot" || srv.from != "robot@example.ru" {
		t.Fatalf("SMTP login/sender: %q / %q", srv.authUser, srv.from)
	}
	if got := strings.Join(srv.rcpts, ","); got != "b1@example.ru,b2@example.ru,control@example.ru" {
		t.Fatalf("SMTP recipients: %s", got)
	}
	if !strings.Contains(srv.data, "multipart/mixed") ||
		!strings.Contains(srv.data, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet") {
		t.Fatal("SMTP example did not deliver its XLSX attachment")
	}
}
