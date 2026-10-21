package interpreter

import (
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
)

// EmailSender is the minimal interface required by email DSL functions.
type EmailSender interface {
	Send(to, subject, textBody, htmlBody string) error
	Configured() bool
}

// EmailAttachment — вложение письма (имя файла, MIME-тип, содержимое).
type EmailAttachment struct {
	Name     string
	MimeType string
	Data     []byte
}

const (
	MaxEmailRecipientBytes        = 512
	MaxEmailSubjectBytes          = 256
	MaxEmailBodyBytes             = 16 << 20
	MaxEmailAttachmentBytes       = 25 << 20
	MaxEmailAttachmentsTotalBytes = 50 << 20
	MaxEmailAttachmentCount       = 20
	MaxEmailAttachmentNameBytes   = 255
)

// ValidateEmailMessage is the shared fail-closed boundary for DSL senders and
// the SMTP implementation. It rejects header injection and bounds allocations
// before MIME message construction.
func ValidateEmailMessage(to, subject, textBody, htmlBody string, files []EmailAttachment) error {
	to = strings.TrimSpace(to)
	if len(to) == 0 {
		return fmt.Errorf("поле Кому не задано")
	}
	if len(to) > MaxEmailRecipientBytes {
		return fmt.Errorf("поле Кому превышает %d байт", MaxEmailRecipientBytes)
	}
	if hasEmailHeaderControl(to) {
		return fmt.Errorf("поле Кому содержит недопустимый управляющий символ")
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("поле Кому содержит неверный email-адрес: %w", err)
	}
	if len(subject) > MaxEmailSubjectBytes {
		return fmt.Errorf("поле Тема превышает %d байт", MaxEmailSubjectBytes)
	}
	if hasEmailHeaderControl(subject) {
		return fmt.Errorf("поле Тема содержит недопустимый управляющий символ")
	}
	if len(textBody) > MaxEmailBodyBytes || len(htmlBody) > MaxEmailBodyBytes-len(textBody) {
		return fmt.Errorf("тело письма превышает %d байт", MaxEmailBodyBytes)
	}
	if len(files) > MaxEmailAttachmentCount {
		return fmt.Errorf("в письме больше %d вложений", MaxEmailAttachmentCount)
	}
	total := 0
	for _, file := range files {
		if len(file.Name) > MaxEmailAttachmentNameBytes || hasEmailHeaderControl(file.Name) {
			return fmt.Errorf("недопустимое имя вложения")
		}
		if len(file.Data) > MaxEmailAttachmentBytes {
			return fmt.Errorf("вложение %q превышает %d байт", file.Name, MaxEmailAttachmentBytes)
		}
		if len(file.Data) > MaxEmailAttachmentsTotalBytes-total {
			return fmt.Errorf("общий размер вложений превышает %d байт", MaxEmailAttachmentsTotalBytes)
		}
		total += len(file.Data)
		if file.MimeType != "" {
			mediaType, _, err := mime.ParseMediaType(file.MimeType)
			if err != nil || mediaType == "" || hasEmailHeaderControl(mediaType) {
				return fmt.Errorf("недопустимый MIME-тип вложения %q", file.Name)
			}
		}
	}
	return nil
}

func hasEmailHeaderControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0
}

// EmailAttachmentSender — необязательное расширение EmailSender: отправка
// письма с вложениями. Реализуется mailer.Mailer; проверяется type-assertion
// в момент отправки, чтобы существующие реализации EmailSender (моки в
// тестах) не требовали доработки.
type EmailAttachmentSender interface {
	SendWithAttachments(to, subject, textBody, htmlBody string, files []EmailAttachment) error
}

// EmailFileResolver optionally authorizes and resolves a path before an email
// attachment is read. UI uses it for RLS-checked attachment-storage paths,
// which intentionally live outside the ordinary DSL file sandbox.
type EmailFileResolver func(path string) (string, error)

// ─── dslEmail (Новый ПисьмоEmail) ────────────────────────────────────────────

type dslEmail struct {
	sender   EmailSender
	guard    NetGuard
	resolver EmailFileResolver
	to       string
	cc       string
	subject  string
	text     string
	html     string
	files    []EmailAttachment
	// profile/from/fromName — Профиль SMTP и отправитель письма из DSL; nil
	// профиль — штатная секция email из app.yaml.
	profile  *EmailProfile
	from     string
	fromName string
}

