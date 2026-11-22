package dslvars

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

type mailLiveContext struct{ ctx context.Context }

func (s *mailLiveContext) Ctx() context.Context { return s.ctx }

func TestCommonBuild_MailUsesCurrentContext(t *testing.T) {
	for _, proto := range []string{"POP3", "IMAP"} {
		for _, live := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/live=%v", proto, live), func(t *testing.T) {
				db, reg, _ := constantsFixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				source := &mailLiveContext{ctx: context.Background()}
				common := Common{Ctx: ctx, Reg: reg, Store: db}
				if live {
					common.Ctx = context.Background()
					common.CtxSource = source
				}
				vars := common.Build()
				// Change the source after Build: the builtin must resolve it at call time.
				source.ctx = ctx
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = ln.Close() }()
				closed := make(chan struct{})
				go func() {
					defer close(closed)
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					defer func() { _ = conn.Close() }()
					_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
					cancel()
					_, _ = io.Copy(io.Discard, conn)
				}()
				host, port, _ := net.SplitHostPort(ln.Addr().String())
				n, _ := strconv.Atoi(port)
				src := fmt.Sprintf(`Функция Тест()
Профиль = Новый Структура("Протокол,Сервер,Порт,Пользователь,Пароль,Защита,Таймаут", "%s", "%s", %d, "tasks", "secret", "НЕТ", 5);
Возврат ПолучитьПисьма(Профиль);
КонецФункции`, proto, host, n)
				prog, err := parser.New(lexer.New(src, "mail.os")).ParseProgram()
				if err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				var got any
				err = interpreter.New().RunWithResult(prog.Procedures[0], nil, &got, vars)
				if err == nil || time.Since(start) > time.Second {
					t.Fatalf("host cancellation not honored promptly: %v", err)
				}
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("mail transport not closed")
				}
			})
		}
	}
}
