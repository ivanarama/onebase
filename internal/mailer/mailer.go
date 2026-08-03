package mailer

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"net/smtp"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/secrets"
)

// Config holds SMTP settings from config/app.yaml section "email".
type Config struct {
	SMTPHost    string `yaml:"smtp_host"`
	SMTPPort    int    `yaml:"smtp_port"`
	SMTPUser    string `yaml:"smtp_user"`
	SMTPPass    string `yaml:"smtp_password"` // значение или ссылка env:ИМЯ / file:/путь / enc:… (план 83)
	FromName    string `yaml:"from_name"`
	FromAddress string `yaml:"from_address"`
}

type Mailer struct {
	cfg Config
}

func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg}
}

func (m *Mailer) Configured() bool {
	return m != nil && m.cfg.SMTPHost != ""
}

// Send delivers an email. Pass empty htmlBody for plain-text only.
func (m *Mailer) Send(to, subject, textBody, htmlBody string) error {
	return m.SendWithAttachments(to, subject, textBody, htmlBody, nil)
}

// SendWithAttachments delivers an email with file attachments (multipart/mixed).
// Реализует interpreter.EmailAttachmentSender — DSL-объект ПисьмоEmail
// использует его при наличии вложений (ПисьмоEmail.ПрисоединитьФайл).
func (m *Mailer) SendWithAttachments(to, subject, textBody, htmlBody string, files []interpreter.EmailAttachment) error {
	if !m.Configured() {
		return fmt.Errorf("email не настроен — добавьте секцию email в config/app.yaml")
	}
	headers, err := normalizeMessageHeaders(m.cfg, to, subject, textBody, htmlBody, files)
	if err != nil {
		return err
	}
	port := m.cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", m.cfg.SMTPHost, port)

	msg := buildMsgWithFiles(headers.from, headers.to, headers.subject, textBody, htmlBody, files)

	var auth smtp.Auth
	if m.cfg.SMTPUser != "" {
		pass, err := m.password()
		if err != nil {
			return fmt.Errorf("почта: пароль SMTP: %w", err)
		}
		auth = smtp.PlainAuth("", m.cfg.SMTPUser, pass, m.cfg.SMTPHost)
	}

	if port == 465 {
		return sendTLS(addr, m.cfg.SMTPHost, auth, headers.envelopeFrom, headers.envelopeTo, msg)
	}
	return smtp.SendMail(addr, auth, headers.envelopeFrom, []string{headers.envelopeTo}, msg)
}

type messageHeaders struct {
	from         string
	to           string
	subject      string
	envelopeFrom string
	envelopeTo   string
}

func normalizeMessageHeaders(cfg Config, to, subject, textBody, htmlBody string, files []interpreter.EmailAttachment) (messageHeaders, error) {
	if err := interpreter.ValidateEmailMessage(to, subject, textBody, htmlBody, files); err != nil {
		return messageHeaders{}, fmt.Errorf("email: %w", err)
	}
	if len(cfg.FromAddress) > interpreter.MaxEmailRecipientBytes || headerHasControl(cfg.FromAddress) {
		return messageHeaders{}, fmt.Errorf("email: адрес отправителя содержит недопустимое значение")
	}
	parsedFrom, err := mail.ParseAddress(strings.TrimSpace(cfg.FromAddress))
	if err != nil {
		return messageHeaders{}, fmt.Errorf("email: неверный адрес отправителя: %w", err)
	}
	if len(cfg.FromName) > interpreter.MaxEmailRecipientBytes || headerHasControl(cfg.FromName) {
		return messageHeaders{}, fmt.Errorf("email: имя отправителя содержит недопустимое значение")
	}
	parsedTo, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil {
		return messageHeaders{}, fmt.Errorf("email: неверный адрес получателя: %w", err)
	}

	fromName := parsedFrom.Name
	if cfg.FromName != "" {
		fromName = cfg.FromName
	}
	return messageHeaders{
		from:         formatAddressHeader(fromName, parsedFrom.Address),
		to:           formatAddressHeader(parsedTo.Name, parsedTo.Address),
		subject:      subject,
		envelopeFrom: parsedFrom.Address,
		envelopeTo:   parsedTo.Address,
	}, nil
}

