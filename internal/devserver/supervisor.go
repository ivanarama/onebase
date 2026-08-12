package devserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

// Supervisor — режим пересборки для разработчика платформы (`onebase dev
// --reload-binary`). Сам сервер он не поднимает: собирает бинарь из исходников,
// запускает его дочерним процессом и следит за деревом Go-кода. На правку — заново
// `go build` и перезапуск процесса.
//
// Почему дочерний процесс, а не перезагрузка внутри своего: заменить в живом
// процессе Go-код нечем, а перезапуск «себя» на Windows упирается в то, что
// работающий .exe нельзя перезаписать. Супервизор собирает каждую версию в
// отдельный файл во временном каталоге, поэтому пересборка не трогает бинарь,
// которым запущен он сам.
//
// Конфигурацию (.yaml/.os) супервизор не наблюдает: её без пересборки
// перечитывает сам дочерний dev-сервер.
type Supervisor struct {
	// SourceDir — корень модуля платформы (каталог с go.mod).
	SourceDir string
	// Package — что собирать; пусто = ./cmd/onebase.
	Package string
	// Args — аргументы дочернего процесса (без имени программы).
	Args []string
	// Env — окружение дочернего процесса; nil = унаследовать своё.
	Env []string
	// Port — порт дочернего сервера. >0: ждём освобождения порта перед
	// перезапуском и ответа /health после него.
	Port int
	// Out — куда писать ход дела и вывод дочернего процесса; nil = os.Stdout.
	Out io.Writer
	// OnReady зовётся, когда сервер ответил на /health. restart=false — первый
	// запуск (по нему `--open` открывает браузер), true — после пересборки.
	OnReady func(restart bool)

	// Подменяются в тестах: сборка и запуск — единственное, что ходит наружу.
	build func(ctx context.Context, outPath string) ([]byte, error)
	start func(bin string, args, env []string, out io.Writer) (*exec.Cmd, error)

	binSeq int
}

// child — запущенный процесс сервера и канал с результатом его ожидания.
type child struct {
	cmd  *exec.Cmd
	bin  string
	exit chan error // буфер 1: результат Wait можно вернуть обратно, если его прочитал не тот, кому он нужен
}

func supervisorLog() *slog.Logger { return oblog.Component("devserver.supervisor") }

