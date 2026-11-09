package interpreter

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// receiveIMAP забирает письма по IMAP4rev1 (RFC 3501): SELECT папки,
// UID SEARCH, размер (RFC822.SIZE) до загрузки, тело — BODY.PEEK[]: PEEK
// не ставит \Seen, письмо остаётся непрочитанным, пока об этом не попросили
// (ОтметитьПрочитанными). Письма не удаляются.
func receiveIMAP(ctx context.Context, p mailProfile, opts mailReceiveOptions) ([]receivedMail, error) {
	c, err := dialMail(ctx, p)
	if err != nil {
		return nil, err
	}
	defer c.close()
	greeting, err := c.readLine()
	if err != nil {
		return nil, fmt.Errorf("приветствие IMAP: %w", err)
	}
	if !strings.HasPrefix(greeting, "* OK") && !strings.HasPrefix(greeting, "* PREAUTH") {
		return nil, fmt.Errorf("приветствие IMAP: %s", serverText(greeting))
	}
	s := &imapSession{c: c}
	if p.security == "starttls" {
		if _, err := s.run("STARTTLS"); err != nil {
			return nil, fmt.Errorf("STARTTLS: %w", err)
		}
		if err := c.startTLS(p.host); err != nil {
			return nil, err
		}
	}
	if !strings.HasPrefix(greeting, "* PREAUTH") {
		// Ответ на LOGIN в текст ошибки не вставляем: он может повторять
		// введённое.
		if _, err := s.run("LOGIN", imapString(p.user), imapString(p.password)); err != nil {
			return nil, fmt.Errorf("сервер отклонил вход пользователя %s", p.user)
		}
	}
	selectLines, err := s.run("SELECT", imapString(imapMailboxName(p.folder)))
	if err != nil {
		return nil, fmt.Errorf("папка %s: %w", p.folder, err)
	}
	validity, err := imapUIDValidity(selectLines)
	if err != nil {
		return nil, err
	}
	prefix := validity + ":"
	var out []receivedMail
	// SEARCH читается потоком; в памяти остаётся только следующая порция
	// подходящих UID. Если письма оказались слишком большими, продолжим за
	// последним UID порции, не загружая список всего ящика.
	for next := uint64(1); len(out) < opts.max && next <= 1<<32-1; {
		uids, err := s.searchUIDs(next, opts.max-len(out), prefix, opts.skip)
		if err != nil {
			return out, fmt.Errorf("UID SEARCH: %w", err)
		}
		if len(uids) == 0 {
			break
		}
		for _, n := range uids {
			next = uint64(n) + 1
			uid := strconv.FormatUint(uint64(n), 10)
			sizeLines, err := s.run("UID FETCH " + uid + " (RFC822.SIZE)")
			if err != nil {
				return out, fmt.Errorf("UID FETCH %s: %w", uid, err)
			}
			if size, ok := imapFetchSize(sizeLines); ok && size > opts.maxSize {
				continue
			}
			bodyLines, err := s.run("UID FETCH " + uid + " (BODY.PEEK[])")
			if err != nil {
				return out, fmt.Errorf("UID FETCH %s: %w", uid, err)
			}
			raw := imapFetchBody(bodyLines)
			if raw == nil || int64(len(raw)) > opts.maxSize {
				continue
			}
			m, err := parseMailMessage(raw)
			if err != nil {
				return out, err
			}
			m.ID = prefix + uid
			out = append(out, m)
			if opts.markSeen {
				if _, err := s.run("UID STORE " + uid + ` +FLAGS (\Seen)`); err != nil {
					return out, fmt.Errorf("UID STORE %s: %w", uid, err)
				}
			}
		}
	}
	_, _ = s.run("LOGOUT")
	return out, nil
}

var imapValidityRe = regexp.MustCompile(`(?i)\[UIDVALIDITY ([0-9]+)\]`)

func imapUIDValidity(lines []imapLine) (string, error) {
	var validity string
	for _, l := range lines {
		for _, m := range imapValidityRe.FindAllStringSubmatch(l.text, -1) {
			n, err := strconv.ParseUint(m[1], 10, 32)
			if err != nil || n == 0 {
				return "", errors.New("неверный UIDVALIDITY папки IMAP")
			}
			v := strconv.FormatUint(n, 10)
			if validity != "" && validity != v {
				return "", errors.New("неоднозначный UIDVALIDITY папки IMAP")
			}
			validity = v
		}
	}
	if validity == "" {
		return "", errors.New("сервер не вернул UIDVALIDITY папки IMAP")
	}
	return validity, nil
}

