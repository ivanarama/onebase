package devserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The helper owns one persistent TCP connection. It replies with its PID, and
// never closes that connection itself: EOF or a peer reset means the OS terminated
// the child (or Supervisor's sole Wait reaped it), not a read of Cmd.ProcessState.
func TestSupervisorHelperProcess(t *testing.T) {
	if os.Getenv("ONEBASE_DEVSERVER_HELPER") != "1" {
		t.Skip("вспомогательный процесс запускается супервизором, а не раннером")
	}
	observer, err := netip.ParseAddrPort(os.Getenv("ONEBASE_DEVSERVER_OBSERVER"))
	if err != nil || observer.Addr() != netip.AddrFrom4([4]byte{127, 0, 0, 1}) || observer.Port() == 0 {
		t.Fatal("helper observer must be a numeric IPv4 loopback address with a port")
	}
	conn, err := net.DialTimeout("tcp4", observer.String(), 10*time.Second) //nolint:gosec // G704: numeric 127.0.0.1 and a nonzero port are checked above; no DNS or external hosts.
	if err != nil {
		t.Fatal(err)
	}
	// Do not defer conn.Close: only process termination must close this socket.
	if _, err := fmt.Fprintf(conn, "ready %d\n", os.Getpid()); err != nil {
		os.Exit(2)
	}
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		switch scanner.Text() {
		case "ping":
			if _, err := fmt.Fprintf(conn, "pong %d\n", os.Getpid()); err != nil {
				os.Exit(2)
			}
		case "exit":
			os.Exit(0)
		case "reset":
			// Exercise the reset path on Unix too; Windows may do this on Kill.
			if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		default:
			os.Exit(2)
		}
	}
	os.Exit(2)
}

// observedChild only reads the process's immutable PID and exchanges messages
// on its own connection. Supervisor remains the only caller of cmd.Wait.
type observedChild struct {
	cmd     *exec.Cmd
	conn    net.Conn
	reader  *bufio.Reader
	mu      sync.Mutex
	stopped bool
}

func (p *observedChild) ping() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil || p.stopped {
		return errors.New("helper is not connected")
	}
	if err := p.conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(p.conn, "ping"); err != nil {
		return err
	}
	line, err := p.reader.ReadString('\n')
	if err != nil {
		return err
	}
	if line != fmt.Sprintf("pong %d\n", p.cmd.Process.Pid) {
		return fmt.Errorf("unexpected helper response: %q", line)
	}
	return nil
}

func (p *observedChild) waitStopped() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return nil
	}
	if p.conn == nil {
		return errors.New("missing helper connection")
	}
	if err := p.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	b, err := p.reader.ReadByte()
	if err == nil {
		return fmt.Errorf("expected helper termination, got byte %q", b)
	}
	// A timeout or arbitrary read error cannot prove process termination.
	if !errors.Is(err, io.EOF) && !helperPeerReset(err) {
		return fmt.Errorf("expected helper EOF or peer reset: %w", err)
	}
	p.stopped = true
	return nil
}

func requireHelperAlive(t *testing.T, p *observedChild) {
	t.Helper()
	if p == nil {
		t.Fatal("helper was not started")
	}
	if err := p.ping(); err != nil {
		t.Fatalf("helper does not answer: %v", err)
	}
}

func requireHelperStopped(t *testing.T, p *observedChild) {
	t.Helper()
	if p == nil {
		t.Fatal("helper was not started")
	}
	if err := p.waitStopped(); err != nil {
		t.Fatalf("helper did not terminate: %v", err)
	}
}