// Run держит цикл «собрать → запустить → ждать правок» до отмены ctx.
// Ошибку возвращает только на том, после чего работать не с чем: не собралась
// первая сборка, не нашёлся компилятор, не запустился наблюдатель. Дальше все
// сбои — рабочая ситуация разработчика: не скомпилировалось, упало при старте.
func (s *Supervisor) Run(ctx context.Context) error {
	if s.SourceDir == "" {
		return fmt.Errorf("devserver: не задан каталог исходников")
	}
	binDir, err := os.MkdirTemp("", "onebase-dev")
	if err != nil {
		return fmt.Errorf("devserver: временный каталог сборки: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(binDir); err != nil {
			supervisorLog().Debug("не удалось убрать каталог сборок", "dir", binDir, "err", err)
		}
	}()

	// Буфер 1: пока идёт сборка, серия правок схлопывается в один повтор —
	// пересобирать столько раз, сколько было сохранений, незачем.
	changed := make(chan struct{}, 1)
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	watchDone, err := WatchGoContext(watchCtx, s.SourceDir, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("devserver: наблюдение за исходниками: %w", err)
	}
	defer func() {
		stopWatch()
		<-watchDone
	}()

	s.logf("[dev] собираю %s...", s.pkg())
	bin, out, err := s.compile(ctx, binDir)
	if err != nil {
		return fmt.Errorf("devserver: сборка не удалась: %w\n%s", err, out)
	}
	cur, err := s.spawn(bin)
	if err != nil {
		return fmt.Errorf("devserver: запуск сервера: %w", err)
	}
	defer func() { s.stop(cur) }()
	s.logf("[dev] отслеживаю Go-код в %s — пересборка и перезапуск по сохранению файла", s.SourceDir)
	s.awaitReady(ctx, cur, false)

	for {
		var exitCh chan error
		if cur != nil {
			exitCh = cur.exit
		}
		select {
		case <-ctx.Done():
			return nil
		case werr := <-exitCh:
			// Процесс завершился сам: не собралась конфигурация, занят порт,
			// паника. Перезапускать вслепую нельзя — получился бы цикл падений;
			// ждём следующей правки, она и есть попытка починки.
			s.logf("[dev] процесс сервера завершился (%s) — жду правок в Go-коде", exitReason(werr))
			cur = nil
		case <-changed:
			started := time.Now()
			s.logf("[dev] изменился Go-код — пересобираю...")
			nextBin, out, err := s.compile(ctx, binDir)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				// Не скомпилировалось — прежний процесс намеренно оставляем
				// работать: у разработчика в браузере остаётся живой сервер,
				// а не «страница недоступна» до следующей удачной сборки.
				s.logf("[dev] сборка не удалась — сервер работает на прежней сборке:\n%s", trimOutput(out))
				continue
			}
			prevBin := ""
			if cur != nil {
				prevBin = cur.bin
				s.stop(cur)
				cur = nil
			}
			next, err := s.spawn(nextBin)
			if err != nil {
				s.logf("[dev] не удалось запустить пересобранный сервер: %v", err)
				continue
			}
			cur = next
			if prevBin != "" {
				// Windows держит файл запущенного процесса — удалять можно
				// только прежнюю сборку, и только после её остановки.
				if err := os.Remove(prevBin); err != nil && !os.IsNotExist(err) {
					supervisorLog().Debug("прежняя сборка не удалилась", "bin", prevBin, "err", err)
				}
			}
			s.logf("[dev] пересобрано за %s", time.Since(started).Round(time.Millisecond))
			s.awaitReady(ctx, cur, true)
		}
	}
}

func (s *Supervisor) pkg() string {
	if s.Package != "" {
		return s.Package
	}
	return "./cmd/onebase"
}

func (s *Supervisor) out() io.Writer {
	if s.Out != nil {
		return s.Out
	}
	return os.Stdout
}

func (s *Supervisor) logf(format string, args ...any) {
	// Ошибку записи не разбираем: это ход дела в консоли разработчика, и если
	// писать некуда, сообщить об этом всё равно нечем.
	_, _ = fmt.Fprintf(s.out(), format+"\n", args...)
}

// compile собирает очередную версию бинаря в отдельный файл: перезаписать тот,
// которым запущен работающий сервер, на Windows нельзя.
func (s *Supervisor) compile(ctx context.Context, binDir string) (string, []byte, error) {
	s.binSeq++
	name := fmt.Sprintf("onebase-dev-%d", s.binSeq)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	outPath := filepath.Join(binDir, name)
	build := s.build
	if build == nil {
		build = s.goBuild
	}
	msg, err := build(ctx, outPath)
	if err != nil {
		return "", msg, err
	}
	return outPath, msg, nil
}

func (s *Supervisor) goBuild(ctx context.Context, outPath string) ([]byte, error) {
	tool, err := GoTool()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, tool, "build", "-o", outPath, s.pkg()) //nolint:gosec // G204: пакет и путь сборки задаёт разработчик платформы флагами своей же CLI
	cmd.Dir = s.SourceDir
	return cmd.CombinedOutput()
}

func (s *Supervisor) spawn(bin string) (*child, error) {
	start := s.start
	if start == nil {
		start = startProcess
	}
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	cmd, err := start(bin, s.Args, env, s.out())
	if err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, bin: bin, exit: make(chan error, 1)}
	go func() { c.exit <- cmd.Wait() }()
	return c, nil
}

func startProcess(bin string, args, env []string, out io.Writer) (*exec.Cmd, error) {
	cmd := exec.Command(bin, args...) //nolint:gosec // G204: бинарь собран этим же супервизором, аргументы — из флагов CLI разработчика
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd, cmd.Start()
}

