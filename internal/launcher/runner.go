package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
)

type managedProc struct {
	cmd        *exec.Cmd
	port       int
	startedAt  time.Time
	debugToken string // секрет для X-OneBase-Debug-Token (прокси отладчика)
}

// Runner tracks running base processes.
type Runner struct {
	mu    sync.Mutex
	procs map[string]*managedProc
	// exits отмечает базы, чей процесс завершился после последнего Start.
	// WaitReady по этому признаку отличает «упал при старте» (ошибка с хвостом
	// лога сразу) от «ещё запускается» (ждём дольше).
	exits map[string]bool
}

func NewRunner() *Runner {
	return &Runner{procs: make(map[string]*managedProc), exits: make(map[string]bool)}
}

// DebugToken возвращает секрет debug API для запущенной базы (пустую строку,
// если база не запущена этим лаунчером). Прокси отладчика прикладывает его как
// заголовок X-OneBase-Debug-Token.
func (r *Runner) DebugToken(baseID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mp, ok := r.procs[baseID]; ok {
		return mp.debugToken
	}
	return ""
}

func generateDebugToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// normalizeHost сводит выбранный интерфейс прослушивания к безопасному набору:
// "0.0.0.0" (все интерфейсы — база доступна из сети) либо "127.0.0.1" (только
// этот компьютер, умолчание). Пустое/незнакомое → loopback: наружу база
// выставляется только явным выбором (secure-by-default, план 53).
func normalizeHost(s string) string {
	if strings.TrimSpace(s) == "0.0.0.0" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// runArgs собирает аргументы дочернего `onebase run` для базы. Вынесено из Start
// ради юнит-тестов. Главное здесь — проброс --host: без него дочерний процесс
// всегда брал дефолт 127.0.0.1, и открыть базу в локальную сеть через лаунчер
// было нельзя (issue #590). Loopback тоже передаём явно, чтобы поведение не
// зависело от дефолта подкоманды run.
func runArgs(base *Base) []string {
	var args []string
	if base.DBType == "sqlite" || (base.DBType == "" && base.DB == "") {
		// backward-compat: пустой db и пустой db_type → SQLite (как было до
		// добавления поля db_type). db_path генерируется автоматически если пустой.
		dbPath := base.DBPath
		if dbPath == "" {
			dbPath = filepath.Join(os.TempDir(), "onebase_"+base.ID+".db")
		}
		args = []string{"run", "--sqlite", dbPath, "--port", fmt.Sprintf("%d", base.Port)}
	} else {
		args = []string{"run", "--db", base.DB, "--port", fmt.Sprintf("%d", base.Port)}
	}
	if base.ConfigSource == "file" {
		args = append(args, "--project", base.Path)
	} else {
		args = append(args, "--config-source", "database")
	}
	args = append(args, "--host", normalizeHost(base.Host))
	return args
}

func (r *Runner) Start(base *Base) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.procs[base.ID]; ok {
		return i18nerr.Errorf("база %q уже запущена", base.Name)
	}

	// check port conflict with other running bases (tracked)
	for _, mp := range r.procs {
		if mp.port == base.Port {
			return i18nerr.Errorf("порт %d уже занят другой запущенной базой", base.Port)
		}
	}

	// OS-level port availability check: catches leftover processes not tracked by runner
	if !portFree(base.Port) {
		return i18nerr.Errorf("порт %d уже занят другим процессом — остановите его вручную или смените порт базы", base.Port)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("runner: executable: %w", err)
	}

	logPath, err := baseLogPath(base.ID)
	if err != nil {
		return err
	}
	// SecretFile: журнал лежит в ~/.onebase (каталог 0700) и содержит вывод
	// прикладного сервера — там могут оказаться данные пользователей. Пишет и
	// читает его только лаунчер под тем же пользователем, так что закрытые
	// права никому не мешают.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsmode.SecretFile) //nolint:gosec // G304: logPath собран baseLogPath из идентификатора базы, а не из запроса
	if err != nil {
		return fmt.Errorf("runner: log: %w", err)
	}

	args := runArgs(base)

	// Per-base секрет для debug API: процесс базы примет запросы к /debug/global/*
	// только с этим токеном (см. ui.MountDebug). Конфигуратор-прокси его прикладывает.
	debugToken, err := generateDebugToken()
	if err != nil {
		closeRead("журнал базы", logFile)
		return fmt.Errorf("runner: debug token: %w", err)
	}

	cmd := exec.Command(exe, args...) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "ONEBASE_DEBUG_TOKEN="+debugToken)
	noWindow(cmd)

	if err := cmd.Start(); err != nil {
		closeRead("журнал базы", logFile)
		return fmt.Errorf("runner: start: %w", err)
	}

	r.procs[base.ID] = &managedProc{cmd: cmd, port: base.Port, startedAt: time.Now(), debugToken: debugToken}
	delete(r.exits, base.ID)

	go func() {
		// Ошибку Wait не разбираем здесь намеренно: ненулевой код возврата —
		// штатный исход для остановленной базы, а сам код и причину забирает
		// recordExit из cmd.ProcessState. Возвращать её некуда — горутина.
		bestEffort("дождаться завершения процесса базы", cmd.Wait())
		closeRead("журнал базы", logFile)
		r.recordExit(base.ID, cmd)
	}()

	return nil
}

