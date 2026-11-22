package interpreter

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// Письмо партнёра: multipart/alternative, тема в KOI8-R (RFC 2047), простой
// текст в windows-1251 quoted-printable, HTML в UTF-8 base64, вложение.
const mailPartner = "From: =?UTF-8?B?0J/QsNGA0YLQvdGR0YAg0JzQodCa?= <partner@example.ru>\r\n" +
	"To: tasks@example.ru\r\n" +
	"Subject: =?KOI8-R?B?8sXNz87UINrB0dfLySAxNw==?=\r\n" +
	"Date: Tue, 20 Oct 2026 10:15:00 +0300\r\n" +
	"Message-ID: <partner-17@example.ru>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"OUT\"\r\n" +
	"\r\n" +
	"--OUT\r\n" +
	"Content-Type: multipart/alternative; boundary=\"ALT\"\r\n" +
	"\r\n" +
	"--ALT\r\n" +
	"Content-Type: text/plain; charset=windows-1251\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"=CF=F0=E8=E2=E5=F2, =E7=E0=FF=E2=EA=E0 17\r\n" +
	"--ALT\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"PGI+0JfQsNGP0LLQutCwIDE3PC9iPg==\r\n" +
	"--ALT--\r\n" +
	"--OUT\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Disposition: attachment; filename=\"a.txt\"\r\n" +
	"\r\n" +
	"вложение\r\n" +
	"--OUT--\r\n"

const mailPlain = "From: client@example.ru\r\n" +
	"Subject: plain\r\n" +
	"Message-ID: <plain-1@example.ru>\r\n" +
	"\r\n" +
	".leading dot line\r\n" +
	"second line\r\n"

type fakeMailServer struct {
	addr     string
	starttls *tls.Config // не nil — сервер умеет STLS/STARTTLS
	mu       sync.Mutex
	commands []string
}

func (f *fakeMailServer) record(cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
}

func (f *fakeMailServer) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

// startFakeMail запускает подставной сервер протокола proto ("pop3"/"imap").
// tlsCfg != nil — сервер сразу TLS (POP3S/IMAPS).
func startFakeMail(t *testing.T, proto string, tlsCfg *tls.Config, messages []string, starttls ...*tls.Config) *fakeMailServer {
	return startFakeMailWithValidity(t, proto, tlsCfg, messages, []uint32{1001}, starttls...)
}

// validities задают UIDVALIDITY последовательных соединений одного ящика.
func startFakeMailWithValidity(t *testing.T, proto string, tlsCfg *tls.Config, messages []string, validities []uint32, starttls ...*tls.Config) *fakeMailServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f := &fakeMailServer{addr: ln.Addr().String()}
	if len(starttls) > 0 {
		f.starttls = starttls[0]
	}
	go func() {
		session := 0
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			validity := validities[min(session, len(validities)-1)]
			session++
			go func() {
				defer conn.Close() //nolint:errcheck
				if proto == "pop3" {
					f.servePOP3(conn, messages)
				} else {
					f.serveIMAP(conn, messages, validity)
				}
			}()
		}
	}()
	return f
}

func (f *fakeMailServer) servePOP3(conn net.Conn, messages []string) {
	r := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	w("+OK fake POP3")
	upgrade := func() bool {
		tc := tls.Server(conn, f.starttls)
		if tc.Handshake() != nil {
			return false
		}
		conn, r = tc, bufio.NewReader(tc)
		return true
	}
	authed := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.record(line)
		cmd := strings.ToUpper(strings.Fields(line + " x")[0])
		switch cmd {
		case "STLS":
			if f.starttls == nil {
				w("-ERR no TLS")
				continue
			}
			w("+OK begin TLS")
			if !upgrade() {
				return
			}
		case "USER":
			w("+OK")
		case "PASS":
			if strings.TrimPrefix(line, "PASS ") == "секрет-1" {
				authed = true
				w("+OK logged in")
			} else {
				w("-ERR [AUTH] bad password секрет-неверный")
			}
		case "UIDL", "LIST":
			if !authed {
				w("-ERR not authenticated")
				continue
			}
			w("+OK")
			for i, m := range messages {
				if cmd == "UIDL" {
					w(fmt.Sprintf("%d uid-%d", i+1, i+1))
				} else {
					w(fmt.Sprintf("%d %d", i+1, len(m)))
				}
			}
			w(".")
		case "RETR":
			n, _ := strconv.Atoi(strings.Fields(line)[1])
			w("+OK")
			for _, l := range strings.Split(strings.TrimSuffix(messages[n-1], "\r\n"), "\r\n") {
				if strings.HasPrefix(l, ".") {
					l = "." + l
				}
				w(l)
			}
			w(".")
		case "QUIT":
			w("+OK bye")
			return
		default:
			w("-ERR unknown")
		}
	}
}

