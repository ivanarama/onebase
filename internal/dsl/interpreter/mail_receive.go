package interpreter

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// ПолучитьПисьма(Профиль[, Параметры]) — чтение почтового ящика по POP3 или
// IMAP (как ИнтернетПочта в 1С). Письма с сервера не удаляются никогда:
// «новизну» ведёт вызывающий код по Ид (UIDL у POP3, UID у IMAP) и передаёт
// уже полученные в Параметры.Пропустить.
const (
	mailReceiveDefaultMax  = 50
	mailReceiveMaxMessages = 500
	mailReceiveDefaultSize = 10 << 20
	mailReceiveMaxSize     = 50 << 20
	mailReceiveDefaultWait = 30 * time.Second
	mailReceiveMaxLine     = 64 << 10
)

type mailProfile struct {
	protocol string // "pop3" | "imap"
	host     string
	port     int
	user     string
	password string
	security string // "tls" | "starttls" | "none"
	timeout  time.Duration
	folder   string
}

type mailReceiveOptions struct {
	skip     map[string]bool
	max      int
	maxSize  int64
	markSeen bool
}

// mailTLSConfig — конфигурация TLS соединения с почтовым сервером. Сертификат
// проверяется всегда; переменная — только чтобы тест мог подставить свой
// корневой сертификат подставного сервера.
var mailTLSConfig = func(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func newReceiveMailBuiltin(guard NetGuard) BuiltinFunc {
	return func(args []any, file string, line int) (any, error) {
		checkNet(guard)
		if len(args) < 1 {
			panic(userError{Msg: "ПолучитьПисьма(Профиль[, Параметры]): не указан профиль", File: file, Line: line})
		}
		profile, err := parseMailProfile(args[0])
		if err != nil {
			panic(userError{Msg: "ПолучитьПисьма: " + err.Error(), File: file, Line: line})
		}
		var opts mailReceiveOptions
		if len(args) >= 2 {
			opts, err = parseMailReceiveOptions(args[1])
		} else {
			opts, err = parseMailReceiveOptions(nil)
		}
		if err != nil {
			panic(userError{Msg: "ПолучитьПисьма: " + err.Error(), File: file, Line: line})
		}
		var mails []receivedMail
		switch profile.protocol {
		case "pop3":
			mails, err = receivePOP3(profile, opts)
		default:
			mails, err = receiveIMAP(profile, opts)
		}
		if err != nil {
			// Текст ошибки строится только из адреса сервера, логина и ответа
			// сервера на команду — пароль в него не попадает (см. команды ниже).
			panic(userError{Msg: "ПолучитьПисьма: " + profile.host + ": " + err.Error(), File: file, Line: line})
		}
		items := make([]any, 0, len(mails))
		for _, m := range mails {
			items = append(items, mailToStruct(m))
		}
		return NewArray(items), nil
	}
}

func mailToStruct(m receivedMail) *Struct {
	s := &Struct{vals: map[string]any{}}
	set := func(k string, v any) {
		kl := strings.ToLower(k)
		s.keys = append(s.keys, kl)
		s.vals[kl] = v
	}
	set("Ид", m.ID)
	set("MessageID", m.MessageID)
	set("Отправитель", m.From)
	set("ОтправительИмя", m.FromName)
	set("Тема", m.Subject)
	if m.HasDate {
		set("Дата", m.Date)
	} else {
		set("Дата", nil)
	}
	set("ТекстHTML", m.HTML)
	set("ТекстПростой", m.Text)
	return s
}

func structStr(s *Struct, names ...string) string {
	for _, n := range names {
		if v := s.Get(n); v != nil {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
	}
	return ""
}

func structNum(s *Struct, names ...string) (float64, bool) {
	for _, n := range names {
		if v := s.Get(n); v != nil {
			if f, ok := toFloat(v); ok {
				return f, true
			}
			if f, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprintf("%v", v)), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

func parseMailProfile(v any) (mailProfile, error) {
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return mailProfile{}, errors.New("профиль — Структура {Протокол, Сервер, Порт, Пользователь, Пароль, Защита, Таймаут, Папка}")
	}
	p := mailProfile{
		host:     structStr(s, "сервер", "server", "host"),
		user:     structStr(s, "пользователь", "user", "login"),
		password: fmt.Sprintf("%v", firstNonNil(s.Get("пароль"), s.Get("password"))),
		folder:   structStr(s, "папка", "folder"),
		timeout:  mailReceiveDefaultWait,
	}
	if p.password == "<nil>" {
		p.password = ""
	}
	switch strings.ToLower(structStr(s, "протокол", "protocol")) {
	case "pop3", "pop":
		p.protocol = "pop3"
	case "imap", "imap4":
		p.protocol = "imap"
	case "":
		return p, errors.New("не указан Протокол (POP3 или IMAP)")
	default:
		return p, fmt.Errorf("неизвестный Протокол %q (POP3 или IMAP)", structStr(s, "протокол", "protocol"))
	}
	switch strings.ToLower(structStr(s, "защита", "security", "tls")) {
	case "", "tls", "ssl", "истина", "true":
		p.security = "tls"
	case "starttls", "stls":
		p.security = "starttls"
	case "нет", "none", "ложь", "false":
		p.security = "none"
	default:
		return p, fmt.Errorf("неизвестная Защита %q (TLS, STARTTLS или НЕТ)", structStr(s, "защита", "security"))
	}
	if p.host == "" {
		return p, errors.New("не указан Сервер")
	}
	if p.user == "" {
		return p, errors.New("не указан Пользователь")
	}
	if strings.ContainsAny(p.user+p.password+p.folder, "\r\n") {
		return p, errors.New("перевод строки в Пользователь, Пароль или Папка недопустим")
	}
	if port, ok := structNum(s, "порт", "port"); ok && port > 0 {
		if port > 65535 {
			return p, fmt.Errorf("неверный Порт %v", port)
		}
		p.port = int(port)
	} else {
		p.port = defaultMailPort(p.protocol, p.security)
	}
	if secs, ok := structNum(s, "таймаут", "timeout"); ok && secs > 0 {
		p.timeout = time.Duration(secs * float64(time.Second))
	}
	if p.folder == "" {
		p.folder = "INBOX"
	}
	return p, nil
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func defaultMailPort(protocol, security string) int {
	switch {
	case protocol == "pop3" && security == "tls":
		return 995
	case protocol == "pop3":
		return 110
	case security == "tls":
		return 993
	default:
		return 143
	}
}

func parseMailReceiveOptions(v any) (mailReceiveOptions, error) {
	opts := mailReceiveOptions{skip: map[string]bool{}, max: mailReceiveDefaultMax, maxSize: mailReceiveDefaultSize}
	if v == nil {
		return opts, nil
	}
	s, ok := v.(*Struct)
	if !ok {
		return opts, errors.New("параметры — Структура {Пропустить, Максимум, МаксимальныйРазмер, ОтметитьПрочитанными}")
	}
	if n, ok := structNum(s, "максимум", "max", "limit"); ok && n > 0 {
		if n > mailReceiveMaxMessages {
			n = mailReceiveMaxMessages
		}
		opts.max = int(n)
	}
	if n, ok := structNum(s, "максимальныйразмер", "maxsize"); ok && n > 0 {
		if n > mailReceiveMaxSize {
			n = mailReceiveMaxSize
		}
		opts.maxSize = int64(n)
	}
	if v := firstNonNil(s.Get("отметитьпрочитанными"), s.Get("markseen")); v != nil {
		opts.markSeen = truthy(v)
	}
	if v := firstNonNil(s.Get("пропустить"), s.Get("skip")); v != nil {
		var items []any
		switch x := v.(type) {
		case *Array:
			items = x.Iterate()
		case []any:
			items = x
		default:
			if it, ok := v.(interface{ Iterate() []any }); ok {
				items = it.Iterate()
			} else {
				return opts, errors.New("свойство Пропустить — массив Ид уже полученных писем")
			}
		}
		for _, it := range items {
			if it != nil {
				opts.skip[strings.TrimSpace(fmt.Sprintf("%v", it))] = true
			}
		}
	}
	return opts, nil
}

// mailConn — строковое соединение с почтовым сервером и сдвигаемым дедлайном.
type mailConn struct {
	conn    net.Conn
	r       *bufio.Reader
	timeout time.Duration
}

func dialMail(p mailProfile) (*mailConn, error) {
	d := net.Dialer{Timeout: p.timeout}
	addr := net.JoinHostPort(p.host, strconv.Itoa(p.port))
	var conn net.Conn
	var err error
	if p.security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, mailTLSConfig(p.host))
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("подключение не установлено: %w", err)
	}
	return &mailConn{conn: conn, r: bufio.NewReaderSize(conn, 32<<10), timeout: p.timeout}, nil
}

func (c *mailConn) startTLS(host string) error {
	tc := tls.Client(c.conn, mailTLSConfig(host))
	_ = tc.SetDeadline(time.Now().Add(c.timeout))
	if err := tc.Handshake(); err != nil {
		return fmt.Errorf("STARTTLS: %w", err)
	}
	c.conn = tc
	c.r = bufio.NewReaderSize(tc, 32<<10)
	return nil
}

func (c *mailConn) touch() { _ = c.conn.SetDeadline(time.Now().Add(c.timeout)) }

func (c *mailConn) close() { _ = c.conn.Close() }

func (c *mailConn) writeLine(line string) error {
	c.touch()
	_, err := io.WriteString(c.conn, line+"\r\n")
	return err
}

func (c *mailConn) readLine() (string, error) {
	c.touch()
	var buf []byte
	for {
		chunk, isPrefix, err := c.r.ReadLine()
		if err != nil {
			return "", err
		}
		buf = append(buf, chunk...)
		if len(buf) > mailReceiveMaxLine {
			return "", errors.New("слишком длинная строка ответа сервера")
		}
		if !isPrefix {
			return string(buf), nil
		}
	}
}

func (c *mailConn) readN(n int64) ([]byte, error) {
	c.touch()
	buf := make([]byte, n)
	_, err := io.ReadFull(c.r, buf)
	return buf, err
}

// serverText — ответ сервера для сообщения об ошибке, без управляющих символов.
func serverText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}