// stop останавливает дочерний сервер и, если известен порт, дожидается его
// освобождения: без этого пересобранный процесс встретил бы «порт занят».
func (s *Supervisor) stop(c *child) {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return
	}
	terminate(c.cmd.Process)
	select {
	case <-c.exit:
	case <-time.After(5 * time.Second):
		// Не отреагировал на сигнал — добиваем, иначе порт останется занят.
		if err := c.cmd.Process.Kill(); err != nil {
			supervisorLog().Debug("не удалось завершить процесс сервера", "err", err)
		}
		select {
		case <-c.exit:
		case <-time.After(5 * time.Second):
			s.logf("[dev] процесс сервера не завершился — порт %d может остаться занятым", s.Port)
		}
	}
	if s.Port > 0 && !waitPortFree(s.Port, 5*time.Second) {
		s.logf("[dev] порт %d не освободился за 5 с", s.Port)
	}
}

// awaitReady ждёт, пока пересобранный сервер ответит на /health, и зовёт OnReady.
// Пока сервер не ответил, открывать браузер или сообщать «готово» рано: первый
// запуск делает миграцию схемы БД и порт открывает только после неё.
func (s *Supervisor) awaitReady(ctx context.Context, c *child, restart bool) {
	if c == nil {
		return
	}
	if s.Port > 0 && !s.waitHealthy(ctx, c) {
		return
	}
	if s.OnReady != nil {
		s.OnReady(restart)
	}
}

// readyTimeout — сколько ждать ответа /health. Первый запуск на пустой базе
// создаёт схему до открытия порта, поэтому запас большой (как у лаунчера).
var readyTimeout = 2 * time.Minute

func (s *Supervisor) waitHealthy(ctx context.Context, c *child) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case werr := <-c.exit:
			// Процесс умер, не дождавшись готовности. Результат Wait возвращаем
			// в канал: сообщить о завершении — дело основного цикла.
			c.exit <- werr
			return false
		default:
		}
		if healthOK(client, s.Port) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.logf("[dev] сервер не ответил на порту %d за %s", s.Port, readyTimeout)
	return false
}

// WaitHealthy ждёт, пока сервер на порту ответит на /health. Используется для
// `--open`: открывать браузер до готовности сервера — значит показать
// «соединение не установлено» вместо базы.
func WaitHealthy(ctx context.Context, port int, timeout time.Duration) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if healthOK(client, port) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func healthOK(client *http.Client, port int) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	resp, err := client.Get(url) //nolint:gosec // G107: адрес собран из порта дочернего процесса на loopback
	if err != nil {
		return false
	}
	resp.Body.Close() //nolint:errcheck,gosec // G104: опрос готовности, тело не читаем
	return resp.StatusCode == http.StatusOK
}

// GoTool возвращает путь к компилятору Go. PATH — не единственный источник:
// на Windows SDK часто распакован рядом с проектом, а PATH правит только IDE,
// поэтому в запасе GOROOT.
func GoTool() (string, error) {
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	if root := os.Getenv("GOROOT"); root != "" {
		p := filepath.Join(root, "bin", "go")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("компилятор go не найден: добавьте его в PATH или задайте GOROOT")
}

// waitPortFree ждёт освобождения TCP-порта на loopback.
func waitPortFree(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			// Свободен только если слушатель ещё и закрылся: иначе порт заняли мы сами.
			return ln.Close() == nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func exitReason(err error) string {
	if err == nil {
		return "код 0"
	}
	return err.Error()
}

// trimOutput режет вывод компилятора: при поломке в общем пакете ошибок бывают
// сотни строк, а нужны первые — остальные из них же и следуют.
func trimOutput(out []byte) string {
	const maxLines = 40
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") +
		fmt.Sprintf("\n... ещё %d строк вывода компилятора", len(lines)-maxLines)
}
