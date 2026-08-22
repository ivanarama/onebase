package interpreter

// Тест предохранителя сети на уровне DSL-builtins (план 62): при guard,
// возвращающем ошибку, сетевые функции отказывают до выполнения запроса.

import (
	"errors"
	"strings"
	"testing"
)

var errLocked = errors.New("сетевые возможности отключены предохранителем")

func lockedGuard() error { return errLocked }

// callBuiltinExpectPanic вызывает builtin и ловит userError-панику.
func callBuiltinExpectPanic(t *testing.T, fn any, args []any) string {
	t.Helper()
	bf, ok := fn.(BuiltinFunc)
	if !ok {
		t.Fatalf("ожидался BuiltinFunc, получен %T", fn)
	}
	var msg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				if ue, ok := r.(userError); ok {
					msg = ue.Msg
					return
				}
				t.Fatalf("ожидалась userError, получено %T: %v", r, r)
			}
		}()
		_, _ = bf(args, "test.os", 1)
		t.Fatal("ожидалась паника (сеть заблокирована), но вызов прошёл")
	}()
	return msg
}

func TestHTTPBuiltins_BlockedByGuard(t *testing.T) {
	m := NewHTTPFunctions(lockedGuard)

	for _, tc := range []struct {
		name string
		args []any
	}{
		{name: "HTTPПолучить", args: []any{"http://example.com"}},
		{name: "HTTPПолучитьБезопасно", args: []any{"http://8.8.8.8", "8.8.8.8"}},
		{name: "HTTPОтправить", args: []any{"http://example.com", "{}"}},
	} {
		msg := callBuiltinExpectPanic(t, m[tc.name], tc.args)
		if !strings.Contains(msg, "предохранител") {
			t.Errorf("%s: ожидалось сообщение про предохранитель, получено %q", tc.name, msg)
		}
	}
}

func TestEmailBuiltins_BlockedByGuard(t *testing.T) {
	m := NewEmailFunctions(nil, lockedGuard)
	// ОтправитьПисьмо должен упасть на guard ДО проверки «email не настроен».
	msg := callBuiltinExpectPanic(t, m["ОтправитьПисьмо"], []any{"a@b.c", "тема", "текст"})
	if !strings.Contains(msg, "предохранител") {
		t.Errorf("ожидалось сообщение про предохранитель, получено %q", msg)
	}
}

func TestHTTPBuiltins_NilGuardAllows(t *testing.T) {
	// nil-guard не блокирует: вызов дойдёт до реального HTTP и упадёт на сети,
	// а не на предохранителе (проверяем, что сообщение НЕ про предохранитель).
	m := NewHTTPFunctions(nil)
	msg := callBuiltinExpectPanic(t, m["HTTPПолучить"], []any{"http://127.0.0.1:1/nope"})
	if strings.Contains(msg, "предохранител") {
		t.Errorf("при nil-guard блокировки быть не должно, получено %q", msg)
	}
}
