package devserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	// Sleep, а не select{} — пустой select роняет рантайм как deadlock.
	time.Sleep(2 * time.Minute)
}

// helperSupervisor собирает супервизор с подменёнными сборкой и запуском: обе
// операции ходят наружу (go build, exec), и в тесте важна логика вокруг них.
func helperSupervisor(t *testing.T, srcDir string) (*Supervisor, *recorder) {
	t.Helper()
	rec := &recorder{}
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
			cmd := exec.Command(os.Args[0], "-test.run=TestSupervisorHelperProcess") //nolint:gosec // G204: тест запускает собственный бинарь
			cmd.Env = append(os.Environ(), "ONEBASE_DEVSERVER_HELPER=1")
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.starts++
			rec.procs = append(rec.procs, cmd)
			return cmd, nil
		},
	}
	return sup, rec
}

type recorder struct {
	mu       sync.Mutex
	builds   int
	starts   int
	procs    []*exec.Cmd
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

func (r *recorder) proc(i int) *exec.Cmd {
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

	writeGo(t, dir, "main.go", "package main // правка\n")
	waitFor(t, "перезапуск после пересборки", func() bool { _, starts := rec.counts(); return starts == 2 })

	if builds, _ := rec.counts(); builds != 2 {
		t.Errorf("ожидалось 2 сборки, получено %d", builds)
	}
	// Прежний процесс должен быть остановлен, а не брошен: иначе порт остался бы
	// за ним и пересобранный сервер не поднялся бы.
	waitFor(t, "остановку прежнего процесса", func() bool {
		return first != nil && first.ProcessState != nil
	})
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

	rec.failBuilds(errors.New("exit status 1"))
	writeGo(t, dir, "main.go", "package main // сломано\n")
	waitFor(t, "неудачную сборку", func() bool { builds, _ := rec.counts(); return builds >= 2 })

	// Дать шанс ошибочному перезапуску проявиться.
	time.Sleep(500 * time.Millisecond)
	if _, starts := rec.counts(); starts != 1 {
		t.Fatalf("сервер перезапускался при неудачной сборке: starts=%d", starts)
	}
	if first == nil || first.ProcessState != nil {
		t.Fatal("прежний процесс остановлен, хотя новая сборка не удалась")
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
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run вернул ошибку при штатной остановке: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
	if proc == nil || proc.ProcessState == nil {
		t.Fatal("дочерний процесс сервера не остановлен")
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
	if err := proc.Process.Kill(); err != nil {
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
}
