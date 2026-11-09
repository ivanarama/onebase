package interpreter

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// Отправка письма с профилем SMTP из DSL (как ИнтернетПочтовыйПрофиль 1С):
// разные ящики — партнёрам, аварийный — задаются данными конфигурации, а не
// одной секцией email в config/app.yaml. Без профиля письмо уходит, как и
// раньше, через app.yaml.

// EmailProfile — параметры SMTP-подключения из DSL.
type EmailProfile struct {
	Host     string
	Port     int
	User     string
	Password string // значение или enc:-секрет; ссылки env:/file: из DSL не принимаются
	Security string // "tls" | "starttls" | "none"
	Auth     string // "" (выбрать по серверу) | "plain" | "login"
	Timeout  time.Duration
	From     string
	FromName string
}

// EmailMessage — письмо целиком: несколько получателей, копия, текст и/или HTML,
// вложения и отправитель (переопределяет профиль или app.yaml).
type EmailMessage struct {
	To       []string
	Cc       []string
	Subject  string
	Text     string
	HTML     string
	Files    []EmailAttachment
	From     string
	FromName string
}

// EmailMessageSender — отправитель, умеющий письмо с профилем, несколькими
// получателями и копией. Реализуется mailer.Mailer; проверяется
// type-assertion, как EmailAttachmentSender.
type EmailMessageSender interface {
	SendMessage(profile *EmailProfile, msg EmailMessage) error
}

// ProfileMailer — отправитель для писем с профилем, когда секции email в
// app.yaml нет и штатного отправителя у DSL нет вовсе. Регистрирует пакет
// mailer (init); nil — отправка с профилем недоступна.
var ProfileMailer EmailMessageSender

const (
	emailProfileDefaultTimeout = 30 * time.Second
	emailProfileMaxTimeout     = 5 * time.Minute
	MaxEmailRecipients         = 50
)

// parseEmailProfile разбирает Структуру профиля. Пароль в сообщения об ошибках
// не попадает никогда.
func parseEmailProfile(v any) (*EmailProfile, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return nil, errors.New("профиль — Структура {Сервер, Порт, Пользователь, Пароль, Защита, Таймаут, Отправитель, ИмяОтправителя}")
	}
	p := &EmailProfile{
		Host:     structStr(s, "сервер", "server", "host"),
		User:     structStr(s, "пользователь", "user", "login"),
		From:     structStr(s, "отправитель", "from"),
		FromName: structStr(s, "имяотправителя", "fromname"),
		Timeout:  emailProfileDefaultTimeout,
	}
	if pw := firstNonNil(s.Get("пароль"), s.Get("password")); pw != nil {
		p.Password = fmt.Sprint(pw)
	}
	if p.Host == "" {
		return nil, errors.New("в профиле не указан Сервер")
	}
	if hasEmailHeaderControl(p.Host) || hasEmailHeaderControl(p.User) || strings.ContainsAny(p.Password, "\r\n") {
		return nil, errors.New("управляющие символы в Сервер, Пользователь или Пароль профиля недопустимы")
	}
	switch strings.ToLower(structStr(s, "защита", "security", "tls")) {
	case "", "tls", "ssl", "истина", "true":
		p.Security = "tls"
	case "starttls", "stls":
		p.Security = "starttls"
	case "нет", "none", "ложь", "false":
		p.Security = "none"
	default:
		return nil, fmt.Errorf("неизвестная Защита %q (TLS, STARTTLS или НЕТ)", structStr(s, "защита", "security"))
	}
	switch strings.ToLower(structStr(s, "аутентификация", "auth")) {
	case "", "авто", "auto":
	case "plain":
		p.Auth = "plain"
	case "login":
		p.Auth = "login"
	default:
		return nil, fmt.Errorf("неизвестная Аутентификация %q (PLAIN или LOGIN)", structStr(s, "аутентификация", "auth"))
	}
	if port, ok := structNum(s, "порт", "port"); ok && port > 0 {
		if port > 65535 {
			return nil, fmt.Errorf("неверный Порт %v", port)
		}
		p.Port = int(port)
	} else if p.Security == "tls" {
		p.Port = 465
	} else {
		p.Port = 587
	}
	if secs, ok := structNum(s, "таймаут", "timeout"); ok && secs > 0 {
		p.Timeout = time.Duration(secs * float64(time.Second))
		if p.Timeout > emailProfileMaxTimeout {
			p.Timeout = emailProfileMaxTimeout
		}
	}
	return p, nil
}