func (e *dslEmail) Get(field string) any {
	switch field {
	case "кому", "to":
		return e.to
	case "копия", "cc":
		return e.cc
	case "тема", "subject":
		return e.subject
	case "текст", "text", "body":
		return e.text
	case "htmlтело", "htmlbody":
		return e.html
	case "отправитель", "from":
		return e.from
	case "имяотправителя", "fromname":
		return e.fromName
	}
	return nil
}

func (e *dslEmail) Set(field string, val any) {
	s := fmt.Sprintf("%v", val)
	switch field {
	case "кому", "to":
		emailLengthOrRaise("ПисьмоEmail.Кому", s, MaxEmailRecipientBytes)
		e.to = s
	case "копия", "cc":
		emailLengthOrRaise("ПисьмоEmail.Копия", s, MaxEmailRecipientBytes)
		e.cc = s
	case "тема", "subject":
		emailLengthOrRaise("ПисьмоEmail.Тема", s, MaxEmailSubjectBytes)
		e.subject = s
	case "текст", "text", "body":
		emailLengthOrRaise("ПисьмоEmail.Текст", s, MaxEmailBodyBytes)
		e.text = s
	case "htmlтело", "htmlbody":
		emailLengthOrRaise("ПисьмоEmail.HTMLТело", s, MaxEmailBodyBytes)
		e.html = s
	case "отправитель", "from":
		emailLengthOrRaise("ПисьмоEmail.Отправитель", s, MaxEmailRecipientBytes)
		e.from = s
	case "имяотправителя", "fromname":
		emailLengthOrRaise("ПисьмоEmail.ИмяОтправителя", s, MaxEmailRecipientBytes)
		e.fromName = s
	case "профиль", "profile":
		p, err := parseEmailProfile(val)
		if err != nil {
			panic(userError{Msg: "ПисьмоEmail.Профиль: " + err.Error()})
		}
		e.profile = p
	}
}

func emailLengthOrRaise(field, value string, maxBytes int) {
	if len(value) > maxBytes {
		panic(userError{Msg: fmt.Sprintf("%s превышает %d байт", field, maxBytes)})
	}
}

func (e *dslEmail) CallMethod(name string, args []any) any {
	switch name {
	case "присоединитьфайл", "attachfile":
		// ПисьмоEmail.ПрисоединитьФайл(Путь[, ИмяВПисьме]) — файл читается с
		// диска в момент вызова (уважая файловую песочницу DSL).
		if len(args) < 1 {
			panic(userError{Msg: "ПисьмоEmail.ПрисоединитьФайл: не указан путь к файлу"})
		}
		pathArg := strings.TrimSpace(fmt.Sprint(args[0]))
		path := ""
		if e.resolver != nil {
			var err error
			path, err = e.resolver(pathArg)
			if err != nil {
				panic(userError{Msg: "ПисьмоEmail.ПрисоединитьФайл: " + err.Error()})
			}
		} else {
			path = safePathOrRaise("ПисьмоEmail.ПрисоединитьФайл", pathArg)
		}
		file, err := os.Open(path)
		if err != nil {
			panic(userError{Msg: "ПисьмоEmail.ПрисоединитьФайл: " + err.Error()})
		}
		data, readErr := io.ReadAll(io.LimitReader(file, MaxEmailAttachmentBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			panic(userError{Msg: "ПисьмоEmail.ПрисоединитьФайл: " + readErr.Error()})
		}
		if closeErr != nil {
			panic(userError{Msg: "ПисьмоEmail.ПрисоединитьФайл: " + closeErr.Error()})
		}
		if len(data) > MaxEmailAttachmentBytes {
			panic(userError{Msg: fmt.Sprintf("ПисьмоEmail.ПрисоединитьФайл: файл превышает %d байт", MaxEmailAttachmentBytes)})
		}
		fname := filepath.Base(path)
		if len(args) > 1 {
			if n := strings.TrimSpace(fmt.Sprint(args[1])); n != "" {
				fname = n
			}
		}
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(fname)))
		if mt == "" {
			mt = "application/octet-stream"
		}
		if len(e.files) >= MaxEmailAttachmentCount {
			panic(userError{Msg: fmt.Sprintf("ПисьмоEmail.ПрисоединитьФайл: не более %d вложений", MaxEmailAttachmentCount)})
		}
		total := len(data)
		for _, attached := range e.files {
			if len(attached.Data) > MaxEmailAttachmentsTotalBytes-total {
				panic(userError{Msg: fmt.Sprintf("ПисьмоEmail.ПрисоединитьФайл: общий размер вложений превышает %d байт", MaxEmailAttachmentsTotalBytes)})
			}
			total += len(attached.Data)
		}
		e.files = append(e.files, EmailAttachment{Name: fname, MimeType: mt, Data: data})
		return nil
	case "добавитьвложение", "addattachment":
		// ПисьмоEmail.ДобавитьВложение(ИмяФайла, Данные[, ТипСодержимого[, Base64]]) —
		// вложение из данных, а не с диска: двоичного типа в DSL нет, поэтому
		// Данные — строка, Base64 = Истина для base64 (ВыгрузитьВExcel, PDF).
		if len(args) < 2 {
			panic(userError{Msg: "ПисьмоEmail.ДобавитьВложение(ИмяФайла, Данные[, ТипСодержимого[, Base64]])"})
		}
		mt := ""
		if len(args) > 2 && args[2] != nil {
			mt = strings.TrimSpace(fmt.Sprint(args[2]))
		}
		isB64 := len(args) > 3 && truthy(args[3])
		att, err := newEmailAttachment(strings.TrimSpace(fmt.Sprint(args[0])), fmt.Sprint(args[1]), mt, isB64)
		if err != nil {
			panic(userError{Msg: "ПисьмоEmail.ДобавитьВложение: " + err.Error()})
		}
		if len(e.files) >= MaxEmailAttachmentCount {
			panic(userError{Msg: fmt.Sprintf("ПисьмоEmail.ДобавитьВложение: не более %d вложений", MaxEmailAttachmentCount)})
		}
		e.files = append(e.files, att)
		return nil
	case "отправить", "send":
		checkNet(e.guard)
		if e.subject == "" {
			panic(userError{Msg: "ПисьмоEmail.Отправить: поле Тема не задана"})
		}
		to, err := splitEmailRecipients(e.to)
		if err != nil {
			panic(userError{Msg: "ПисьмоEmail.Отправить: Кому: " + err.Error()})
		}
		cc, err := splitEmailRecipients(e.cc)
		if err != nil {
			panic(userError{Msg: "ПисьмоEmail.Отправить: Копия: " + err.Error()})
		}
		msg := EmailMessage{To: to, Cc: cc, Subject: e.subject, Text: e.text, HTML: e.html, Files: e.files, From: e.from, FromName: e.fromName}
		if err := sendEmailMessage(e.sender, e.profile, msg); err != nil {
			panic(userError{Msg: "ОтправитьПисьмо: " + err.Error()})
		}
		return nil
	}
	panic(userError{Msg: "ПисьмоEmail: неизвестный метод " + name})
}

