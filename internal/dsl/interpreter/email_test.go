package interpreter_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSender implements interpreter.EmailSender for testing.
type stubSender struct {
	to      string
	subject string
	text    string
	html    string
	calls   int
}

func (s *stubSender) Configured() bool { return true }
func (s *stubSender) Send(to, subject, textBody, htmlBody string) error {
	s.to = to
	s.subject = subject
	s.text = textBody
	s.html = htmlBody
	s.calls++
	return nil
}

func TestEmailShorthand(t *testing.T) {
	stub := &stubSender{}
	src := `Процедура Тест()
  ОтправитьПисьмо("client@example.com", "Заказ принят", "Привет!");
КонецПроцедуры`
	runHTTPSrc(t, src, interpreter.NewEmailFunctions(stub, nil))
	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, "client@example.com", stub.to)
	assert.Equal(t, "Заказ принят", stub.subject)
	assert.Equal(t, "Привет!", stub.text)
}

func TestEmailObject(t *testing.T) {
	stub := &stubSender{}
	src := `Процедура Тест()
  Письмо = Новый ПисьмоEmail;
  Письмо.Кому     = "boss@company.ru";
  Письмо.Тема     = "Отчёт";
  Письмо.Текст    = "Итоги за месяц";
  Письмо.HTMLТело = "<b>Итоги</b>";
  Письмо.Отправить();
КонецПроцедуры`
	runHTTPSrc(t, src, interpreter.NewEmailFunctions(stub, nil))
	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, "boss@company.ru", stub.to)
	assert.Equal(t, "Отчёт", stub.subject)
	assert.Equal(t, "Итоги за месяц", stub.text)
	assert.Equal(t, "<b>Итоги</b>", stub.html)
}

func TestEmailNotConfigured(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    ОтправитьПисьмо("x@y.com", "тема", "текст");
    Возврат "no error";
  Исключение
    Возврат "caught: " + ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	// nil sender → should panic with user error
	extra := interpreter.NewEmailFunctions(nil, nil)
	result := runHTTPSrc(t, src, extra)
	msg, ok := result.(string)
	require.True(t, ok, fmt.Sprintf("expected string, got %T", result))
	assert.Contains(t, msg, "caught:")
	assert.Contains(t, msg, "не настроен")
}

func TestValidateEmailMessageRejectsHeaderInjection(t *testing.T) {
	tests := []struct {
		name    string
		to      string
		subject string
	}{
		{name: "recipient", to: "victim@example.com\r\nBcc: attacker@example.com", subject: "ok"},
		{name: "subject", to: "victim@example.com", subject: "ok\r\nBcc: attacker@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := interpreter.ValidateEmailMessage(tt.to, tt.subject, "body", "", nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "управляющий символ")
		})
	}
	require.NoError(t, interpreter.ValidateEmailMessage(
		"Получатель <client@example.com>", "Обычная тема", "body", "", nil,
	))
}

func TestEmailObjectRejectsOversizedSubjectAtAssignment(t *testing.T) {
	stub := &stubSender{}
	subject := strings.Repeat("x", interpreter.MaxEmailSubjectBytes+1)
	src := fmt.Sprintf(`Процедура Тест()
  Письмо = Новый ПисьмоEmail;
  Письмо.Кому = "client@example.com";
  Письмо.Тема = "%s";
КонецПроцедуры`, subject)
	err := runHTTPSrcErr(t, src, interpreter.NewEmailFunctions(stub, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "превышает")
	assert.Equal(t, 0, stub.calls)
}

// attachStubSender реализует и EmailSender, и EmailAttachmentSender.
type attachStubSender struct {
	stubSender
	files []interpreter.EmailAttachment
}

func (s *attachStubSender) SendWithAttachments(to, subject, textBody, htmlBody string, files []interpreter.EmailAttachment) error {
	s.to, s.subject, s.text, s.html = to, subject, textBody, htmlBody
	s.files = files
	s.calls++
	return nil
}

// ПисьмоEmail.ПрисоединитьФайл: файл читается с диска и уходит в
// SendWithAttachments; без вложений используется обычный Send.
func TestEmailObjectWithAttachment(t *testing.T) {
	stub := &attachStubSender{}
	dir := t.TempDir()
	path := filepath.Join(dir, "прайс.zip")
	require.NoError(t, os.WriteFile(path, []byte{0x50, 0x4B, 0x03, 0x04}, 0o644))

	src := fmt.Sprintf(`Процедура Тест()
  Письмо = Новый ПисьмоEmail;
  Письмо.Кому  = "client@example.com";
  Письмо.Тема  = "Обновление";
  Письмо.Текст = "Файл во вложении";
  Письмо.ПрисоединитьФайл("%s");
  Письмо.Отправить();
КонецПроцедуры`, path)
	runHTTPSrc(t, src, interpreter.NewEmailFunctions(stub, nil))

	require.Equal(t, 1, stub.calls)
	require.Len(t, stub.files, 1)
	assert.Equal(t, "прайс.zip", stub.files[0].Name)
	assert.Equal(t, []byte{0x50, 0x4B, 0x03, 0x04}, stub.files[0].Data)
	assert.NotEmpty(t, stub.files[0].MimeType)
}

// Отправитель без поддержки вложений даёт понятную ошибку.
func TestEmailAttachmentUnsupportedSender(t *testing.T) {
	stub := &stubSender{}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	src := fmt.Sprintf(`Процедура Тест()
  Письмо = Новый ПисьмоEmail;
  Письмо.Кому = "a@b.ru";
  Письмо.Тема = "Т";
  Письмо.ПрисоединитьФайл("%s");
  Письмо.Отправить();
КонецПроцедуры`, path)
	err := runHTTPSrcErr(t, src, interpreter.NewEmailFunctions(stub, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "не поддерживает вложения")
}

// runHTTPSrcErr — как runHTTPSrc, но возвращает ошибку исполнения.
func runHTTPSrcErr(t *testing.T, src string, extra map[string]any) error {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	require.NoError(t, err)
	interp := interpreter.New()
	var result any
	return interp.RunWithResult(prog.Procedures[0], nil, &result, extra)
}