// splitEmailRecipients принимает «a@x, b@y; c@z» или Массив адресов.
func splitEmailRecipients(v any) ([]string, error) {
	var raw []string
	switch x := v.(type) {
	case nil:
		return nil, nil
	case *Array:
		for _, it := range x.Iterate() {
			if it != nil {
				raw = append(raw, fmt.Sprint(it))
			}
		}
	default:
		raw = strings.FieldsFunc(fmt.Sprint(x), func(r rune) bool { return r == ',' || r == ';' })
	}
	var out []string
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if len(r) > MaxEmailRecipientBytes || hasEmailHeaderControl(r) {
			return nil, errors.New("адрес получателя содержит недопустимое значение")
		}
		if _, err := mail.ParseAddress(r); err != nil {
			return nil, fmt.Errorf("неверный адрес %q: %w", r, err)
		}
		out = append(out, r)
	}
	if len(out) > MaxEmailRecipients {
		return nil, fmt.Errorf("получателей больше %d", MaxEmailRecipients)
	}
	return out, nil
}

// ValidateEmailMessageFull — проверка письма с несколькими получателями: те же
// пределы тела и вложений, что у ValidateEmailMessage.
func ValidateEmailMessageFull(msg EmailMessage) error {
	if len(msg.To) == 0 {
		return errors.New("поле Кому не задано")
	}
	for _, list := range [][]string{msg.To, msg.Cc} {
		for _, a := range list {
			if len(a) > MaxEmailRecipientBytes || hasEmailHeaderControl(a) {
				return errors.New("адрес получателя содержит недопустимое значение")
			}
			if _, err := mail.ParseAddress(a); err != nil {
				return fmt.Errorf("неверный адрес %q: %w", a, err)
			}
		}
	}
	if len(msg.To)+len(msg.Cc) > MaxEmailRecipients {
		return fmt.Errorf("получателей больше %d", MaxEmailRecipients)
	}
	if hasEmailHeaderControl(msg.From) || hasEmailHeaderControl(msg.FromName) ||
		len(msg.From) > MaxEmailRecipientBytes || len(msg.FromName) > MaxEmailRecipientBytes {
		return errors.New("отправитель содержит недопустимое значение")
	}
	// Тема, тело и вложения — общая проверка (адрес подставляем первый: он уже
	// проверен выше).
	return ValidateEmailMessage(msg.To[0], msg.Subject, msg.Text, msg.HTML, msg.Files)
}

// parseEmailAttachment — Структура {ИмяФайла, Данные, ТипСодержимого, Base64}.
// Двоичного типа в DSL нет: Данные — строка; Base64 = Истина, когда строка —
// base64 (ВыгрузитьВExcel, Записать(..., "pdf") отдают именно её).
func parseEmailAttachment(v any) (EmailAttachment, error) {
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return EmailAttachment{}, errors.New("вложение — Структура {ИмяФайла, Данные, ТипСодержимого, Base64}")
	}
	name := structStr(s, "имяфайла", "filename", "name")
	if name == "" {
		return EmailAttachment{}, errors.New("у вложения не указано ИмяФайла")
	}
	raw := ""
	if d := firstNonNil(s.Get("данные"), s.Get("data")); d != nil {
		raw = fmt.Sprint(d)
	}
	isB64 := false
	if b := firstNonNil(s.Get("base64")); b != nil {
		isB64 = truthy(b)
	}
	return newEmailAttachment(name, raw, structStr(s, "типсодержимого", "contenttype", "mimetype"), isB64)
}

