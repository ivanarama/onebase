package interpreter

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// execRunner достаёт builtin ВыполнитьКоманду с заданным guard'ом.
func execRunner(guard ExecGuard, ctxSources ...CtxSource) BuiltinFunc {
	return NewExecFunctions(guard, nil, ctxSources...)["ВыполнитьКоманду"].(BuiltinFunc)
}

// echoCmd возвращает кросс-платформенную команду echo для аргумента arg.
func echoCmd(arg string) (string, *Array) {
	if runtime.GOOS == "windows" {
		return "cmd", NewArray([]any{"/c", "echo", arg})
	}
	return "echo", NewArray([]any{arg})
}

func TestExecuteCommand_SuccessNoShell(t *testing.T) {
	// Аргумент с shell-метасимволами должен попасть в вывод ДОСЛОВНО —
	// доказывает, что инъекции нет (команда исполняется без shell).
	inj := "$(whoami)"
	cmd, args := echoCmd(inj)
	res, err := execRunner(nil)([]any{cmd, args}, "", 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	m := res.(*MapThis)
	if code, _ := m.Get("КодВозврата").(float64); code != 0 {
		t.Errorf("КодВозврата = %v, ожидался 0", m.Get("КодВозврата"))
	}
	out, _ := m.Get("СтандартныйВывод").(string)
	if !strings.Contains(out, "whoami") {
		t.Errorf("вывод не содержит литерал аргумента: %q", out)
	}
	if !strings.Contains(out, "$(") {
		t.Errorf("shell-метасимволы были интерпретированы (инъекция!): %q", out)
	}
	if fin, _ := m.Get("Завершилась").(bool); !fin {
		t.Errorf("Завершилась должно быть Истина для быстрой команды")
	}
}

func TestExecuteCommand_DeniedByGuard(t *testing.T) {
	deny := ExecGuard(func() error { return errExecDeniedTest })
	mustPanicUser(t, func() {
		cmd, args := echoCmd("x")
		_, _ = execRunner(deny)([]any{cmd, args}, "", 0)
	})
}

func TestExecuteCommand_RestrictedProfileDenies(t *testing.T) {
	v, ok := RestrictedProfile().Vars()["ВыполнитьКоманду"]
	if !ok {
		t.Fatal("RestrictedProfile должен подменять ВыполнитьКоманду deny-заглушкой")
	}
	mustPanicUser(t, func() { _, _ = v.(BuiltinFunc)([]any{"echo"}, "", 0) })
}

func TestExecuteCommand_Timeout(t *testing.T) {
	var cmd string
	var args *Array
	if runtime.GOOS == "windows" {
		cmd, args = "ping", NewArray([]any{"-n", "6", "127.0.0.1"})
	} else {
		cmd, args = "sleep", NewArray([]any{"6"})
	}
	start := time.Now()
	res, err := execRunner(nil)([]any{cmd, args, 1.0}, "", 0) // таймаут 1с
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("таймаут не сработал, прошло %v", elapsed)
	}
	if fin, _ := res.(*MapThis).Get("Завершилась").(bool); fin {
		t.Errorf("ожидалось Завершилась=false (убито по таймауту)")
	}
}

func TestExecuteCommand_ExecutionContextCancelsProcess(t *testing.T) {
	var cmd string
	var args *Array
	if runtime.GOOS == "windows" {
		cmd, args = "ping", NewArray([]any{"-n", "6", "127.0.0.1"})
	} else {
		cmd, args = "sleep", NewArray([]any{"6"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := execRunner(nil, NewStaticCtx(ctx))([]any{cmd, args, 10.0}, "", 0)
	if err == nil {
		t.Fatal("expected execution context deadline error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "врем") {
		t.Fatalf("unexpected deadline error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("execution context did not stop process, elapsed %v", elapsed)
	}
}

var errExecDeniedTest = &execTestErr{}

type execTestErr struct{}

func (*execTestErr) Error() string { return "запрещено (тест)" }

// mustPanicUser проверяет, что fn паникует (userError запрета).
func mustPanicUser(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("ожидалась паника-запрет")
		}
	}()
	fn()
}
