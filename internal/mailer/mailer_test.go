package mailer_test

import (
	"bufio"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSMTP accepts one connection, records the DATA payload, then closes.
type fakeSMTP struct {
	ln      net.Listener
	mu      sync.Mutex
	payload string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &fakeSMTP{ln: ln}
	go f.serve()
	return f
}

func (f *fakeSMTP) Addr() string { return f.ln.Addr().String() }

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	send := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
	recv := func() string { line, _ := r.ReadString('\n'); return strings.TrimSpace(line) }

	send("220 fakesmtp ESMTP")
	recv() // EHLO / HELO
	send("250 OK")
	recv() // MAIL FROM
	send("250 OK")
	recv() // RCPT TO
	send("250 OK")
	recv() // DATA
	send("354 Start input")

	var body strings.Builder
	for {
		line, _ := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "." {
			break
		}
		body.WriteString(line + "\n")
	}
	f.mu.Lock()
	f.payload = body.String()
	f.mu.Unlock()
	send("250 OK")
	recv() // QUIT
	send("221 Bye")
}

func (f *fakeSMTP) Payload() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.payload
}

func TestSend_PlainText(t *testing.T) {
	srv := startFakeSMTP(t)
	defer srv.ln.Close()

	host, portStr, _ := net.SplitHostPort(srv.Addr())
	port := 0
	_, err := net.LookupPort("tcp", portStr)
	require.NoError(t, err)
	_, _ = host, port

	// Parse port as int
	var p int
	for _, b := range portStr {
		p = p*10 + int(b-'0')
	}

	m := mailer.New(mailer.Config{
		SMTPHost:    "127.0.0.1",
		SMTPPort:    p,
		FromAddress: "from@example.com",
	})

	err = m.Send("to@example.com", "Тест", "Привет мир", "")
	require.NoError(t, err)

	payload := srv.Payload()
	assert.Contains(t, payload, "To: to@example.com")
	assert.Contains(t, payload, "Subject: Тест")
	assert.Contains(t, payload, "Привет мир")
}

func TestSend_HTML(t *testing.T) {
	srv := startFakeSMTP(t)
	defer srv.ln.Close()

	_, portStr, _ := net.SplitHostPort(srv.Addr())
	var p int
	for _, b := range portStr {
		p = p*10 + int(b-'0')
	}

	m := mailer.New(mailer.Config{
		SMTPHost:    "127.0.0.1",
		SMTPPort:    p,
		FromName:    "Мой Склад",
		FromAddress: "noreply@example.com",
	})

	err := m.Send("client@example.com", "Заказ принят", "Текст", "<p>Текст</p>")
	require.NoError(t, err)

	payload := srv.Payload()
	assert.Contains(t, payload, "multipart/alternative")
	assert.Contains(t, payload, "text/html")
	assert.Contains(t, payload, "<p>Текст</p>")
	message, err := mail.ReadMessage(strings.NewReader(payload))
	require.NoError(t, err)
	from, err := mail.ParseAddress(message.Header.Get("From"))
	require.NoError(t, err)
	assert.Equal(t, "Мой Склад", from.Name)
}

func TestConfigured_False(t *testing.T) {
	m := mailer.New(mailer.Config{})
	assert.False(t, m.Configured())
	err := m.Send("x@y.com", "s", "b", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "не настроен")
}

func TestSend_RejectsHeaderInjectionBeforeSMTP(t *testing.T) {
	tests := []struct {
		name    string
		cfg     mailer.Config
		to      string
		subject string
	}{
		{
			name:    "recipient",
			cfg:     mailer.Config{SMTPHost: "127.0.0.1", SMTPPort: 1, FromAddress: "from@example.com"},
			to:      "victim@example.com\r\nBcc: attacker@example.com",
			subject: "ok",
		},
		{
			name:    "subject",
			cfg:     mailer.Config{SMTPHost: "127.0.0.1", SMTPPort: 1, FromAddress: "from@example.com"},
			to:      "victim@example.com",
			subject: "ok\r\nBcc: attacker@example.com",
		},
		{
			name:    "from name",
			cfg:     mailer.Config{SMTPHost: "127.0.0.1", SMTPPort: 1, FromAddress: "from@example.com", FromName: "Sender\r\nBcc: attacker@example.com"},
			to:      "victim@example.com",
			subject: "ok",
		},
		{
			name:    "from address",
			cfg:     mailer.Config{SMTPHost: "127.0.0.1", SMTPPort: 1, FromAddress: "from@example.com\r\nBcc: attacker@example.com"},
			to:      "victim@example.com",
			subject: "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mailer.New(tt.cfg).Send(tt.to, tt.subject, "body", "")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "connect")
		})
	}
}