// helperSupervisor собирает супервизор с подменёнными сборкой и запуском: обе
// операции ходят наружу (go build, exec), и в тесте важна логика вокруг них.
func helperSupervisor(t *testing.T, srcDir string) (*Supervisor, *recorder) {
	t.Helper()
	rec := &recorder{}
	// Registered before runSupervisor cleanup: Run must finish and reap children
	// before their observer sockets are checked and closed. No reader goroutine
	// or second Wait needs a separate lifetime here.
	t.Cleanup(func() {
		rec.mu.Lock()
		procs := append([]*observedChild(nil), rec.procs...)
		startErrors := append([]error(nil), rec.startErrors...)
		rec.mu.Unlock()
		for _, err := range startErrors {
			t.Error(err)
		}
		for _, p := range procs {
			if err := p.waitStopped(); err != nil {
				t.Errorf("helper cleanup: %v", err)
				_ = p.cmd.Process.Kill()
				if err := p.waitStopped(); err != nil {
					t.Errorf("helper after kill: %v", err)
				}
			}
			if p.conn != nil {
				_ = p.conn.Close()
			}
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
			rec.mu.Lock()
			var previous *observedChild
			if len(rec.procs) > 0 {
				previous = rec.procs[len(rec.procs)-1]
			}
			rec.mu.Unlock()
			// Check at the replacement boundary, before starting a new child.
			if previous != nil {
				if err := previous.waitStopped(); err != nil {
					rec.mu.Lock()
					rec.startErrors = append(rec.startErrors, err)
					rec.mu.Unlock()
					return nil, err
				}
			}
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				return nil, err
			}
			defer func() { _ = listener.Close() }()
			if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return nil, err
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorHelperProcess$") //nolint:gosec // G204: тест запускает собственный бинарь
			cmd.Env = append(os.Environ(), "ONEBASE_DEVSERVER_HELPER=1", "ONEBASE_DEVSERVER_OBSERVER="+listener.Addr().String())
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			p := &observedChild{cmd: cmd}
			conn, handshakeErr := listener.Accept()
			if handshakeErr == nil {
				p.conn, p.reader = conn, bufio.NewReader(conn)
				handshakeErr = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				if handshakeErr == nil {
					var line string
					line, handshakeErr = p.reader.ReadString('\n')
					if handshakeErr == nil && line != fmt.Sprintf("ready %d\n", cmd.Process.Pid) {
						handshakeErr = fmt.Errorf("unexpected handshake: %q", line)
					}
				}
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.starts++
			rec.procs = append(rec.procs, p)
			if handshakeErr != nil {
				rec.startErrors = append(rec.startErrors, handshakeErr)
				_ = cmd.Process.Kill()
			}
			// Even a failed handshake returns the started command, so Supervisor
			// owns and completes its Wait. The test records the handshake failure.
			return cmd, nil
		},
	}
	return sup, rec
}

type recorder struct {
	mu          sync.Mutex
	builds      int
	starts      int
	procs       []*observedChild
	startErrors []error
	buildErr    error
	out         syncBuffer
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

func (r *recorder) proc(i int) *observedChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.procs) {
		return nil
	}
	return r.procs[i]
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
	requireHelperAlive(t, first)

	writeGo(t, dir, "main.go", "package main // правка\n")
	waitFor(t, "перезапуск после пересборки", func() bool { _, starts := rec.counts(); return starts == 2 })

	if builds, _ := rec.counts(); builds != 2 {
		t.Errorf("ожидалось 2 сборки, получено %d", builds)
	}
	requireHelperStopped(t, first)
	requireHelperAlive(t, rec.proc(1))
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
	requireHelperAlive(t, first)

	rec.failBuilds(errors.New("exit status 1"))
	writeGo(t, dir, "main.go", "package main // сломано\n")
	waitFor(t, "неудачную сборку", func() bool { builds, _ := rec.counts(); return builds >= 2 })

	// Дать шанс ошибочному перезапуску проявиться.
	time.Sleep(500 * time.Millisecond)
	if _, starts := rec.counts(); starts != 1 {
		t.Fatalf("сервер перезапускался при неудачной сборке: starts=%d", starts)
	}
	requireHelperAlive(t, first)
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
	requireHelperAlive(t, proc)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run вернул ошибку при штатной остановке: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
	requireHelperStopped(t, proc)
}

// An unresponsive observer is not proof of exit: a live helper must still
// answer after the read deadline, and Supervisor must remain its sole waiter.
func TestSupervisor_ObserverTimeoutDoesNotCountAsExit(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	runSupervisor(t, sup)
	waitFor(t, "первый запуск сервера", func() bool { _, starts := rec.counts(); return starts == 1 })
	proc := rec.proc(0)
	requireHelperAlive(t, proc)
	var timeout net.Error
	if err := proc.waitStopped(); !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("live helper must produce a timeout, got %v", err)
	}
	requireHelperAlive(t, proc)
}

func TestSupervisor_PeerResetCountsAsExit(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "main.go", "package main\n")
	sup, rec := helperSupervisor(t, dir)
	runSupervisor(t, sup)
	waitFor(t, "первый запуск сервера", func() bool { _, starts := rec.counts(); return starts == 1 })
	proc := rec.proc(0)
	requireHelperAlive(t, proc)
	if _, err := fmt.Fprintln(proc.conn, "reset"); err != nil {
		t.Fatal(err)
	}
	requireHelperStopped(t, proc)
	waitFor(t, "супервизор дождался завершения процесса", func() bool {
		return bytes.Contains([]byte(rec.out.String()), []byte("процесс сервера завершился"))
	})
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
	requireHelperAlive(t, proc)
	if err := proc.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	requireHelperStopped(t, proc)
	waitFor(t, "сообщение о завершении сервера", func() bool {
		return bytes.Contains([]byte(rec.out.String()), []byte("процесс сервера завершился"))
	})
	time.Sleep(500 * time.Millisecond)
	if _, starts := rec.counts(); starts != 1 {
		t.Fatalf("супервизор перезапускал упавший сервер без правок: starts=%d", starts)
	}

	writeGo(t, dir, "main.go", fmt.Sprintf("package main // %d\n", time.Now().UnixNano()))
	waitFor(t, "запуск после правки", func() bool { _, starts := rec.counts(); return starts == 2 })
	requireHelperAlive(t, rec.proc(1))
}
