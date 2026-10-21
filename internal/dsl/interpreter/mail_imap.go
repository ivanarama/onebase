package interpreter

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// receiveIMAP забирает письма по IMAP4rev1 (RFC 3501): SELECT папки,
// UID SEARCH ALL, размер (RFC822.SIZE) до загрузки, тело — BODY.PEEK[]: PEEK
// не ставит \Seen, письмо остаётся непрочитанным, пока об этом не попросили
// (ОтметитьПрочитанными). Письма не удаляются.
func receiveIMAP(p mailProfile, opts mailReceiveOptions) ([]receivedMail, error) {
	c, err := dialMail(p)
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
	if _, err := s.run("SELECT", imapString(p.folder)); err != nil {
		return nil, fmt.Errorf("папка %s: %w", p.folder, err)
	}
	lines, err := s.run("UID SEARCH ALL")
	if err != nil {
		return nil, fmt.Errorf("UID SEARCH: %w", err)
	}
	var uids []string
	for _, l := range lines {
		if strings.HasPrefix(strings.ToUpper(l.text), "* SEARCH") {
			uids = append(uids, strings.Fields(l.text)[2:]...)
		}
	}
	var out []receivedMail
	for _, uid := range uids {
		if len(out) >= opts.max {
			break
		}
		if opts.skip[uid] {
			continue
		}
		if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
			continue
		}
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
		m.ID = uid
		out = append(out, m)
		if opts.markSeen {
			if _, err := s.run("UID STORE " + uid + ` +FLAGS (\Seen)`); err != nil {
				return out, fmt.Errorf("UID STORE %s: %w", uid, err)
			}
		}
	}
	_, _ = s.run("LOGOUT")
	return out, nil
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
		text, err := s.c.readLine()
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