func (f *fakeMailServer) serveIMAP(conn net.Conn, messages []string, validity uint32) {
	r := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	w("* OK fake IMAP ready")
	upgrade := func() bool {
		tc := tls.Server(conn, f.starttls)
		if tc.Handshake() != nil {
			return false
		}
		conn, r = tc, bufio.NewReader(tc)
		return true
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		// Литерал {n}: подтверждаем и дочитываем байты.
		for strings.HasSuffix(line, "}") && strings.Contains(line, "{") {
			n, _ := strconv.Atoi(line[strings.LastIndex(line, "{")+1 : len(line)-1])
			w("+ go ahead")
			buf := make([]byte, n)
			if _, err := r.Read(buf); err != nil {
				return
			}
			rest, _ := r.ReadString('\n')
			line = line[:strings.LastIndex(line, "{")] + `"` + string(buf) + `"` + strings.TrimRight(rest, "\r\n")
		}
		f.record(line)
		parts := strings.SplitN(line, " ", 3)
		tag, cmd := parts[0], strings.ToUpper(parts[1])
		args := ""
		if len(parts) > 2 {
			args = parts[2]
		}
		switch {
		case cmd == "STARTTLS":
			w(tag + " OK begin TLS")
			if !upgrade() {
				return
			}
		case cmd == "LOGIN":
			if strings.Contains(args, `"секрет-1"`) {
				w(tag + " OK logged in")
			} else {
				w(tag + ` NO [AUTHENTICATIONFAILED] bad password секрет-неверный`)
			}
		case cmd == "SELECT":
			if validity != 0 {
				w(fmt.Sprintf("* OK [UIDVALIDITY %d] UIDs valid", validity))
			}
			w(fmt.Sprintf("* %d EXISTS", len(messages)))
			w(tag + " OK [READ-WRITE] selected")
		case cmd == "UID" && strings.HasPrefix(strings.ToUpper(args), "SEARCH"):
			ids := []string{}
			first := uint64(1)
			if fields := strings.Fields(args); len(fields) == 4 && fields[2] == "UID" {
				first, _ = strconv.ParseUint(strings.SplitN(fields[3], ":", 2)[0], 10, 32)
			}
			for i := range messages {
				if uint64(100+i) >= first {
					ids = append(ids, strconv.Itoa(100+i))
				}
			}
			w("* SEARCH " + strings.Join(ids, " "))
			w(tag + " OK search done")
		case cmd == "UID" && strings.HasPrefix(strings.ToUpper(args), "FETCH"):
			f2 := strings.Fields(args)
			uid, _ := strconv.Atoi(f2[1])
			m := messages[uid-100]
			if strings.Contains(args, "RFC822.SIZE") {
				w(fmt.Sprintf("* %d FETCH (UID %d RFC822.SIZE %d)", uid-99, uid, len(m)))
			} else {
				_, _ = fmt.Fprintf(conn, "* %d FETCH (UID %d BODY[] {%d}\r\n%s)\r\n", uid-99, uid, len(m), m)
			}
			w(tag + " OK fetch done")
		case cmd == "UID" && strings.HasPrefix(strings.ToUpper(args), "STORE"):
			w(tag + " OK store done")
		case cmd == "LOGOUT":
			w("* BYE")
			w(tag + " OK bye")
			return
		default:
			w(tag + " BAD unknown")
		}
	}
}

func mailTestProc(t *testing.T, src string) *ast.ProcedureDecl {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	return prog.Procedures[0]
}

const receiveSrc = `
Функция Тест()
	Профиль = Новый Структура("Протокол, Сервер, Порт, Пользователь, Пароль, Защита, Таймаут", Протокол, Сервер, Порт, "tasks", Пароль, Защита, 5);
	П = Новый Структура("Пропустить, Максимум, ОтметитьПрочитанными", Пропустить, Максимум, Отметить);
	Итог = "";
	Для Каждого Письмо Из ПолучитьПисьма(Профиль, П) Цикл
		Итог = Итог + Письмо.Ид + "|" + Письмо.MessageID + "|" + Письмо.Отправитель + "|" + Письмо.ОтправительИмя + "|"
			+ Письмо.Тема + "|" + Письмо.ТекстПростой + "|" + Письмо.ТекстHTML + "|" + ?(Письмо.Дата = Неопределено, "-", Формат(Письмо.Дата, "ДФ=dd.MM.yyyy")) + ";";
	КонецЦикла;
	Возврат Итог;
КонецФункции
`