func formatAddressHeader(name, address string) string {
	if name == "" {
		return address
	}
	return (&mail.Address{Name: name, Address: address}).String()
}

func headerHasControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0
}

// password разыменовывает пароль SMTP в момент отправки. Поддержаны ссылки
// env:/file:/enc: (план 83); историческая форма env:ИМЯ — их частный случай.
// Ошибка возвращается наверх: отправлять письмо, молча подставив пустой пароль,
// нельзя — сервер ответит отказом, а причина потеряется.
func (m *Mailer) password() (string, error) {
	return secrets.Default().Resolve(m.cfg.SMTPPass)
}

func sendTLS(addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Quit() //nolint:errcheck
	if auth != nil {
		if err = c.Auth(auth); err != nil {
			return err
		}
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	defer wc.Close() //nolint:errcheck
	_, err = wc.Write(msg)
	return err
}

func buildMsg(from, to, subject, textBody, htmlBody string) []byte {
	return buildMsgWithFiles(from, to, subject, textBody, htmlBody, nil)
}

func buildMsgWithFiles(from, to, subject, textBody, htmlBody string, files []interpreter.EmailAttachment) []byte {
	var b strings.Builder
	b.WriteString("From: " + stripHeaderControls(from) + "\r\n")
	b.WriteString("To: " + stripHeaderControls(to) + "\r\n")
	b.WriteString("Subject: " + stripHeaderControls(subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if len(files) > 0 {
		const mixed = "==boundary_onebase_mixed"
		b.WriteString("Content-Type: multipart/mixed; boundary=\"" + mixed + "\"\r\n\r\n")
		// Тело письма — вложенной частью (alternative при наличии HTML).
		b.WriteString("--" + mixed + "\r\n")
		writeBodyPart(&b, textBody, htmlBody)
		// Файлы — base64 с переносом строк по RFC 2045.
		for _, f := range files {
			mt := safeMediaType(f.MimeType)
			b.WriteString("--" + mixed + "\r\n")
			b.WriteString("Content-Type: " + mt + "; name=\"" + sanitizeHeaderValue(f.Name) + "\"\r\n")
			b.WriteString("Content-Disposition: attachment; filename=\"" + sanitizeHeaderValue(f.Name) + "\"\r\n")
			b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
			writeBase64Wrapped(&b, f.Data)
			b.WriteString("\r\n")
		}
		b.WriteString("--" + mixed + "--\r\n")
		return []byte(b.String())
	}

	writeBodyPart(&b, textBody, htmlBody)
	return []byte(b.String())
}

// writeBodyPart пишет заголовок Content-Type и тело (text и/или html).
// Вызывается и как корень письма без вложений, и как часть multipart/mixed.
func writeBodyPart(b *strings.Builder, textBody, htmlBody string) {
	if htmlBody != "" {
		const boundary = "==boundary_onebase_email"
		b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
		if textBody != "" {
			b.WriteString("--" + boundary + "\r\n")
			b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
			b.WriteString(textBody + "\r\n")
		}
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		b.WriteString(htmlBody + "\r\n")
		b.WriteString("--" + boundary + "--\r\n")
	} else {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(textBody + "\r\n")
	}
}

// sanitizeHeaderValue убирает из имени файла символы, ломающие MIME-заголовок.
func sanitizeHeaderValue(s string) string {
	s = strings.NewReplacer("\r", "", "\n", "", "\"", "'").Replace(stripHeaderControls(s))
	return s
}

func stripHeaderControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func safeMediaType(value string) string {
	if value == "" {
		return "application/octet-stream"
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil || mediaType == "" || headerHasControl(mediaType) {
		return "application/octet-stream"
	}
	return mediaType
}

// writeBase64Wrapped пишет данные в base64 строками по 76 символов (RFC 2045).
func writeBase64Wrapped(b *strings.Builder, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	const lineLen = 76
	for len(enc) > lineLen {
		b.WriteString(enc[:lineLen] + "\r\n")
		enc = enc[lineLen:]
	}
	if enc != "" {
		b.WriteString(enc + "\r\n")
	}
}
