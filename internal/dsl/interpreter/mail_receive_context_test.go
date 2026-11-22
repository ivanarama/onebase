package interpreter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestReceiveMail_IMAPMailboxNames(t *testing.T) {
	for _, tc := range []struct{ folder, wire string }{
		{"INBOX", "INBOX"},
		{"Заявки", "&BBcEMARPBDIEOgQ4-"},
		{"R&D", "R&-D"},
		{"~peter/mail/台北/日本語", "~peter/mail/&U,BTFw-/&ZeVnLIqe-"},
		{"😀 &", "&2D3eAA- &-"},
	} {
		t.Run(tc.folder, func(t *testing.T) {
			srv := startFakeMail(t, "imap", nil, []string{mailPlain})
			vars := NewEmailFunctions(nil, nil)
			for k, v := range mailVars("IMAP", srv.addr, "НЕТ", "секрет-1", nil, 1, false) {
				vars[k] = v
			}
			vars["Папка"] = tc.folder
			src := strings.Replace(receiveSrc, "П = Новый Структура", "Профиль.Вставить(\"Папка\", Папка);\n П = Новый Структура", 1)
			var got any
			if err := New().RunWithResult(mailTestProc(t, src), nil, &got, vars); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(fmt.Sprint(got), "1001:100|plain-1@example.ru") {
				t.Fatalf("mail not received: %v", got)
			}
			if !strings.Contains(srv.all(), " SELECT \""+tc.wire+"\"\n") {
				t.Fatalf("mailbox %q not encoded as %q: %s", tc.folder, tc.wire, srv.all())
			}
		})
	}
}

// Every fixture accepts a real connection and stalls in a specific protocol
// phase. Client cancellation must close that connection, including TLS handshakes.
func stalledMailServer(t *testing.T, proto, security string) (string, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready, closed := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		defer close(closed)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		if security == "starttls" {
			greeting, command := "+OK ready\r\n", "STLS"
			if proto == "IMAP" {
				greeting, command = "* OK ready\r\n", "STARTTLS"
			}
			if _, err = io.WriteString(conn, greeting); err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil || !strings.Contains(line, command) {
				return
			}
			response := "+OK start TLS\r\n"
			if proto == "IMAP" {
				response = strings.Fields(line)[0] + " OK start TLS\r\n"
			}
			if _, err = io.WriteString(conn, response); err != nil {
				return
			}
		}
		if security == "session" {
			r := bufio.NewReader(conn)
			if proto == "POP3" {
				if _, err = io.WriteString(conn, "+OK ready\r\n"); err != nil {
					return
				}
				for range 2 { // USER and PASS
					if _, err = r.ReadString('\n'); err != nil {
						return
					}
					if _, err = io.WriteString(conn, "+OK\r\n"); err != nil {
						return
					}
				}
				if _, err = r.ReadString('\n'); err != nil {
					return
				} // UIDL
				if _, err = io.WriteString(conn, "+OK list\r\n1 stable-id\r\n"); err != nil {
					return
				}
			} else {
				if _, err = io.WriteString(conn, "* OK ready\r\n"); err != nil {
					return
				}
				for step := range 2 { // LOGIN and SELECT
					line, readErr := r.ReadString('\n')
					if readErr != nil {
						return
					}
					if step == 1 {
						if _, err = io.WriteString(conn, "* OK [UIDVALIDITY 1001] valid\r\n"); err != nil {
							return
						}
					}
					if _, err = io.WriteString(conn, strings.Fields(line)[0]+" OK done\r\n"); err != nil {
						return
					}
				}
				if _, err = r.ReadString('\n'); err != nil {
					return
				} // SEARCH response, incomplete line
				if _, err = io.WriteString(conn, "* SEARCH "); err != nil {
					return
				}
			}
		}
		close(ready)
		_, _ = io.Copy(io.Discard, conn)
	}()
	return ln.Addr().String(), ready, closed
}

func TestReceiveMail_ExecutionCancellation(t *testing.T) {
	for _, proto := range []string{"POP3", "IMAP"} {
		for _, security := range []string{"НЕТ", "tls", "starttls", "session"} {
			for _, limit := range []string{"cancel", "wall-clock"} {
				t.Run(proto+"/"+security+"/"+limit, func(t *testing.T) {
					addr, ready, closed := stalledMailServer(t, proto, security)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					profile := SandboxProfile{Context: ctx}
					if limit == "wall-clock" {
						profile.MaxWallClock = 100 * time.Millisecond
					} else {
						go func() {
							select {
							case <-ready:
								time.Sleep(30 * time.Millisecond)
								cancel()
							case <-ctx.Done():
							}
						}()
					}
					vars := NewEmailFunctions(nil, nil)
					profileSecurity := security
					if security == "session" {
						profileSecurity = "НЕТ"
					}
					for k, v := range mailVars(proto, addr, profileSecurity, "secret", nil, 1, false) {
						vars[k] = v
					}
					start := time.Now()
					var result any
					src := strings.Replace(receiveSrc, "\n\tПрофиль", "\n Попытка\n\tПрофиль", 1)
					src = strings.Replace(src, "\nКонецФункции", "\n Исключение\n Возврат \"caught\";\n КонецПопытки;\nКонецФункции", 1)
					if proto == "IMAP" {
						src = strings.ReplaceAll(src, "ПолучитьПисьма", "ReceiveEmails")
					}
					err := New().RunSandboxed(mailTestProc(t, src), nil, profile, &result, vars)
					want := context.Canceled
					if limit == "wall-clock" {
						want = errSandboxTimeout
					}
					if !errors.Is(err, want) {
						t.Fatalf("cancellation must bypass DSL catch, want %v: %v (result %v)", want, err, result)
					}
					if elapsed := time.Since(start); elapsed > time.Second {
						t.Fatalf("cancellation took %s (profile timeout 5 s): %v", elapsed, err)
					}
					select {
					case <-closed:
					case <-time.After(time.Second):
						t.Fatal("cancelled mailbox connection remains open")
					}
				})
			}
		}
	}
}
