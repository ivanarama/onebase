package mailer

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/secrets"
)

// Письмо с профилем из DSL регистрирует пакет: без секции email в app.yaml у
// DSL нет штатного отправителя, а отправка по профилю нужна и тогда.
func init() {
	interpreter.ProfileMailer = New(Config{})
}

// tlsConfigFor — конфигурация TLS соединения с SMTP-сервером. Сертификат
// проверяется всегда; переменная — чтобы тест подставил корневой сертификат
// подставного сервера.
var tlsConfigFor = func(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

const defaultSendTimeout = 30 * time.Second

// SendMessage отправляет письмо: с профилем SMTP из DSL или, без профиля, через
// секцию email из app.yaml. Несколько получателей и копия уходят одним письмом.
// Ошибки строятся из адреса сервера, логина и ответа сервера — пароль в них
// не попадает.
func (m *Mailer) SendMessage(p *interpreter.EmailProfile, msg interpreter.EmailMessage) error {
	if err := interpreter.ValidateEmailMessageFull(msg); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	conn := sendParams{timeout: defaultSendTimeout}
	if p != nil {
		conn.host, conn.port, conn.user = p.Host, p.Port, p.User
		conn.security, conn.auth = p.Security, p.Auth
		if p.Timeout > 0 {
			conn.timeout = p.Timeout
		}
		// Из DSL принимается пароль строкой или enc:-секретом: ссылка
		// env:/file: позволила бы коду отправить содержимое файла или
		// переменной окружения на свой сервер под видом пароля.
		pass, err := secrets.Default().ResolveEnc(p.Password)
		if err != nil {
			return errors.New("email: пароль профиля не расшифрован (допустимы строка и enc:-секрет)")
		}
		conn.pass = pass
		conn.from, conn.fromName = p.From, p.FromName
		if conn.from == "" {
			conn.from = p.User
		}
	} else {
		if !m.Configured() {
			return errors.New("email не настроен — добавьте секцию email в config/app.yaml или передайте Профиль")
		}
		conn.host, conn.port, conn.user = m.cfg.SMTPHost, m.cfg.SMTPPort, m.cfg.SMTPUser
		if conn.port == 0 {
			conn.port = 587
		}
		conn.security = "opportunistic"
		if conn.port == 465 {
			conn.security = "tls"
		}
		if conn.user != "" {
			pass, err := m.password()
			if err != nil {
				return fmt.Errorf("почта: пароль SMTP: %w", err)
			}
			conn.pass = pass
		}
		conn.from, conn.fromName = m.cfg.FromAddress, m.cfg.FromName
	}
	if msg.From != "" {
		conn.from = msg.From
	}
	if msg.FromName != "" {
		conn.fromName = msg.FromName
	}
	from, err := mail.ParseAddress(strings.TrimSpace(conn.from))
	if err != nil {
		return fmt.Errorf("email: неверный адрес отправителя: %w", err)
	}
	fromName := from.Name
	if conn.fromName != "" {
		fromName = conn.fromName
	}
	var to, cc, rcpts []string
	for _, list := range []struct {
		in  []string
		out *[]string
	}{{msg.To, &to}, {msg.Cc, &cc}} {
		for _, a := range list.in {
			addr, err := mail.ParseAddress(strings.TrimSpace(a))
			if err != nil {
				return fmt.Errorf("email: неверный адрес %q: %w", a, err)
			}
			*list.out = append(*list.out, formatAddressHeader(addr.Name, addr.Address))
			rcpts = append(rcpts, addr.Address)
		}
	}
	data := buildMsgFull(formatAddressHeader(fromName, from.Address), strings.Join(to, ", "), strings.Join(cc, ", "),
		msg.Subject, msg.Text, msg.HTML, msg.Files)
	if err := deliver(conn, from.Address, rcpts, data); err != nil {
		return fmt.Errorf("почта %s: %w", conn.host, err)
	}
	return nil
}

type sendParams struct {
	host, user, pass string
	port             int
	security, auth   string // security: tls | starttls | none | opportunistic
	timeout          time.Duration
	from, fromName   string
}

func deliver(p sendParams, envFrom string, rcpts []string, data []byte) error {
	addr := net.JoinHostPort(p.host, strconv.Itoa(p.port))
	d := net.Dialer{Timeout: p.timeout}
	var conn net.Conn
	var err error
	if p.security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, tlsConfigFor(p.host))
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("подключение не установлено: %w", err)
	}
	// Вся сессия ограничена: зависший сервер не должен держать регламент.
	_ = conn.SetDeadline(time.Now().Add(3 * p.timeout))
	c, err := smtp.NewClient(conn, p.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("приветствие SMTP: %w", err)
	}
	defer c.Close() //nolint:errcheck
	secured := p.security == "tls"
	if p.security == "starttls" || p.security == "opportunistic" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfigFor(p.host)); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
			secured = true
		} else if p.security == "starttls" {
			return errors.New("сервер не поддерживает STARTTLS")
		}
	}
	if p.user != "" {
		if !secured && p.security != "none" {
			// Без явного «Защита: НЕТ» пароль по открытому каналу не уходит.
			return errors.New("сервер без шифрования: пароль не отправлен (для такого сервера укажите Защита НЕТ)")
		}
		auth := chooseAuth(c, p.auth, p.user, p.pass)
		if err := c.Auth(auth); err != nil {
			// Ответ на AUTH в текст не вставляем: часть серверов повторяет
			// в нём присланное.
			return fmt.Errorf("сервер отклонил вход пользователя %s", p.user)
		}
	}
	if err := c.Mail(envFrom); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	return c.Quit()
}

// chooseAuth — PLAIN или LOGIN: явный выбор профиля, иначе по объявленному
// сервером AUTH (PLAIN, затем LOGIN — 1С ходит через LOGIN).
func chooseAuth(c *smtp.Client, mode, user, pass string) smtp.Auth {
	switch mode {
	case "login":
		return loginAuth{user: user, pass: pass}
	case "plain":
		return plainAuth{user: user, pass: pass}
	}
	if ok, ext := c.Extension("AUTH"); ok {
		mechs := strings.Fields(strings.ToUpper(ext))
		hasPlain, hasLogin := false, false
		for _, m := range mechs {
			hasPlain = hasPlain || m == "PLAIN"
			hasLogin = hasLogin || m == "LOGIN"
		}
		if !hasPlain && hasLogin {
			return loginAuth{user: user, pass: pass}
		}
	}
	return plainAuth{user: user, pass: pass}
}

// plainAuth / loginAuth — свои механизмы: net/smtp.PlainAuth отказывается
// работать без TLS даже там, где профиль явно выбрал «Защита: НЕТ». Шифрование
// проверяет deliver: без него пароль уходит, только когда так решил профиль.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("неожиданный запрос сервера при входе PLAIN")
	}
	return nil, nil
}

type loginAuth struct{ user, pass string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(prompt, "user"):
		return []byte(a.user), nil
	case strings.Contains(prompt, "pass"):
		return []byte(a.pass), nil
	}
	return nil, errors.New("неожиданный запрос сервера при входе LOGIN")
}