// ─── NewEmailFunctions ────────────────────────────────────────────────────────

// NewEmailFunctions returns DSL functions/factories to inject into extraVars.
// If sender is nil or not configured, functions panic with a user-friendly message.
func NewEmailFunctions(sender EmailSender, guard NetGuard, resolvers ...EmailFileResolver) map[string]any {
	var resolver EmailFileResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	// ОтправитьПисьмо(Кому, Тема, Текст[, Параметры]) — Параметры: Структура
	// {HTML, Копия, Вложения, Профиль, Отправитель, ИмяОтправителя}. Кому и Копия —
	// адреса через запятую/точку с запятой или Массив.
	shorthand := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		checkNet(guard)
		to, err := splitEmailRecipients(argAt(args, 0))
		if err != nil {
			panic(userError{Msg: "ОтправитьПисьмо: Кому: " + err.Error(), File: file, Line: line})
		}
		msg := EmailMessage{To: to, Subject: strArg(args, 1), Text: strArg(args, 2)}
		params, err := parseEmailParams(argAt(args, 3), msg)
		if err != nil {
			panic(userError{Msg: "ОтправитьПисьмо: " + err.Error(), File: file, Line: line})
		}
		if err := sendEmailMessage(sender, params.profile, params.msg); err != nil {
			panic(userError{Msg: "ОтправитьПисьмо: " + err.Error(), File: file, Line: line})
		}
		return nil, nil
	})

	// Объект письма создаётся и без секции email: Профиль задаётся свойством,
	// а отсутствие и того и другого — ошибка Отправить.
	emailFactory := func(args []any) any {
		checkNet(guard)
		return &dslEmail{sender: sender, guard: guard, resolver: resolver}
	}

	receive := newReceiveMailBuiltin(guard)
	return map[string]any{
		"ОтправитьПисьмо":        shorthand,
		"SendEmail":              shorthand,
		"ПолучитьПисьма":         receive,
		"ReceiveEmails":          receive,
		"__factory_ПисьмоEmail":  emailFactory,
		"__factory_EmailMessage": emailFactory,
	}
}