func newEmailAttachment(name, raw, mimeType string, isB64 bool) (EmailAttachment, error) {
	data := []byte(raw)
	if isB64 {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if err != nil {
			return EmailAttachment{}, fmt.Errorf("данные вложения %q не base64: %w", name, err)
		}
		data = decoded
	}
	if mimeType == "" {
		mimeType = emailMimeByName(name)
	}
	att := EmailAttachment{Name: name, MimeType: mimeType, Data: data}
	if err := ValidateEmailMessage("x@x", "", "", "", []EmailAttachment{att}); err != nil {
		return EmailAttachment{}, err
	}
	return att, nil
}

func emailMimeByName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".xlsx"):
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case strings.HasSuffix(lower, ".xls"):
		return "application/vnd.ms-excel"
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "text/html"
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".csv"):
		return "text/plain"
	}
	return "application/octet-stream"
}

// emailParams — четвёртый аргумент ОтправитьПисьмо.
type emailParams struct {
	profile *EmailProfile
	msg     EmailMessage
}

func parseEmailParams(v any, msg EmailMessage) (emailParams, error) {
	out := emailParams{msg: msg}
	if v == nil {
		return out, nil
	}
	s, ok := v.(*Struct)
	if !ok || s == nil {
		return out, errors.New("параметры — Структура {HTML, Копия, Вложения, Профиль, Отправитель, ИмяОтправителя}")
	}
	if h := firstNonNil(s.Get("html"), s.Get("htmlтело"), s.Get("htmlbody")); h != nil {
		out.msg.HTML = fmt.Sprint(h)
	}
	cc, err := splitEmailRecipients(firstNonNil(s.Get("копия"), s.Get("cc")))
	if err != nil {
		return out, fmt.Errorf("копия: %w", err)
	}
	out.msg.Cc = cc
	if f := structStr(s, "отправитель", "from"); f != "" {
		out.msg.From = f
	}
	if n := structStr(s, "имяотправителя", "fromname"); n != "" {
		out.msg.FromName = n
	}
	if att := firstNonNil(s.Get("вложения"), s.Get("attachments")); att != nil {
		arr, ok := att.(*Array)
		if !ok {
			return out, errors.New("вложения — Массив Структур {ИмяФайла, Данные, ТипСодержимого, Base64}")
		}
		for _, it := range arr.Iterate() {
			a, err := parseEmailAttachment(it)
			if err != nil {
				return out, err
			}
			out.msg.Files = append(out.msg.Files, a)
		}
	}
	profile, err := parseEmailProfile(firstNonNil(s.Get("профиль"), s.Get("profile")))
	if err != nil {
		return out, err
	}
	out.profile = profile
	return out, nil
}

// sendEmailMessage — общий путь отправки письма с профилем/копией/вложениями.
// Без профиля, копии и с одним получателем годится и старый EmailSender.
func sendEmailMessage(sender EmailSender, profile *EmailProfile, msg EmailMessage) error {
	if err := ValidateEmailMessageFull(msg); err != nil {
		return err
	}
	var ms EmailMessageSender
	if s, ok := sender.(EmailMessageSender); ok && (profile != nil || sender.Configured()) {
		ms = s
	} else if profile != nil {
		ms = ProfileMailer
	}
	if ms != nil {
		if profile == nil && (sender == nil || !sender.Configured()) {
			return errors.New("email не настроен — добавьте секцию email: в config/app.yaml или передайте Профиль")
		}
		return ms.SendMessage(profile, msg)
	}
	if profile != nil {
		return errors.New("отправка с профилем недоступна в этой среде")
	}
	if sender == nil || !sender.Configured() {
		return errors.New("email не настроен — добавьте секцию email: в config/app.yaml или передайте Профиль")
	}
	if len(msg.To) != 1 || len(msg.Cc) != 0 || msg.From != "" || msg.FromName != "" {
		return errors.New("отправитель не поддерживает нескольких получателей, копию и отправителя письма")
	}
	if len(msg.Files) > 0 {
		as, ok := sender.(EmailAttachmentSender)
		if !ok {
			return errors.New("отправитель не поддерживает вложения")
		}
		return as.SendWithAttachments(msg.To[0], msg.Subject, msg.Text, msg.HTML, msg.Files)
	}
	return sender.Send(msg.To[0], msg.Subject, msg.Text, msg.HTML)
}
