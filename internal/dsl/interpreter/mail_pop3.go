package interpreter

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// receivePOP3 забирает письма по POP3 (RFC 1939) без удаления с сервера:
// UIDL — ключ «уже получено», LIST — размер до загрузки, RETR — само письмо.
// DELE не отправляется никогда.
func receivePOP3(p mailProfile, opts mailReceiveOptions) ([]receivedMail, error) {
	c, err := dialMail(p)
	if err != nil {
		return nil, err
	}
	defer c.close()
	if _, err := c.pop3Status(); err != nil {
		return nil, fmt.Errorf("приветствие POP3: %w", err)
	}
	if p.security == "starttls" {
		if err := c.pop3Cmd("STLS"); err != nil {
			return nil, fmt.Errorf("STLS: %w", err)
		}
		if err := c.startTLS(p.host); err != nil {
			return nil, err
		}
	}
	if err := c.pop3Cmd("USER " + p.user); err != nil {
		return nil, fmt.Errorf("сервер отклонил пользователя %s: %w", p.user, err)
	}
	// Ответ на PASS в текст ошибки не вставляем: часть серверов повторяет в
	// нём введённое.
	if err := c.writeLine("PASS " + p.password); err != nil {
		return nil, err
	}
	if _, err := c.pop3Status(); err != nil {
		return nil, fmt.Errorf("сервер отклонил вход пользователя %s", p.user)
	}

	uidls, err := c.pop3Multi("UIDL")
	if err != nil {
		return nil, fmt.Errorf("UIDL: %w", err)
	}
	sizes := map[string]int64{}
	if lines, err := c.pop3Multi("LIST"); err == nil {
		for _, l := range lines {
			f := strings.Fields(l)
			if len(f) >= 2 {
				if n, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					sizes[f[0]] = n
				}
			}
		}
	}
	var out []receivedMail
	for _, l := range uidls {
		if len(out) >= opts.max {
			break
		}
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		num, uid := f[0], f[1]
		if opts.skip[uid] {
			continue
		}
		if size, ok := sizes[num]; ok && size > opts.maxSize {
			continue
		}
		lines, err := c.pop3Multi("RETR " + num)
		if err != nil {
			return out, fmt.Errorf("RETR %s: %w", num, err)
		}
		raw := []byte(strings.Join(lines, "\r\n") + "\r\n")
		if int64(len(raw)) > opts.maxSize {
			continue
		}
		m, err := parseMailMessage(raw)
		if err != nil {
			return out, err
		}
		m.ID = uid
		out = append(out, m)
	}
	_ = c.writeLine("QUIT")
	return out, nil
}

// pop3Status читает однострочный ответ: +OK — успех, -ERR — ошибка.
func (c *mailConn) pop3Status() (string, error) {
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(line, "+OK") {
		return line, nil
	}
	return "", errors.New(serverText(strings.TrimPrefix(line, "-ERR")))
}

func (c *mailConn) pop3Cmd(cmd string) error {
	if err := c.writeLine(cmd); err != nil {
		return err
	}
	_, err := c.pop3Status()
	return err
}

// pop3Multi — команда с многострочным ответом до строки «.», с удалением
// точки-заполнителя (dot-stuffing) у строк, начинающихся с «..».
func (c *mailConn) pop3Multi(cmd string) ([]string, error) {
	if err := c.pop3Cmd(cmd); err != nil {
		return nil, err
	}
	var lines []string
	var total int
	for {
		line, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if line == "." {
			return lines, nil
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		total += len(line) + 2
		if int64(total) > mailReceiveMaxSize+int64(mailReceiveMaxLine) {
			return nil, errors.New("ответ сервера превышает предел размера")
		}
		lines = append(lines, line)
	}
}