// recordExit снимает с учёта только тот процесс, завершение которого мы
// наблюдали. После быстрого Restart старый cmd может завершиться уже после
// запуска нового процесса с тем же baseID; без проверки он удалил бы новый
// процесс из procs и WaitReady ошибочно счёл бы его упавшим.
func (r *Runner) recordExit(baseID string, cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.procs[baseID]; ok {
		if current.cmd != cmd {
			return
		}
		delete(r.procs, baseID)
	}
	r.exits[baseID] = true
}

// Stop снимает отслеживаемый процесс базы. Возврата нет намеренно: результат
// всегда был nil, и проверка его ничего бы не значила. Сама по себе отправка
// сигнала успех не гарантирует — подтверждение только одно, освобождение порта,
// поэтому вызывающие, которым важно, что база действительно встала (перед
// восстановлением, переименованием файла БД), обязаны спросить waitPortFree.
func (r *Runner) Stop(baseID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	mp, ok := r.procs[baseID]
	if !ok {
		return
	}
	killProc(mp.cmd.Process)
	delete(r.procs, baseID)
}

// StopAll kills all running base processes (tracked + any still listening on extraPorts)
// and waits for ports to free.
func (r *Runner) StopAll(extraPorts []int) {
	r.mu.Lock()
	type procInfo struct {
		proc *os.Process
		port int
	}
	var all []procInfo
	for id, mp := range r.procs {
		all = append(all, procInfo{mp.cmd.Process, mp.port})
		delete(r.procs, id)
	}
	r.mu.Unlock()

	for _, pi := range all {
		killProc(pi.proc)
	}

	// Kill any processes still occupying the ports (survived launcher restart or are untracked).
	seen := make(map[int]bool)
	for _, pi := range all {
		seen[pi.port] = true
	}
	for _, port := range extraPorts {
		if !seen[port] {
			killByPort(port)
			seen[port] = true
		}
	}
	// Also try port-based kill for tracked ports in case killProc was not enough.
	for _, pi := range all {
		killByPort(pi.port)
	}

	for port := range seen {
		if !waitPortFree(port, 3*time.Second) {
			respondLog().Warn("порт базы не освободился после остановки", "port", port)
		}
	}
}

// killByPort finds and kills any process listening on the given TCP port.
func killByPort(port int) {
	switch runtime.GOOS {
	case "windows":
		// runPowerShell runs with -WindowStyle Hidden — no CMD flash.
		_, psErr := runPowerShell(fmt.Sprintf(
			`$c = Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue
			 if ($c) { Stop-Process -Id $c.OwningProcess -Force -ErrorAction SilentlyContinue }`,
			port))
		bestEffort("завершить процесс на порту через PowerShell", psErr)
	case "darwin":
		target := fmt.Sprintf(":%d", port)
		out, _ := exec.Command("lsof", "-ti", target).Output() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		if pid := strings.TrimSpace(string(out)); pid != "" {
			for _, p := range strings.Fields(pid) {
				// Результат не решающий: успех проверяется опросом порта в
				// waitPortFree, а процесс мог завершиться и сам.
				//nolint:gosec // G204: pid получен от lsof, не от пользователя
				bestEffort("завершить процесс "+p, exec.Command("kill", "-9", p).Run())
			}
		}
	case "linux":
		target := fmt.Sprintf(":%d", port)
		out, _ := exec.Command("sh", "-c", fmt.Sprintf("ss -tlnp 2>/dev/null | grep '%s '", target)).Output() //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
		for _, line := range strings.Split(string(out), "\n") {
			if idx := strings.Index(line, "pid="); idx >= 0 {
				rest := line[idx+4:]
				if end := strings.IndexAny(rest, ",\n "); end > 0 {
					//nolint:gosec // G204: pid разобран из вывода ss, не от пользователя
					bestEffort("завершить процесс "+rest[:end],
						exec.Command("kill", "-9", rest[:end]).Run())
				}
			}
		}
	}
}