func runReceive(t *testing.T, vars map[string]any) (any, error) {
	t.Helper()
	all := NewEmailFunctions(nil, nil)
	for k, v := range vars {
		all[k] = v
	}
	var result any
	err := New().RunWithResult(mailTestProc(t, receiveSrc), nil, &result, all)
	return result, err
}

func mailVars(proto, addr, security, password string, skip []any, max float64, mark bool) map[string]any {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return map[string]any{
		"Протокол": proto, "Сервер": host, "Порт": float64(p), "Защита": security, "Пароль": password,
		"Пропустить": NewArray(skip), "Максимум": max, "Отметить": mark,
	}
}

func TestReceiveMail_POP3AndIMAP(t *testing.T) {
	for _, proto := range []string{"POP3", "IMAP"} {
		t.Run(proto, func(t *testing.T) {
			srv := startFakeMail(t, strings.ToLower(proto), nil, []string{mailPartner, mailPlain})
			got, err := runReceive(t, mailVars(proto, srv.addr, "НЕТ", "секрет-1", nil, 0, false))
			if err != nil {
				t.Fatal(err)
			}
			res := fmt.Sprint(got)
			first := strings.SplitN(res, ";", 2)[0]
			for _, want := range []string{"partner-17@example.ru", "partner@example.ru", "Партнёр МСК",
				"Ремонт заявки 17", "Привет, заявка 17", "<b>Заявка 17</b>", "20.10.2026"} {
				if !strings.Contains(first, want) {
					t.Fatalf("в первом письме нет %q: %s", want, first)
				}
			}
			if strings.Contains(res, "вложение") {
				t.Fatalf("вложение попало в текст: %s", res)
			}
			if !strings.Contains(res, ".leading dot line") {
				t.Fatalf("точка-заполнитель не снята или строка потеряна: %s", res)
			}
			cmds := srv.all()
			if strings.Contains(strings.ToUpper(cmds), "DELE") || strings.Contains(strings.ToUpper(cmds), "EXPUNGE") {
				t.Fatalf("письма с сервера удаляться не должны:\n%s", cmds)
			}
			if proto == "IMAP" {
				if !strings.Contains(cmds, "BODY.PEEK[]") || strings.Contains(cmds, "STORE") {
					t.Fatalf("без ОтметитьПрочитанными письмо читается через PEEK и не помечается:\n%s", cmds)
				}
			}
		})
	}
}

