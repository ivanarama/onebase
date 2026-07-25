package mailer

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

// Письмо с вложением разбирается стандартным MIME-парсером: multipart/mixed,
// внутри — тело (alternative) и файл, содержимое файла совпадает после base64.
func TestBuildMsgWithFiles_ParsableMultipart(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x01, 0xFF, 0x7E}, 100)
	msg := buildMsgWithFiles("a@b.ru", "c@d.ru", "Тема", "текст", "<b>html</b>",
		[]interpreter.EmailAttachment{{Name: "release.zip", MimeType: "application/zip", Data: payload}})

	tp := textproto.NewReader(newBufReader(msg))
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("Content-Type = %s, ожидался multipart/mixed", mediaType)
	}

	mr := multipart.NewReader(tp.R, params["boundary"])

	// Часть 1 — тело (multipart/alternative).
	p1, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p1.Header.Get("Content-Type"), "multipart/alternative") {
		t.Fatalf("часть 1: %s, ожидался multipart/alternative", p1.Header.Get("Content-Type"))
	}

	// Часть 2 — вложение.
	p2, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if p2.FileName() != "release.zip" {
		t.Fatalf("имя вложения = %q", p2.FileName())
	}
	if p2.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("encoding = %q", p2.Header.Get("Content-Transfer-Encoding"))
	}
	// multipart.Part не декодирует base64 сам — декодируем явно.
	raw, err := io.ReadAll(p2)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeB64(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatalf("вложение повреждено: %d байт вместо %d", len(decoded), len(payload))
	}
}

// Без вложений структура письма прежняя (multipart/alternative в корне).
func TestBuildMsg_BackwardCompatible(t *testing.T) {
	msg := buildMsg("a@b.ru", "c@d.ru", "Тема", "текст", "<b>html</b>")
	if !bytes.Contains(msg, []byte("Content-Type: multipart/alternative")) {
		t.Fatal("ожидался multipart/alternative в корне письма без вложений")
	}
	if bytes.Contains(msg, []byte("multipart/mixed")) {
		t.Fatal("multipart/mixed не должен появляться без вложений")
	}
}

// Имя файла с кавычками/переводами строк не ломает заголовок.
func TestSanitizeHeaderValue(t *testing.T) {
	got := sanitizeHeaderValue("evil\"\r\nX-Inject: 1.zip")
	if strings.ContainsAny(got, "\"\r\n") {
		t.Fatalf("опасные символы остались: %q", got)
	}
}

func TestBuildMsg_HeaderValuesCannotInjectNewHeaders(t *testing.T) {
	msg := buildMsg(
		"from@example.com\r\nBcc: attacker@example.com",
		"to@example.com\r\nCc: attacker@example.com",
		"Тема\r\nReply-To: attacker@example.com",
		"body",
		"",
	)
	for _, injected := range []string{"\r\nBcc:", "\r\nCc:", "\r\nReply-To:"} {
		if bytes.Contains(msg, []byte(injected)) {
			t.Fatalf("в сообщение попал внедрённый заголовок %q:\n%s", injected, msg)
		}
	}
}

func TestSafeMediaTypeRejectsHeaderInjection(t *testing.T) {
	if got := safeMediaType("text/plain\r\nX-Evil: 1"); got != "application/octet-stream" {
		t.Fatalf("safeMediaType = %q", got)
	}
}

// --- test helpers ---

func newBufReader(b []byte) *bufio.Reader { return bufio.NewReader(bytes.NewReader(b)) }

func decodeB64(s string) ([]byte, error) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
	return base64.StdEncoding.DecodeString(s)
}