// killProc terminates a tracked process directly — no external utilities, no CMD windows.
func killProc(p *os.Process) {
	if p == nil {
		return
	}
	// Процесс мог завершиться сам между проверкой и Kill — это не ошибка
	// вызывающего. Успех остановки подтверждается освобождением порта.
	bestEffort("завершить процесс базы", p.Kill())
}

// portFree reports whether the TCP port is free on localhost.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	// Порт свободен, только если слушатель ещё и закрылся: иначе мы сами его
	// и заняли, а вызывающий получил бы «свободен» на занятый порт.
	return ln.Close() == nil
}

// waitPortFree blocks until the port becomes free or timeout expires.
// Возвращает false, если порт так и остался занят: для вызывающего это значит,
// что база не остановилась и трогать её файлы нельзя.
func waitPortFree(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portFree(port) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// StartedAt returns when the process for baseID was started.
// The second return value is false if the base is not running.
func (r *Runner) StartedAt(baseID string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mp, ok := r.procs[baseID]; ok {
		return mp.startedAt, true
	}
	return time.Time{}, false
}

func (r *Runner) IsRunning(baseID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.procs[baseID]
	return ok
}

// Healthy сообщает, отвечает ли на порту базы её /health — то есть база уже
// работает, даже если запущена не этим экземпляром лаунчера (прежний
// экземпляр, пересборка exe, ручной запуск). Используется для «усыновления»
// живой базы вместо ошибки «порт занят».
func (r *Runner) Healthy(base *Base) bool {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/health", base.Port))
	if err != nil {
		return false
	}
	// Закрываем литерально, а не через closeRead: bodyclose (блокирующий
	// линтер) распознаёт только прямой resp.Body.Close() и на обёртке
	// сообщает «response body must be closed».
	resp.Body.Close() //nolint:errcheck,gosec // G104: тело не читаем, закрытие здесь вторично
	return resp.StatusCode == http.StatusOK
}

func (r *Runner) BaseURL(base *Base) string {
	return fmt.Sprintf("http://localhost:%d", base.Port)
}

func (r *Runner) MigrateBase(ctx context.Context, base *Base) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}

	var args []string
	if base.DBType == "sqlite" {
		args = []string{"migrate", "--sqlite", base.DBPath}
	} else {
		args = []string{"migrate", "--db", base.DB}
	}
	if base.ConfigSource == "file" {
		args = append(args, "--project", base.Path)
	} else {
		args = append(args, "--config-source", "database")
	}

	cmd := exec.CommandContext(ctx, exe, args...) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	noWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err == nil {
		touchMigrateMarker(base.ID)
	}
	return string(out), err
}

// Restart останавливает базу (если запущена), дожидается освобождения порта и
// запускает её заново. Используется, чтобы запущенная сессия Предприятия
// подхватила изменения конфигурации без ручного захода в лаунчер.
// Базы, запущенные прежним экземпляром лаунчера, в procs не числятся —
// добиваем процесс на порту, иначе Start упрётся в «порт занят».
func (r *Runner) Restart(base *Base) error {
	r.Stop(base.ID)
	if !portFree(base.Port) {
		killByPort(base.Port)
	}
	// Порт мог не освободиться — тогда Start ниже вернёт «порт занят», и эта
	// ошибка дойдёт до пользователя. Отдельно проверять нечего.
	waitPortFree(base.Port, 3*time.Second)
	return r.Start(base)
}

// startupGraceTimeout — сколько ждать готовности процесса базы, запущенного этим
// лаунчером. Сервер открывает порт ТОЛЬКО после загрузки конфигурации и полной
// миграции схемы БД, поэтому первый запуск (схема создаётся с нуля) на большой
// конфигурации или медленном диске легко не укладывается в 15 секунд. Большой
// запас не задерживает диагностику реальных сбоев: если процесс завершился,
// WaitReady возвращает ошибку немедленно. Переменная — для подмены в тестах.
var startupGraceTimeout = 2 * time.Minute

