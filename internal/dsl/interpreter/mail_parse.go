package interpreter

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
)

// receivedMail — письмо, разобранное для DSL (ПолучитьПисьма). Вложения не
// разбираются: задаче «письмо → задача» нужны отправитель, тема, дата и текст.
type receivedMail struct {
	ID        string // UIDL (POP3) или UIDVALIDITY:UID (IMAP) — ключ «уже получено»
	MessageID string
	From      string
	FromName  string
	Subject   string
	Date      time.Time
	HasDate   bool
	HTML      string
	Text      string
}

// mailWordDecoder раскрывает заголовки RFC 2047 в любой кодировке, которую
// знает htmlindex (utf-8, windows-1251, koi8-r, iso-8859-*), а не только в
// utf-8/iso-8859-1, как mime.WordDecoder по умолчанию.
var mailWordDecoder = &mime.WordDecoder{CharsetReader: mailCharsetReader}

func mailCharsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := htmlindex.Get(strings.TrimSpace(charset))
	if err != nil {
		return nil, fmt.Errorf("неизвестная кодировка %q", charset)
	}
	return enc.NewDecoder().Reader(input), nil
}

// decodeMailCharset переводит тело части в UTF-8 по charset из Content-Type.
// Неизвестная кодировка — текст как есть: письмо важнее идеальных букв.
func decodeMailCharset(body []byte, charset string) string {
	charset = strings.TrimSpace(strings.ToLower(charset))
	if charset == "" || charset == "utf-8" || charset == "utf8" || charset == "us-ascii" {
		return string(body)
	}
	r, err := mailCharsetReader(charset, bytes.NewReader(body))
	if err != nil {
		return string(body)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return string(body)
	}
	return string(out)
}

// decodeTransfer снимает Content-Transfer-Encoding части.
func decodeTransfer(body []byte, encoding string) []byte {
	switch strings.TrimSpace(strings.ToLower(encoding)) {
	case "base64":
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body))
		if out, err := base64.StdEncoding.DecodeString(clean); err == nil {
			return out
		}
		if out, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err == nil {
			return out
		}
		return body
	case "quoted-printable":
		if out, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
			return out
		}
		return body
	default:
		return body
	}
}

// parseMailMessage разбирает исходный текст письма (RFC 5322 + MIME).
func parseMailMessage(raw []byte) (receivedMail, error) {
	var out receivedMail
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return out, fmt.Errorf("письмо не разобрано: %w", err)
	}
	h := msg.Header
	if subj, err := mailWordDecoder.DecodeHeader(h.Get("Subject")); err == nil {
		out.Subject = strings.TrimSpace(subj)
	} else {
		out.Subject = strings.TrimSpace(h.Get("Subject"))
	}
	parser := mail.AddressParser{WordDecoder: mailWordDecoder}
	if addr, err := parser.Parse(h.Get("From")); err == nil {
		out.From = addr.Address
		out.FromName = addr.Name
	} else {
		out.From = strings.TrimSpace(h.Get("From"))
	}
	out.MessageID = strings.Trim(strings.TrimSpace(h.Get("Message-Id")), "<>")
	if d, err := h.Date(); err == nil {
		out.Date, out.HasDate = d.Local(), true
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return out, fmt.Errorf("тело письма не прочитано: %w", err)
	}
	walkMailPart(&out, h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), h.Get("Content-Disposition"), body, 0)
	return out, nil
}

// walkMailPart собирает первый text/plain и первый text/html письма, проходя
// multipart рекурсивно. Вложения (Content-Disposition: attachment) и прочие
// типы пропускаются.
func walkMailPart(out *receivedMail, contentType, transfer, disposition string, body []byte, depth int) {
	if depth > 10 {
		return
	}
	if disp, _, err := mime.ParseMediaType(disposition); err == nil && strings.EqualFold(disp, "attachment") {
		return
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType, params = "text/plain", map[string]string{}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := mr.NextRawPart()
			if err != nil {
				return
			}
			partBody, err := io.ReadAll(part)
			if err != nil {
				return
			}
			walkMailPart(out, part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"),
				part.Header.Get("Content-Disposition"), partBody, depth+1)
		}
	}
	switch mediaType {
	case "text/plain":
		if out.Text == "" {
			out.Text = decodeMailCharset(decodeTransfer(body, transfer), params["charset"])
		}
	case "text/html":
		if out.HTML == "" {
			out.HTML = decodeMailCharset(decodeTransfer(body, transfer), params["charset"])
		}
	}
}