func (s *imapSession) searchUIDs(first uint64, limit int, prefix string, skip map[string]bool) ([]uint32, error) {
	uids := make([]uint32, 0, limit)
	collect := func(uid uint32) {
		if uint64(uid) < first || skip[prefix+strconv.FormatUint(uint64(uid), 10)] {
			return
		}
		// Не полагаемся на порядок SEARCH и не допускаем дублей.
		i := sort.Search(len(uids), func(i int) bool { return uids[i] >= uid })
		if i < len(uids) && uids[i] == uid || i >= limit {
			return
		}
		if len(uids) < limit {
			uids = append(uids, 0)
		}
		copy(uids[i+1:], uids[i:len(uids)-1])
		uids[i] = uid
	}
	_, err := s.runWithSearch("UID SEARCH UID "+strconv.FormatUint(first, 10)+":4294967295", collect)
	return uids, err
}

// imapMailboxName encodes IMAP4rev1 mailbox names as modified UTF-7
// (RFC 3501 section 5.1.3). Credentials still use ordinary IMAP strings.
func imapMailboxName(name string) string {
	var out strings.Builder
	var encoded []uint16
	flush := func() {
		if len(encoded) == 0 {
			return
		}
		data := make([]byte, 0, 2*len(encoded))
		for _, u := range encoded {
			data = binary.BigEndian.AppendUint16(data, u)
		}
		out.WriteByte('&')
		out.WriteString(strings.ReplaceAll(base64.RawStdEncoding.EncodeToString(data), "/", ","))
		out.WriteByte('-')
		encoded = encoded[:0]
	}
	for _, r := range name {
		if r >= 0x20 && r <= 0x7e {
			flush()
			out.WriteRune(r)
			if r == '&' {
				out.WriteByte('-')
			}
		} else {
			encoded = utf16.AppendRune(encoded, r)
		}
	}
	flush()
	return out.String()
}

// imapArg — аргумент команды: строка в кавычках или литерал {n} для значений
// с не-ASCII символами (кавычки по RFC 3501 — только 7-битные символы).
type imapArg struct {
	text    string
	literal bool
}

func imapString(s string) imapArg {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return imapArg{text: s, literal: true}
		}
	}
	return imapArg{text: `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`}
}

type imapLine struct {
	text     string
	literals [][]byte
}

type imapSession struct {
	c   *mailConn
	seq int
}

var imapLiteralTail = regexp.MustCompile(`\{(\d+)\}$`)

// run отправляет команду и читает ответ до строки со своим тегом. Строки
// без тега возвращаются вместе с литералами {n}, прочитанными побайтно.
func (s *imapSession) run(cmd string, args ...any) ([]imapLine, error) {
	return s.runWithSearch(cmd, nil, args...)
}

func (s *imapSession) runWithSearch(cmd string, collect func(uint32), args ...any) ([]imapLine, error) {
	s.seq++
	tag := "A" + strconv.Itoa(s.seq)
	line := tag + " " + cmd
	for _, a := range args {
		arg, _ := a.(imapArg)
		if !arg.literal {
			line += " " + arg.text
			continue
		}
		// Литерал: «{n}», ждём продолжения «+», затем сами байты.
		if err := s.c.writeLine(line + " {" + strconv.Itoa(len(arg.text)) + "}"); err != nil {
			return nil, err
		}
		cont, err := s.c.readLine()
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(cont, "+") {
			return nil, errors.New(serverText(cont))
		}
		line = arg.text
	}
	if err := s.c.writeLine(line); err != nil {
		return nil, err
	}
	var out []imapLine
	for {
		text, err := s.c.readIMAPLine(collect)
		if err != nil {
			return nil, err
		}
		cur := imapLine{text: text}
		for {
			m := imapLiteralTail.FindStringSubmatch(text)
			if m == nil {
				break
			}
			n, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil || n < 0 || n > mailReceiveMaxSize+int64(mailReceiveMaxLine) {
				return nil, errors.New("литерал ответа сервера превышает предел размера")
			}
			lit, err := s.c.readN(n)
			if err != nil {
				return nil, err
			}
			cur.literals = append(cur.literals, lit)
			text, err = s.c.readLine()
			if err != nil {
				return nil, err
			}
			cur.text += " " + text
		}
		if strings.HasPrefix(cur.text, tag+" ") {
			status := strings.TrimPrefix(cur.text, tag+" ")
			if strings.HasPrefix(strings.ToUpper(status), "OK") {
				return out, nil
			}
			return out, errors.New(serverText(status))
		}
		out = append(out, cur)
	}
}

var imapSizeRe = regexp.MustCompile(`(?i)RFC822\.SIZE (\d+)`)

func imapFetchSize(lines []imapLine) (int64, bool) {
	for _, l := range lines {
		if m := imapSizeRe.FindStringSubmatch(l.text); m != nil {
			if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func imapFetchBody(lines []imapLine) []byte {
	for _, l := range lines {
		if strings.Contains(strings.ToUpper(l.text), "FETCH") && len(l.literals) > 0 {
			return l.literals[0]
		}
	}
	return nil
}
