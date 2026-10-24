package devserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSupervisorHelperProcess — «сервер» для тестов супервизора: живёт, пока его
// не остановят. Запускается не тестовым раннером, а через os.Args[0] (приём из
// os/exec): нужен настоящий процесс, чтобы проверять его остановку.
func TestSupervisorHelperProcess(t *testing.T) {
	if os.Getenv("ONEBASE_DEVSERVER_HELPER") != "1" {
		t.Skip("вспомогательный процесс запускается супервизором, а не раннером")
	}
	// Only a decimal port crosses the process boundary; the peer is always
	// our parent's IPv4 loopback listener, never an environment-supplied host.
	portText := os.Getenv("ONEBASE_DEVSERVER_HELPER_PORT")
	for _, digit := range portText {
		if digit < '0' || digit > '9' {
			t.Fatal("недопустимый порт helper-процесса")
		}
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		t.Fatal("недопустимый порт helper-процесса")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp4", "127.0.0.1:"+strconv.FormatUint(port, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = conn.Close() // Best-effort cleanup; protocol I/O errors are checked below.
	}()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		if scanner.Text() != "ping" {
			t.Fatal("неизвестный запрос helper-процессу")
		}
		if _, err := fmt.Fprintln(conn, os.Getpid()); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func supervisorHelperCommand(ctx context.Context, port string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSupervisorHelperProcess$") //nolint:gosec // G204: тест запускает собственный бинарь
	cmd.Env = append(os.Environ(), "ONEBASE_DEVSERVER_HELPER=1", "ONEBASE_DEVSERVER_HELPER_PORT="+port)
	return cmd
}

// Exercise the actual subprocess entry point: invalid input must fail before
// attempting a connection, including hosts/URLs that the former env accepted.
func TestSupervisorHelperRejectsInvalidPort(t *testing.T) {
	for _, port := range []string{"", "abc", "0", "65536", "-1", "+1", " 1", "1 ", "127.0.0.1:80", "localhost:80", "http://127.0.0.1:80"} {
		t.Run(port, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := supervisorHelperCommand(ctx, port)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("helper не отклонил порт: err=%v, output=%s", err, out)
			}
			if !strings.Contains(string(out), "недопустимый порт helper-процесса") {
				t.Fatalf("helper не проверил порт перед подключением: %s", out)
			}
		})
	}
}

// helperSupervisor собирает супервизор с подменёнными сборкой и запуском: обе
// операции ходят наружу (go build, exec), и в тесте важна логика вокруг них.
func helperSupervisor(t *testing.T, srcDir string) (*Supervisor, *recorder) {
	t.Helper()
	rec := &recorder{}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close() // Best-effort cleanup unblocks any pending Accept.
		rec.mu.Lock()
		defer rec.mu.Unlock()
		for _, p := range rec.procs {
			// Run owns the only Wait. Kill is also safe after it has reaped
			// the child; this fallback covers failed assertions/startup.
			_ = p.cmd.Process.Kill()
			_ = p.conn.Close() // Best-effort cleanup after terminating the child.
		}
	})
	sup := &Supervisor{
		SourceDir: srcDir,
		Out:       &rec.out,
		build: func(_ context.Context, outPath string) ([]byte, error) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.builds++
			if rec.buildErr != nil {
				return []byte("./internal/x/y.go:10:2: undefined: Ошибка"), rec.buildErr
			}
			// Файл сборки должен появиться: супервизор удаляет прежний бинарь.
			if err := os.WriteFile(outPath, []byte("binary"), 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		},
		start: func(_ string, _, _ []string, _ io.Writer) (*exec.Cmd, error) {
			cmd := supervisorHelperCommand(context.Background(), strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait() // start failed: Supervisor has not taken ownership.
				return nil, err
			}
			conn, err := listener.Accept()
			if err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait() // start failed: no production Wait exists yet.
				return nil, err
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.starts++
			rec.procs = append(rec.procs, &helperProcess{cmd: cmd, conn: conn})
			return cmd, nil
		},
	}
	return sup, rec
}

type recorder struct {
	mu       sync.Mutex
	builds   int
	starts   int
	procs    []*helperProcess
	buildErr error
	out      syncBuffer
}

func (r *recorder) counts() (builds, starts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.builds, r.starts
}

func (r *recorder) failBuilds(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buildErr = err
}

func (r *recorder) proc(i int) *helperProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.procs) {
		return nil
	}
	return r.procs[i]
}

type helperProcess struct {
	cmd  *exec.Cmd
	conn net.Conn
}

// A response proves that this exact child PID is still executing. In
// particular, no exec.Cmd field written asynchronously by Wait is read.
func (p *helperProcess) ping() error {
	if p == nil {
		return errors.New("helper-процесс не запущен")
	}
	if err := p.conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(p.conn, "ping"); err != nil {
		return err
	}
	line, err := bufio.NewReader(p.conn).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != strconv.Itoa(p.cmd.Process.Pid) {
		return fmt.Errorf("helper ответил чужим PID: %q", line)
	}
	return nil
}

// syncBuffer — bytes.Buffer, в который пишет и супервизор, и читает тест.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runSupervisor запускает Run в фоне и глушит его при завершении теста.
// Факт завершения даёт отдельный закрываемый канал: значение из errCh забирает
// тест, и ждать по нему ещё и в cleanup означало бы ждать того, чего уже нет.
func runSupervisor(t *testing.T, sup *Supervisor) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		errCh <- sup.Run(ctx)
		close(finished)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(15 * time.Second):
			t.Error("Run не завершился после отмены контекста (cleanup)")
		}
	})
	return cancel, errCh
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("не дождались: %s", what)
}