// WaitReady polls /health on the base's port until it responds 200 or timeout.
// Для базы, запущенной этим лаунчером, ожидание умнее фиксированного таймаута:
//   - процесс завершился, не начав слушать порт, — немедленная ошибка с хвостом
//     его лога (реальная причина: битая конфигурация, ошибка миграции и т.п.);
//   - процесс жив, но ещё не отвечает — ждём до startupGraceTimeout (первая
//     миграция схемы БД выполняется до открытия порта).
//
// Для «усыновлённых» баз (запущены не этим лаунчером) действует переданный timeout.
func (r *Runner) WaitReady(base *Base, timeout time.Duration) error {
	url := fmt.Sprintf("http://localhost:%d/health", base.Port)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	// procExited ловит и мгновенное падение — процесс, успевший завершиться
	// между Start и WaitReady, из procs уже удалён.
	tracked := r.IsRunning(base.ID) || r.procExited(base.ID)
	if tracked && startupGraceTimeout > timeout {
		timeout = startupGraceTimeout
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			// Литерально — по той же причине, что и в Healthy: bodyclose не
			// видит закрытия через обёртку.
			resp.Body.Close() //nolint:errcheck,gosec // G104: опрос готовности, тело не читаем
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if tracked && !r.IsRunning(base.ID) {
			logPath, _ := baseLogPath(base.ID)
			if tail := tailFile(logPath, logTailLines); tail != "" {
				return i18nerr.Errorf("процесс базы завершился при запуске — причина в конце лога (%s): %s", logPath, "\n\n"+tail)
			}
			return i18nerr.Errorf("процесс базы завершился при запуске — подробности в логе: %s", logPath)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if tracked {
		logPath, _ := baseLogPath(base.ID)
		return i18nerr.Errorf("база не ответила на порту %d за %s, но процесс ещё работает — вероятно, идёт первая миграция схемы БД; подождите и откройте базу ещё раз (лог: %s)", base.Port, timeout, logPath)
	}
	return i18nerr.Errorf("сервер не ответил на порту %d за %s", base.Port, timeout)
}

// procExited сообщает, завершался ли процесс базы после последнего Start.
func (r *Runner) procExited(baseID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exits[baseID]
}

// logTailLines — сколько последних строк лога попадает в ошибку запуска.
const logTailLines = 15

// tailFile возвращает последние n строк файла (читает не более 8 КБ с конца).
// Пустая строка — файла нет или прочитать не удалось.
//
// Реализация общая с отчётом об ошибке (план 115): тот же журнал, то же чтение
// с конца — расходиться этим двум местам незачем.
func tailFile(path string, n int) string {
	return bugreport.TailFile(path, n, 8<<10)
}

// logsDirOverride подменяет каталог логов баз в тестах.
var logsDirOverride string

func baseLogPath(id string) (string, error) {
	if logsDirOverride != "" {
		return filepath.Join(logsDirOverride, id+".log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".onebase", "logs")
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".log"), nil
}

// BaseLogPath возвращает путь к журналу базы. Экспортируется ради отчёта об
// ошибке (план 115): `onebase support` собирает пакет без запущенного лаунчера
// и должен искать журналы там же, где их пишет лаунчер, а не по своей догадке.
func BaseLogPath(id string) (string, error) { return baseLogPath(id) }

// migrateMarkerPath возвращает путь к файлу-метке времени последней успешной
// миграции базы. Метка лежит в служебной папке лаунчера (а не в каталоге
// конфигурации), чтобы не попадать в скан .os/.yaml и в git.
func migrateMarkerPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".onebase", "state")
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "migrate_"+id+".stamp"), nil
}

// touchMigrateMarker обновляет mtime метки последней миграции на текущее время.
func touchMigrateMarker(id string) {
	p, err := migrateMarkerPath(id)
	if err != nil {
		return
	}
	now := time.Now()
	if err := os.WriteFile(p, []byte(now.Format(time.RFC3339)), 0o600); err == nil {
		// Страховка: mtime читает migratedAt, а WriteFile его и так выставляет
		// в «сейчас». Поэтому сбой здесь ни на что не влияет — но пусть будет
		// видно, если файловая система ведёт себя неожиданно.
		if cerr := os.Chtimes(p, now, now); cerr != nil {
			respondLog().Debug("не удалось выставить время метки миграции", "path", p, "err", cerr)
		}
	}
}

// migratedAt возвращает время последней успешной миграции базы (mtime метки).
// Второе значение false, если миграция ещё ни разу не выполнялась.
func migratedAt(id string) (time.Time, bool) {
	p, err := migrateMarkerPath(id)
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