func TestReceiveMail_SkipMaxAndMarkSeen(t *testing.T) {
	pop := startFakeMail(t, "pop3", nil, []string{mailPartner, mailPlain})
	got, err := runReceive(t, mailVars("POP3", pop.addr, "НЕТ", "секрет-1", []any{"uid-1"}, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	if res := fmt.Sprint(got); strings.Contains(res, "uid-1|") || !strings.HasPrefix(res, "uid-2|") {
		t.Fatalf("Пропустить не сработал: %s", res)
	}
	imap := startFakeMail(t, "imap", nil, []string{mailPartner, mailPlain})
	got, err = runReceive(t, mailVars("IMAP", imap.addr, "НЕТ", "секрет-1", nil, 1, true))
	if err != nil {
		t.Fatal(err)
	}
	if res := fmt.Sprint(got); strings.Count(res, ";") != 1 || !strings.HasPrefix(res, "1001:100|") {
		t.Fatalf("Максимум=1 вернул не одно письмо: %s", res)
	}
	if cmds := imap.all(); !strings.Contains(cmds, `UID STORE 100 +FLAGS (\Seen)`) || strings.Contains(cmds, "STORE 101") {
		t.Fatalf("ОтметитьПрочитанными помечает только полученное:\n%s", cmds)
	}
}

func TestReceiveMail_AuthFailureHidesPassword(t *testing.T) {
	for _, proto := range []string{"POP3", "IMAP"} {
		srv := startFakeMail(t, strings.ToLower(proto), nil, []string{mailPlain})
		_, err := runReceive(t, mailVars(proto, srv.addr, "НЕТ", "секрет-неверный", nil, 0, false))
		if err == nil {
			t.Fatalf("%s: неверный пароль обязан дать ошибку", proto)
		}
		if strings.Contains(err.Error(), "секрет") {
			t.Fatalf("%s: пароль попал в текст ошибки: %v", proto, err)
		}
		if !strings.Contains(err.Error(), "отклонил вход пользователя tasks") {
			t.Fatalf("%s: непонятная ошибка входа: %v", proto, err)
		}
	}
}

func TestReceiveMail_TLS(t *testing.T) {
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	defer ts.Close()
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	prev := mailTLSConfig
	mailTLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: "example.com", RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	defer func() { mailTLSConfig = prev }()
	for _, proto := range []string{"POP3", "IMAP"} {
		srv := startFakeMail(t, strings.ToLower(proto), &tls.Config{Certificates: ts.TLS.Certificates}, []string{mailPlain})
		got, err := runReceive(t, mailVars(proto, srv.addr, "TLS", "секрет-1", nil, 0, false))
		if err != nil {
			t.Fatalf("%s по TLS: %v", proto, err)
		}
		if !strings.Contains(fmt.Sprint(got), "plain-1@example.ru") {
			t.Fatalf("%s по TLS: письмо не получено: %v", proto, got)
		}
	}
	// STARTTLS: открытое соединение переходит на TLS до входа.
	for _, proto := range []string{"POP3", "IMAP"} {
		srv := startFakeMail(t, strings.ToLower(proto), nil, []string{mailPlain}, &tls.Config{Certificates: ts.TLS.Certificates})
		got, err := runReceive(t, mailVars(proto, srv.addr, "STARTTLS", "секрет-1", nil, 0, false))
		if err != nil {
			t.Fatalf("%s STARTTLS: %v", proto, err)
		}
		if !strings.Contains(fmt.Sprint(got), "plain-1@example.ru") {
			t.Fatalf("%s STARTTLS: письмо не получено: %v", proto, got)
		}
		cmds := srv.all()
		tlsAt := strings.Index(cmds, "STLS")
		authAt := strings.Index(cmds, "PASS")
		if proto == "IMAP" {
			tlsAt, authAt = strings.Index(cmds, "STARTTLS"), strings.Index(cmds, "LOGIN")
		}
		if tlsAt < 0 || authAt < tlsAt {
			t.Fatalf("%s: вход до перехода на TLS:\n%s", proto, cmds)
		}
	}
	// Без доверия к сертификату соединение не устанавливается.
	mailTLSConfig = prev
	srv := startFakeMail(t, "imap", &tls.Config{Certificates: ts.TLS.Certificates}, []string{mailPlain})
	if _, err := runReceive(t, mailVars("IMAP", srv.addr, "TLS", "секрет-1", nil, 0, false)); err == nil {
		t.Fatal("недоверенный сертификат обязан дать ошибку подключения")
	}
}

func TestReceiveMail_DeniedBySandbox(t *testing.T) {
	srv := startFakeMail(t, "pop3", nil, []string{mailPlain})
	vars := mailVars("POP3", srv.addr, "НЕТ", "секрет-1", nil, 0, false)
	for k, v := range NewEmailFunctions(nil, nil) {
		vars[k] = v
	}
	var result any
	err := New().RunSandboxed(mailTestProc(t, receiveSrc), nil, SandboxProfile{DenyNet: true}, &result, vars)
	if err == nil || !strings.Contains(err.Error(), "сеть запрещена") {
		t.Fatalf("песочница без сети обязана запретить ПолучитьПисьма: %v", err)
	}
	if srv.all() != "" {
		t.Fatalf("запрет обязан сработать до подключения: %s", srv.all())
	}
}

func TestReceiveMail_MaxSizeSkipsLargeMessages(t *testing.T) {
	for _, proto := range []string{"POP3", "IMAP"} {
		srv := startFakeMail(t, strings.ToLower(proto), nil, []string{mailPartner, mailPlain})
		host, port, _ := net.SplitHostPort(srv.addr)
		p, _ := strconv.Atoi(port)
		src := `
Функция Тест()
	Профиль = Новый Структура("Протокол, Сервер, Порт, Пользователь, Пароль, Защита", Протокол, Сервер, Порт, "tasks", "секрет-1", "НЕТ");
	Итог = "";
	Для Каждого Письмо Из ПолучитьПисьма(Профиль, Новый Структура("МаксимальныйРазмер", 300)) Цикл
		Итог = Итог + Письмо.MessageID + ";";
	КонецЦикла;
	Возврат Итог;
КонецФункции
`
		vars := NewEmailFunctions(nil, nil)
		vars["Протокол"], vars["Сервер"], vars["Порт"] = proto, host, float64(p)
		var result any
		if err := New().RunWithResult(mailTestProc(t, src), nil, &result, vars); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(result) != "plain-1@example.ru;" {
			t.Fatalf("%s: письмо больше предела должно быть пропущено: %v", proto, result)
		}
	}
}

// Одна папка и один UID: после смены UIDVALIDITY сохранённый Ид не должен
// скрывать новое письмо. Стабильная UIDVALIDITY по-прежнему исключает повтор.
func TestReceiveMail_IMAPUIDValidity(t *testing.T) {
	srv := startFakeMailWithValidity(t, "imap", nil, []string{mailPlain}, []uint32{1001, 1001, 1002})
	got, err := runReceive(t, mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", nil, 1, false))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.SplitN(fmt.Sprint(got), "|", 2)[0]
	if id != "1001:100" {
		t.Fatalf("Ид = %q", id)
	}
	got, err = runReceive(t, mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", []any{id}, 1, false))
	if err != nil || got != "" {
		t.Fatalf("повтор: %v, %v", got, err)
	}
	got, err = runReceive(t, mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", []any{id}, 1, false))
	if err != nil || !strings.HasPrefix(fmt.Sprint(got), "1002:100|") {
		t.Fatalf("новая UIDVALIDITY потеряла письмо: %v, %v", got, err)
	}
}

func TestReceiveMail_IMAPRequiresUIDValidity(t *testing.T) {
	srv := startFakeMailWithValidity(t, "imap", nil, []string{mailPlain}, []uint32{0})
	_, err := runReceive(t, mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", nil, 1, false))
	if err == nil || !strings.Contains(err.Error(), "UIDVALIDITY") {
		t.Fatalf("ошибка = %v", err)
	}
	if strings.Contains(srv.all(), "UID FETCH") {
		t.Fatal("чтение без стабильного Ид")
	}
}

func TestReceiveMail_MultipartAttachmentDoesNotSupplyBody(t *testing.T) {
	raw := "From: client@example.ru\r\nSubject: attachment\r\n" +
		"Content-Type: multipart/mixed; boundary=OUT\r\n\r\n" +
		"--OUT\r\nContent-Type: multipart/alternative; boundary=ATT\r\n" +
		"Content-Disposition: attachment; filename=part.mime\r\n\r\n" +
		"--ATT\r\nContent-Type: text/html\r\n\r\n<p>ATTACHMENT HTML</p>\r\n" +
		"--ATT\r\nContent-Type: text/plain\r\n\r\nATTACHMENT TEXT\r\n--ATT--\r\n" +
		"--OUT\r\nContent-Type: text/plain\r\n\r\nMAIN BODY\r\n--OUT--\r\n"
	for _, proto := range []string{"POP3", "IMAP"} {
		t.Run(proto, func(t *testing.T) {
			srv := startFakeMail(t, strings.ToLower(proto), nil, []string{raw})
			got, err := runReceive(t, mailVars(proto, srv.addr, "НЕТ", "секрет-1", nil, 1, false))
			if err != nil {
				t.Fatal(err)
			}
			if text := fmt.Sprint(got); !strings.Contains(text, "MAIN BODY") || strings.Contains(text, "ATTACHMENT") {
				t.Fatalf("тело письма = %s", text)
			}
		})
	}
}

func TestReceiveMail_IMAPLargeInboxMaxOne(t *testing.T) {
	messages := make([]string, 16000)
	for i := range messages {
		messages[i] = mailPlain
	}
	// SEARCH >64 КиБ, включая UID, разрезанный границей буфера.
	srv := startFakeMail(t, "imap", nil, messages)
	for _, skipCount := range []int{0, 15500} {
		t.Run(strconv.Itoa(skipCount), func(t *testing.T) {
			skip := make([]any, skipCount)
			for i := range skip {
				skip[i] = fmt.Sprintf("1001:%d", 100+i)
			}
			got, err := runReceive(t, mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", skip, 1, false))
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("1001:%d|", 100+skipCount)
			if text := fmt.Sprint(got); !strings.HasPrefix(text, want) || strings.Count(text, ";") != 1 {
				t.Fatalf("Максимум=1: %s", text)
			}
		})
	}
}

func TestReceiveMail_IMAPContinuesAfterOversizeBatch(t *testing.T) {
	srv := startFakeMail(t, "imap", nil, []string{mailPartner, mailPlain})
	vars := mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", nil, 1, false)
	// Подмена только исходника DSL: всё чтение идёт через публичную функцию.
	src := strings.Replace(receiveSrc, "Итог = \"\";", "П.Вставить(\"МаксимальныйРазмер\", 200); Итог = \"\";", 1)
	all := NewEmailFunctions(nil, nil)
	for k, v := range vars {
		all[k] = v
	}
	var got any
	if err := New().RunWithResult(mailTestProc(t, src), nil, &got, all); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fmt.Sprint(got), "1001:101|") {
		t.Fatalf("после большого письма: %v", got)
	}
	if !strings.Contains(srv.all(), "UID SEARCH UID 101:4294967295") {
		t.Fatal("нет продолжения SEARCH")
	}
}
