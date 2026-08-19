package interpreter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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

// Бюджет старта дерева процессов — не часть проверяемой гарантии. Тест
// доказывает, что потомок УМИРАЕТ после отмены; сколько он стартовал, значения
// не имеет. Между тем старт стоит дорого и цена зависит от машины: помощник и
// его потомок — два отдельных запуска тестового бинаря, а на Windows первый
// запуск свежесобранного .exe уходит на антивирусную проверку файла (замерено
// 6,9 с против 0,15 с у второго запуска того же файла). Под параллельной
// сборкой и прогоном соседних пакетов цена растёт дальше. Прежние 8 секунд
// ложились в неё впритык, и тест краснел на загруженной машине, ничего не
// сообщая о настоящей гарантии (#1038).
//
// Время жизни помощников заведомо больше бюджета старта: иначе медленный старт
// съедает их жизнь, помощник выходит сам, отмене нечего убивать — и тест падает
// уже на «ожидалась ошибка отмены», уводя разбор в сторону.
const (
	execTreeStartBudget = 45 * time.Second
	execTreeHelperLife  = 90 * time.Second
)

func TestExecuteCommand_ExecutionContextCancelsDescendantTree(t *testing.T) {
	heartbeat := t.TempDir() + string(os.PathSeparator) + "descendant-heartbeat"
	args := NewArray([]any{
		"-test.run=^TestExecProcessTreeHelper$",
		"--",
		"onebase-exec-tree-helper",
		"parent",
		heartbeat,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type cancellationPoint struct {
		heartbeat string
		at        time.Time
	}
	canceled := make(chan cancellationPoint, 1)
	started := time.Now()
	go func() {
		deadline := started.Add(execTreeStartBudget)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(heartbeat)
			if value := strings.TrimSpace(string(data)); err == nil && value != "" {
				point := cancellationPoint{heartbeat: value, at: time.Now()}
				cancel()
				canceled <- point
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		canceled <- cancellationPoint{}
	}()

	_, err := execRunner(nil, NewStaticCtx(ctx))([]any{os.Args[0], args, 20.0}, "", 0)
	point := <-canceled
	if point.heartbeat == "" {
		t.Fatalf("descendant did not start within %s (process-tree startup budget, see #1038)", execTreeStartBudget)
	}
	// Замер в журнал: если бюджета однажды перестанет хватать, следующий человек
	// увидит настоящую цену старта, а не будет добывать её заново.
	t.Logf("дерево процессов стартовало за %s (бюджет %s)", point.at.Sub(started).Round(time.Millisecond), execTreeStartBudget)
	if err == nil {
		t.Fatal("expected execution context cancellation error")
	}
	if elapsed := time.Since(point.at); elapsed > 3*time.Second {
		t.Fatalf("descendant-held output pipe outlived cancellation: %v", elapsed)
	}

	// Poll instead of taking one delayed snapshot: killing the child while
	// os.WriteFile is between truncate and write can legitimately leave an
	// empty file. A surviving child will publish another non-empty sequence on
	// its next 40 ms tick, which this window must observe.
	stableUntil := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(stableUntil) {
		second, readErr := os.ReadFile(heartbeat)
		if readErr != nil {
			t.Fatalf("read descendant heartbeat after cancellation: %v", readErr)
		}
		if value := strings.TrimSpace(string(second)); value != "" && value != point.heartbeat {
			t.Fatalf("descendant survived command cancellation: heartbeat advanced from %q to %q", point.heartbeat, value)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestExecProcessTreeHelper runs in subprocesses created by the regression
// above. The parent intentionally does not wait for its child, while the child
// retains stdout/stderr and updates a heartbeat. Killing only the direct parent
// therefore leaves both a live descendant and os/exec copy goroutines blocked.
func TestExecProcessTreeHelper(t *testing.T) {
	separator := -1
	for i, arg := range os.Args {
		if arg == "onebase-exec-tree-helper" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return
	}
	if len(os.Args) < separator+3 {
		t.Fatal("process-tree helper requires mode and heartbeat path")
	}
	mode, heartbeat := os.Args[separator+1], os.Args[separator+2]
	switch mode {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestExecProcessTreeHelper$", "--", "onebase-exec-tree-helper", "child", heartbeat) //nolint:gosec // G204: fixed test binary and arguments; heartbeat is a t.TempDir path
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			t.Fatalf("start process-tree child: %v", err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "descendant pid=%d\n", child.Process.Pid)
		time.Sleep(execTreeHelperLife)
	case "child":
		deadline := time.Now().Add(execTreeHelperLife)
		for sequence := 1; time.Now().Before(deadline); sequence++ {
			if err := os.WriteFile(heartbeat, []byte(strconv.Itoa(sequence)), 0o600); err != nil { //nolint:gosec // G703: subprocess test helper writes only the t.TempDir path supplied by its parent
				t.Fatalf("write process-tree heartbeat: %v", err)
			}
			time.Sleep(40 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown process-tree helper mode %q", mode)
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