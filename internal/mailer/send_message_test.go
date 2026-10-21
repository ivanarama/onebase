package mailer

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// fakeSMTP — подставной SMTP-сервер: запоминает вход, отправителя, получателей
// и тело письма. tlsCfg != nil — сразу TLS (порт 465), starttls != nil —
// сервер объявляет STARTTLS. mechs — объявляемые механизмы AUTH.
type fakeSMTP struct {
	addr     string
	mu       sync.Mutex
	authUser string
	authPass string
	authMech string
	from     string
	rcpts    []string
	data     string
	reject   bool
}

func startFakeSMTP(t *testing.T, tlsCfg, starttls *tls.Config, mechs string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	f := &fakeSMTP{addr: ln.Addr().String()}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, starttls, mechs)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn, starttls *tls.Config, mechs string) {
	defer conn.Close() //nolint:errcheck
	r := bufio.NewReader(conn)
	w := func(s string) { conn.Write([]byte(s + "\r\n")) } //nolint:errcheck
	w("220 fake ESMTP")
	tlsDone := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			w("250-fake")
			if starttls != nil && !tlsDone {
				w("250-STARTTLS")
			}
			w("250 AUTH " + mechs)
		case up == "STARTTLS":
			w("220 go ahead")
			tc := tls.Server(conn, starttls)
			if tc.Handshake() != nil {
				return
			}
			conn, r, tlsDone = tc, bufio.NewReader(tc), true
			w = func(s string) { tc.Write([]byte(s + "\r\n")) } //nolint:errcheck
		case strings.HasPrefix(up, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			f.mu.Lock()
			f.authMech = "PLAIN"
			if len(parts) == 3 {
				f.authUser, f.authPass = parts[1], parts[2]
			}
			f.mu.Unlock()
			if f.reject {
				w("535 5.7.8 authentication failed")
				continue
			}
			w("235 ok")
		case up == "AUTH LOGIN":
			w("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := r.ReadString('\n')
			w("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := r.ReadString('\n')
			uu, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			pp, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			f.mu.Lock()
			f.authMech, f.authUser, f.authPass = "LOGIN", string(uu), string(pp)
			f.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			f.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			f.mu.Lock()
			f.rcpts = append(f.rcpts, strings.Trim(line[len("RCPT TO:"):], "<> "))
			f.mu.Unlock()
			w("250 ok")
		case up == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued")
		case up == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func (f *fakeSMTP) profile(security string) *interpreter.EmailProfile {
	host, port, _ := net.SplitHostPort(f.addr)
	n, _ := strconv.Atoi(port)
	return &interpreter.EmailProfile{
		Host: host, Port: n, User: "robot@iceberg.test", Password: "Секрет-123",
		Security: security, Timeout: 5 * time.Second, From: "robot@iceberg.test", FromName: "Робот Айсберг-1С",
	}
}

func testMessage() interpreter.EmailMessage {
	return interpreter.EmailMessage{
		To: []string{"brigada1@iceberg.test", "Бригада 2 <brigada2@iceberg.test>"}, Cc: []string{"control@iceberg.test"},
		Subject: "Заявки на завтра", Text: "Список во вложении", HTML: "<p>Список <b>во вложении</b></p>",
		Files: []interpreter.EmailAttachment{{Name: "заявки.xlsx", MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", Data: []byte("PK\x03\x04xlsx")}},
	}
}

func trustCert(t *testing.T) *tls.Config {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	prev := tlsConfigFor
	tlsConfigFor = func(string) *tls.Config {
		return &tls.Config{ServerName: "example.com", RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	t.Cleanup(func() { tlsConfigFor = prev })
	return &tls.Config{Certificates: ts.TLS.Certificates}
}

// Профиль «Защита: НЕТ» + PLAIN: письмо с двумя получателями и копией, HTML и
// вложением уходит одним сеансом; отправитель и его имя — из профиля.
func TestSendMessage_ProfilePlainNoTLS(t *testing.T) {
	srv := startFakeSMTP(t, nil, nil, "PLAIN LOGIN")
	if err := New(Config{}).SendMessage(srv.profile("none"), testMessage()); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authMech != "PLAIN" || srv.authUser != "robot@iceberg.test" || srv.authPass != "Секрет-123" {
		t.Fatalf("вход: %s %s", srv.authMech, srv.authUser)
	}
	if srv.from != "robot@iceberg.test" {
		t.Fatalf("MAIL FROM: %q", srv.from)
	}
	if strings.Join(srv.rcpts, ",") != "brigada1@iceberg.test,brigada2@iceberg.test,control@iceberg.test" {
		t.Fatalf("получатели: %v", srv.rcpts)
	}
	for _, want := range []string{"Cc: control@iceberg.test", "multipart/mixed", "multipart/alternative", "text/html", `filename="заявки.xlsx"`, "Subject: Заявки на завтра"} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("в письме нет %q:\n%s", want, srv.data)
		}
	}
	if !strings.Contains(srv.data, "From: =?utf-8?") || !strings.Contains(srv.data, "<robot@iceberg.test>") {
		t.Errorf("имя отправителя не попало в From:\n%s", srv.data)
	}
}

// STARTTLS + LOGIN (сервер объявляет только LOGIN — как у 1С-ящиков).
func TestSendMessage_ProfileStartTLSLogin(t *testing.T) {
	serverTLS := trustCert(t)
	srv := startFakeSMTP(t, nil, serverTLS, "LOGIN")
	if err := New(Config{}).SendMessage(srv.profile("starttls"), testMessage()); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authMech != "LOGIN" || srv.authPass != "Секрет-123" {
		t.Fatalf("вход LOGIN не выполнен: %s", srv.authMech)
	}
}

// Сразу TLS (порт 465).
func TestSendMessage_ProfileTLS(t *testing.T) {
	serverTLS := trustCert(t)
	srv := startFakeSMTP(t, serverTLS, nil, "PLAIN")
	if err := New(Config{}).SendMessage(srv.profile("tls"), testMessage()); err != nil {
		t.Fatal(err)
	}
}

// Сервер без STARTTLS при Защита STARTTLS — пароль не отправляется; отказ входа
// не раскрывает пароль в тексте ошибки.
func TestSendMessage_SecurityAndPasswordHygiene(t *testing.T) {
	srv := startFakeSMTP(t, nil, nil, "PLAIN")
	err := New(Config{}).SendMessage(srv.profile("starttls"), testMessage())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("ожидался отказ без STARTTLS, получено %v", err)
	}
	srv.mu.Lock()
	sentPass := srv.authPass
	srv.mu.Unlock()
	if sentPass != "" {
		t.Fatal("пароль ушёл по незащищённому каналу")
	}
	bad := startFakeSMTP(t, nil, nil, "PLAIN")
	bad.reject = true
	err = New(Config{}).SendMessage(bad.profile("none"), testMessage())
	if err == nil {
		t.Fatal("ожидался отказ входа")
	}
	if strings.Contains(err.Error(), "Секрет-123") {
		t.Fatalf("пароль в тексте ошибки: %v", err)
	}
}

// Без профиля и без секции email — понятная ошибка, а не попытка соединения.
func TestSendMessage_NoProfileNoConfig(t *testing.T) {
	err := New(Config{}).SendMessage(nil, testMessage())
	if err == nil || !strings.Contains(err.Error(), "не настроен") {
		t.Fatalf("ожидалась ошибка настройки, получено %v", err)
	}
}