func writeGo(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Правка Go-файла → пересборка и перезапуск сервера.
func TestSupervisor_RebuildsAndRestartsOnGoChange(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	runSupervisor(t, sup)

	waitFor(t, "первый запуск сервера", func() bool { _, starts := rec.counts(); return starts == 1 })
	first := rec.proc(0)
	if err := first.ping(); err != nil {
		t.Fatalf("первый процесс не отвечает: %v", err)
	}

	writeGo(t, dir, "main.go", "package main // правка\n")
	waitFor(t, "перезапуск после пересборки", func() bool { _, starts := rec.counts(); return starts == 2 })

	if builds, _ := rec.counts(); builds != 2 {
		t.Errorf("ожидалось 2 сборки, получено %d", builds)
	}
	// Прежний процесс должен быть остановлен, а не брошен: иначе порт остался бы
	// за ним и пересобранный сервер не поднялся бы.
	if err := first.ping(); err == nil {
		t.Fatal("прежний PID отвечает после успешной пересборки")
	}
	if err := rec.proc(1).ping(); err != nil {
		t.Fatalf("новый PID не отвечает после пересборки: %v", err)
	}
}

// Не скомпилировалось — прежний сервер продолжает работать. Это главное свойство
// режима: пока код в работе не собирается, в браузере остаётся живая страница.
func TestSupervisor_KeepsRunningServerWhenBuildFails(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	runSupervisor(t, sup)

	waitFor(t, "первый запуск сервера", func() bool { _, starts := rec.counts(); return starts == 1 })
	first := rec.proc(0)
	if err := first.ping(); err != nil {
		t.Fatalf("первый процесс не отвечает: %v", err)
	}

	rec.failBuilds(errors.New("exit status 1"))
	writeGo(t, dir, "main.go", "package main // сломано\n")
	waitFor(t, "неудачную сборку", func() bool { builds, _ := rec.counts(); return builds >= 2 })

	waitFor(t, "сообщение о неудачной сборке", func() bool {
		return strings.Contains(rec.out.String(), "сборка не удалась — сервер работает на прежней сборке")
	})
	if _, starts := rec.counts(); starts != 1 {
		t.Fatalf("сервер перезапускался при неудачной сборке: starts=%d", starts)
	}
	if err := first.ping(); err != nil {
		t.Fatalf("прежний PID не отвечает после неудачной сборки: %v", err)
	}
	if out := rec.out.String(); !bytes.Contains([]byte(out), []byte("undefined: Ошибка")) {
		t.Errorf("вывод компилятора не показан разработчику:\n%s", out)
	}
}

// Первая сборка обязана пройти: запускать нечего, и Run возвращает ошибку
// вместе с выводом компилятора.
func TestSupervisor_FirstBuildFailureIsFatal(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	rec.failBuilds(errors.New("exit status 2"))

	err := sup.Run(context.Background())
	if err == nil {
		t.Fatal("ожидалась ошибка первой сборки")
	}
	if _, starts := rec.counts(); starts != 0 {
		t.Errorf("сервер запускался несмотря на неудачную первую сборку: starts=%d", starts)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("undefined: Ошибка")) {
		t.Errorf("в ошибке нет вывода компилятора: %v", err)
	}
}

// Отмена контекста (Ctrl+C у супервизора) останавливает и дочерний сервер:
// иначе он остался бы висеть на порту после выхода.
func TestSupervisor_StopsChildOnCancel(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	cancel, errCh := runSupervisor(t, sup)

	waitFor(t, "первый запуск сервера", func() bool { _, starts := rec.counts(); return starts == 1 })
	proc := rec.proc(0)
	if err := proc.ping(); err != nil {
		t.Fatalf("дочерний процесс не отвечает перед отменой: %v", err)
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run вернул ошибку при штатной остановке: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
	if err := proc.ping(); err == nil {
		t.Fatal("дочерний PID отвечает после завершения Run")
	}
}

// Сервер упал сам (занят порт, битая конфигурация) — супервизор не крутит
// перезапуск в цикле, а ждёт следующей правки и поднимает сервер после неё.
func TestSupervisor_WaitsForEditAfterChildExits(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	runSupervisor(t, sup)

	waitFor(t, "первый запуск сервера", func() bool { _, starts := rec.counts(); return starts == 1 })
	proc := rec.proc(0)
	if err := proc.ping(); err != nil {
		t.Fatalf("дочерний процесс не отвечает перед остановкой: %v", err)
	}
	if err := proc.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "сообщение о завершении сервера", func() bool {
		return bytes.Contains([]byte(rec.out.String()), []byte("процесс сервера завершился"))
	})
	time.Sleep(500 * time.Millisecond)
	if _, starts := rec.counts(); starts != 1 {
		t.Fatalf("супервизор перезапускал упавший сервер без правок: starts=%d", starts)
	}

	writeGo(t, dir, "main.go", fmt.Sprintf("package main // %d\n", time.Now().UnixNano()))
	waitFor(t, "запуск после правки", func() bool { _, starts := rec.counts(); return starts == 2 })
	if err := rec.proc(1).ping(); err != nil {
		t.Fatalf("новый PID не отвечает после правки: %v", err)
	}
}
