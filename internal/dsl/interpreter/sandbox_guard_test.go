package interpreter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deny-guard блокирует файловую операцию до реального доступа к ФС.
func TestNewFileFunctions_GuardBlocks(t *testing.T) {
	deny := FileGuard(func() error { return errors.New("файлы запрещены") })
	m := NewFileFunctions(deny)
	msg := callBuiltinExpectPanic(t, m["копироватьфайл"], []any{"a.txt", "b.txt"})
	if !strings.Contains(msg, "файлы запрещены") {
		t.Errorf("ожидалось сообщение guard'а, получено %q", msg)
	}
}

func TestGuardedFile_PreservesCauseAndCallLocation(t *testing.T) {
	sentinel := errors.New("файлы запрещены")
	fn := guardedFile(
		FileGuard(func() error { return sentinel }),
		BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
			t.Fatal("файловая функция вызвана после deny")
			return nil, nil
		}),
	)
	defer func() {
		r := recover()
		ue, ok := r.(userError)
		if !ok {
			t.Fatalf("ожидалась userError, получено %T: %v", r, r)
		}
		if ue.File != "module.os" || ue.Line != 17 {
			t.Fatalf("позиция = %s:%d, ожидалась module.os:17", ue.File, ue.Line)
		}
		if !errors.Is(ue.Err, sentinel) {
			t.Fatalf("исходная ошибка потеряна: %v", ue.Err)
		}
	}()
	_, _ = fn(nil, "module.os", 17)
}

func TestNewFileFunctions_FactoryKeepsGuardBehavior(t *testing.T) {
	sentinel := errors.New("файлы запрещены")
	factory, ok := NewFileFunctions(FileGuard(func() error { return sentinel }))["__factory_ЧтениеТекста"].(func([]any) any)
	if !ok {
		t.Fatal("фабрика ЧтениеТекста изменила сигнатуру")
	}
	defer func() {
		r := recover()
		ue, ok := r.(userError)
		if !ok {
			t.Fatalf("ожидалась userError, получено %T: %v", r, r)
		}
		if ue.File != "" || ue.Line != 0 {
			t.Fatalf("фабрика неожиданно получила позицию: %s:%d", ue.File, ue.Line)
		}
		if !errors.Is(ue.Err, sentinel) {
			t.Fatalf("исходная ошибка фабрики потеряна: %v", ue.Err)
		}
	}()
	factory([]any{"ignored.txt"})
}

func TestSandboxOverlayIsUnaffectedByEnvWriters(t *testing.T) {
	e := newEnv(nil)
	applySandboxVars(e, SandboxProfile{DenyFile: true})
	const name = "GetTempFileName"
	assertDeny := func(stage string) {
		t.Helper()
		got, ok := e.get(name)
		if !ok {
			t.Fatalf("%s: overlay-имя потеряно", stage)
		}
		if got == "shadow" {
			t.Fatalf("%s: пользовательское значение затенило overlay", stage)
		}
		if _, ok := got.(BuiltinFunc); !ok {
			t.Fatalf("%s: overlay вернул %T, ожидалась BuiltinFunc", stage, got)
		}
	}

	assertDeny("initial")
	e.set(name, "shadow")
	assertDeny("set")
	e.setLocal(name, "shadow")
	assertDeny("setLocal")
	e.declare(name, "shadow")
	assertDeny("declare")
	e.declareModule(name, "shadow")
	assertDeny("declareModule")
	restore := publishTemp(e, map[string]any{name: "shadow"})
	assertDeny("publishTemp")
	restore()
	assertDeny("publishTemp restore")
}

// nil-guard не блокирует: копирование реального файла проходит.
func TestNewFileFunctions_NilGuardAllows(t *testing.T) {
	SetFileSandbox("")
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.txt")
	fn, ok := NewFileFunctions(nil)["копироватьфайл"].(BuiltinFunc)
	if !ok {
		t.Fatal("копироватьфайл должна быть BuiltinFunc")
	}
	if _, err := fn([]any{src, dst}, "", 0); err != nil {
		t.Fatalf("nil-guard не должен блокировать: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("файл должен быть скопирован: %v", err)
	}
}
