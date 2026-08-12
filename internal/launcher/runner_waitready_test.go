package launcher

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunnerRecordExitDoesNotClobberReplacement(t *testing.T) {
	r := NewRunner()
	oldCmd := &exec.Cmd{}
	newCmd := &exec.Cmd{}
	r.procs["base"] = &managedProc{cmd: newCmd}

	// Поздний Wait старого процесса после Restart не должен удалить уже
	// запущенную замену и пометить её завершившейся.
	r.recordExit("base", oldCmd)
	if got := r.procs["base"]; got == nil || got.cmd != newCmd {
		t.Fatal("завершение старого процесса удалило новый процесс")
	}
	if r.exits["base"] {
		t.Fatal("завершение старого процесса пометило новый как упавший")
	}

	r.recordExit("base", newCmd)
	if _, ok := r.procs["base"]; ok {
		t.Fatal("завершившийся текущий процесс остался в procs")
	}
	if !r.exits["base"] {
		t.Fatal("завершение текущего процесса не зафиксировано")
	}
}

func TestWaitReady_AdoptedTokenDoesNotTrustPublicHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	base := &Base{ID: "adopted-auth", Name: "adopted-auth", Port: port, ControlToken: "secret"}

	if err := NewRunner().WaitReady(base, 300*time.Millisecond); err == nil {
		t.Fatal("public health from a listener without HMAC identity was accepted")
	}
}

// waitReadyFreePort возвращает свободный локальный порт (и сразу освобождает его).
func waitReadyFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()

	if got := tailFile(filepath.Join(dir, "нет.log"), 5); got != "" {
		t.Errorf("несуществующий файл: ожидалась пустая строка, получено %q", got)
	}
	if got := tailFile("", 5); got != "" {
		t.Errorf("пустой путь: ожидалась пустая строка, получено %q", got)
	}

	p := filepath.Join(dir, "base.log")
	if err := os.WriteFile(p, []byte("строка1\r\nстрока2\r\nстрока3\r\nстрока4\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := tailFile(p, 2)
	if got != "строка3\nстрока4" {
		t.Errorf("последние 2 строки: получено %q", got)
	}

	// Файл больше 8 КБ: хвост читается, обрезанная первая строка отбрасывается.
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "line %04d\n", i)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got = tailFile(p, 3)
	if got != "line 1997\nline 1998\nline 1999" {
		t.Errorf("хвост большого файла: получено %q", got)
	}
}

// Процесс базы завершился, не начав слушать порт, — ошибка немедленная и
// содержит причину из конца лога, а не «сервер не ответил за 15s».
func TestWaitReady_ProcessDiedShowsLogTail(t *testing.T) {
	logsDirOverride = t.TempDir()
	t.Cleanup(func() { logsDirOverride = "" })

	base := &Base{ID: "died", Name: "died", Port: waitReadyFreePort(t)}
	logText := "запуск...\nload project: нет каталога documents\n"
	if err := os.WriteFile(filepath.Join(logsDirOverride, base.ID+".log"), []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRunner()
	r.exits[base.ID] = true // процесс успел упасть ещё до входа в WaitReady

	start := time.Now()
	err := r.WaitReady(base, 15*time.Second)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ошибка о падении должна быть немедленной, ждали %s", elapsed)
	}
	if !strings.Contains(err.Error(), "завершился при запуске") {
		t.Errorf("ошибка не говорит о падении процесса: %v", err)
	}
	if !strings.Contains(err.Error(), "нет каталога documents") {
		t.Errorf("в ошибке нет причины из лога: %v", err)
	}
}

// Живому процессу дают startupGraceTimeout, а не переданный таймаут: первый
// запуск мигрирует схему БД до открытия порта и легко превышает 15 секунд.
func TestWaitReady_AliveProcessGetsGrace(t *testing.T) {
	oldGrace := startupGraceTimeout
	startupGraceTimeout = 700 * time.Millisecond
	t.Cleanup(func() { startupGraceTimeout = oldGrace })
	logsDirOverride = t.TempDir()
	t.Cleanup(func() { logsDirOverride = "" })

	base := &Base{ID: "slow", Name: "slow", Port: waitReadyFreePort(t)}
	r := NewRunner()
	r.procs[base.ID] = &managedProc{port: base.Port} // жив, но порт ещё не слушает

	start := time.Now()
	err := r.WaitReady(base, 100*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("порт никто не слушает — ожидалась ошибка")
	}
	if elapsed < 600*time.Millisecond {
		t.Fatalf("живому процессу должны дать grace-таймаут, вышли через %s", elapsed)
	}
	if !strings.Contains(err.Error(), "процесс ещё работает") {
		t.Errorf("ошибка должна объяснять, что процесс жив и запускается: %v", err)
	}
}

// Для «усыновлённых» баз (не запускались этим лаунчером) поведение прежнее:
// переданный таймаут и старое сообщение.
func TestWaitReady_UntrackedKeepsPlainTimeout(t *testing.T) {
	base := &Base{ID: "adopted", Name: "adopted", Port: waitReadyFreePort(t)}
	r := NewRunner()

	start := time.Now()
	err := r.WaitReady(base, 300*time.Millisecond)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("чужая база не должна ждать grace-таймаут, ждали %s", elapsed)
	}
	if !strings.Contains(err.Error(), "сервер не ответил на порту") {
		t.Errorf("для чужой базы ожидалось прежнее сообщение: %v", err)
	}
}
