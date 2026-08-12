package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/processcontrol"
)

type managedProc struct {
	cmd          *exec.Cmd
	port         int
	startedAt    time.Time
	debugToken   string // секрет для X-OneBase-Debug-Token (прокси отладчика)
	controlToken string
	done         chan struct{}
}

// Runner tracks running base processes.
type Runner struct {
	mu       sync.Mutex
	procs    map[string]*managedProc
	stopping bool
	// lifecycleMu сериализует stop/restart/restore/delete/update. stopping при
	// этом запрещает обычному Start войти между остановкой и разрушительной
	// операцией; internal startHeld используется владельцем lease при Restart.
	lifecycleMu sync.Mutex
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

func (r *Runner) Start(base *Base) error { return r.start(base, false) }

func (r *Runner) startHeld(base *Base) error { return r.start(base, true) }

func (r *Runner) start(base *Base, lifecycleHeld bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping && !lifecycleHeld {
		return i18nerr.Errorf("лаунчер останавливает базы — новый запуск временно запрещён")
	}

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

	exe, err := exePath()
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

	// Persistent per-base secret нужен только HMAC lifecycle-control. Основной
	// launcher сохраняет его до запуска, чтобы безопасно усыновить процесс.
	controlToken := base.ControlToken
	if controlToken == "" {
		closeRead("журнал базы", logFile)
		return errors.New("control token базы не сохранён; запуск отменён")
	}
	// Debug bearer intentionally is process-local. Persisting/reusing it would
	// let an untrusted process on the registered port steal a credential that
	// unlocks evaluate/pprof/metrics. Adopted lifecycle control uses HMAC above.
	debugToken, err := generateDebugToken()
	if err != nil {
		closeRead("журнал базы", logFile)
		return fmt.Errorf("runner: debug token: %w", err)
	}

	cmd := exec.Command(exe, args...) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(),
		"ONEBASE_DEBUG_TOKEN="+debugToken,
		"ONEBASE_CONTROL_TOKEN="+controlToken,
		"ONEBASE_BASE_ID="+base.ID)
	noWindow(cmd)

	if err := cmd.Start(); err != nil {
		closeRead("журнал базы", logFile)
		return fmt.Errorf("runner: start: %w", err)
	}

	done := make(chan struct{})
	r.procs[base.ID] = &managedProc{cmd: cmd, port: base.Port, startedAt: time.Now(),
		debugToken: debugToken, controlToken: controlToken, done: done}
	delete(r.exits, base.ID)

	go func() {
		// Ошибку Wait не разбираем здесь намеренно: ненулевой код возврата —
		// штатный исход для остановленной базы, а сам код и причину забирает
		// recordExit из cmd.ProcessState. Возвращать её некуда — горутина.
		bestEffort("дождаться завершения процесса базы", cmd.Wait())
		closeRead("журнал базы", logFile)
		close(done)
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

// exePath — путь к бинарю платформы, который лаунчер запускает дочерним
// процессом (сервер базы, migrate). Вынесено в переменную ради тестов: под
// `go test` os.Executable() указывает на тест-бинарь, и запуск его «как
// платформы» прогоняет весь пакет заново — рекурсивно и без конца.
var exePath = os.Executable

const (
	controlProbeTimeout = 1500 * time.Millisecond
	controlStopTimeout  = 35 * time.Second
)

// BaseRuntimeStatus отделяет «на порту отвечает onebase» от «лаунчер умеет
// доказуемо обратиться именно к этому процессу». Второе требуется для Stop:
// убивать произвольный PID только потому, что он занял сохранённый порт, нельзя.
type BaseRuntimeStatus struct {
	Running      bool
	Controllable bool
	// Occupied distinguishes a definitely stopped base from an unresponsive or
	// foreign listener. Destructive operations must fail closed in the latter
	// case even when onebase identity could not be established.
	Occupied bool
}

// RuntimeStatus возвращает свежий, ограниченный таймаутом статус без чтения
// app.yaml/БД конфигурации. Процесс текущего Runner известен по os.Process;
// переживший перезапуск — по persistent control-token и base ID.
func (r *Runner) RuntimeStatus(base *Base) BaseRuntimeStatus {
	if base == nil {
		return BaseRuntimeStatus{}
	}
	r.mu.Lock()
	mp, tracked := r.procs[base.ID]
	tracked = tracked && managedProcMatchesBase(mp, base)
	r.mu.Unlock()
	if tracked {
		return BaseRuntimeStatus{Running: true, Controllable: true, Occupied: true}
	}
	if portFree(base.Port) {
		return BaseRuntimeStatus{}
	}
	if base.ControlToken != "" {
		if controlIdentity(base, 0) {
			return BaseRuntimeStatus{Running: true, Controllable: true, Occupied: true}
		}
		// Once a persistent identity exists, an HMAC failure means this listener
		// is not the registered process. Do not downgrade to forgeable /health.
		return BaseRuntimeStatus{Occupied: true}
	}
	// Совместимость с процессами, запущенными старым launcher: показываем их
	// как работающие, но не выдаём право на kill-by-port. /healthz маркирован
	// версией onebase и не требует доступной БД для самой идентификации.
	if onebaseHealthMarker(base) {
		return BaseRuntimeStatus{Running: true, Occupied: true}
	}
	return BaseRuntimeStatus{Occupied: true}
}

// managedProcMatchesBase distinguishes a Store record from a different record
// that reused its ID while the old process was still tracked. Production
// managed processes always carry a control token; the tokenless branch keeps
// compatibility with in-package test doubles and pre-token in-memory records.
func managedProcMatchesBase(mp *managedProc, base *Base) bool {
	if mp == nil || base == nil {
		return false
	}
	if mp.controlToken != "" || base.ControlToken != "" {
		return mp.controlToken != "" && base.ControlToken != "" && mp.controlToken == base.ControlToken
	}
	// Compatibility for tokenless in-package test doubles and pre-token
	// in-memory records. Without a token, port is the strongest available key.
	return mp.port == base.Port
}

func (r *Runner) tracksBaseGeneration(base *Base) bool {
	if base == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return managedProcMatchesBase(r.procs[base.ID], base)
}

func localControlClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		// Never follow a redirect from a configured localhost port. Besides
		// changing the authenticated peer, redirects make local probes an SSRF.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func controlProcessIdentity(base *Base, expectedPID int) (processcontrol.Identity, error) {
	return controlProcessIdentityWithClient(base, expectedPID, localControlClient(controlProbeTimeout))
}

func controlProcessIdentityWithClient(base *Base, expectedPID int, client *http.Client) (processcontrol.Identity, error) {
	if base == nil || base.ControlToken == "" {
		return processcontrol.Identity{}, errors.New("control token is empty")
	}
	challenge, err := processcontrol.NewNonce()
	if err != nil {
		return processcontrol.Identity{}, err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/debug/process/identity?%s",
		base.Port, (url.Values{processcontrol.ChallengeQuery: []string{challenge}}).Encode())
	resp, err := client.Get(endpoint)
	if err != nil {
		return processcontrol.Identity{}, err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec // probe body; status/JSON determine identity
	if resp.StatusCode != http.StatusOK {
		return processcontrol.Identity{}, fmt.Errorf("identity HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return processcontrol.Identity{}, err
	}
	var got processcontrol.Identity
	if err := json.Unmarshal(data, &got); err != nil {
		return processcontrol.Identity{}, err
	}
	want := processcontrol.IdentityProof(base.ControlToken, got.BaseID, got.PID,
		got.Instance, challenge)
	if got.BaseID != base.ID || got.PID <= 0 || got.Instance == "" ||
		!processcontrol.Verify(got.Proof, want) {
		return processcontrol.Identity{}, errors.New("identity proof mismatch")
	}
	if expectedPID > 0 && got.PID != expectedPID {
		return processcontrol.Identity{}, fmt.Errorf("identity PID %d does not match tracked process PID %d", got.PID, expectedPID)
	}
	return got, nil
}

// singleConnectionControlClient authenticates and sends a sensitive follow-up
// over one already-open TCP connection. If that connection closes, the
// transport refuses to redial, so a process that races to reoccupy the port
// never receives the follow-up credential.
func singleConnectionControlClient(port int, timeout time.Duration) (*http.Client, func(), error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return nil, nil, err
	}
	var dialMu sync.Mutex
	used := false
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialMu.Lock()
			defer dialMu.Unlock()
			if used {
				return nil, errors.New("authenticated control connection is closed")
			}
			used = true
			return conn, nil
		},
		DisableKeepAlives: false,
		MaxConnsPerHost:   1,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cleanup := func() {
		transport.CloseIdleConnections()
		_ = conn.Close()
	}
	return client, cleanup, nil
}

func controlIdentity(base *Base, expectedPID int) bool {
	_, err := controlProcessIdentity(base, expectedPID)
	return err == nil
}

func onebaseHealthMarker(base *Base) bool {
	client := localControlClient(controlProbeTimeout)
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", base.Port))
	if err == nil {
		marked := resp.Header.Get("X-OneBase-Version") != ""
		resp.Body.Close() //nolint:errcheck,gosec // only headers identify the server
		if marked {
			return true
		}
	}
	// Старые onebase до version-header имели публичный /health = 200. Такой
	// ответ недостаточен для управления процессом, но для защиты данных его
	// консервативно считаем работающей (неуправляемой) базой: лучше показать
	// лишний индикатор/отказать в restore, чем писать поверх живой SQLite.
	resp, err = client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", base.Port))
	if err != nil {
		return false
	}
	resp.Body.Close() //nolint:errcheck,gosec // liveness status only
	return resp.StatusCode == http.StatusOK
}

type processExitWaiter interface {
	Wait(time.Duration) bool
	Close() error
}

var openProcessExitWaiter = newProcessExitWaiter

func requestControlStop(base *Base, expectedPID int) error {
	// Prove identity and deliver the signed stop request over one already-open
	// connection. If the proven process exits between the two requests, the
	// transport must fail instead of redialling a different process that won
	// the same port.
	client, closeClient, err := singleConnectionControlClient(base.Port, 5*time.Second)
	if err != nil {
		return fmt.Errorf("подключиться к процессу базы %q: %w", base.Name, err)
	}
	defer closeClient()

	identity, err := controlProcessIdentityWithClient(base, expectedPID, client)
	if err != nil {
		return fmt.Errorf("подтвердить процесс базы %q: %w", base.Name, err)
	}
	waiter, err := openProcessExitWaiter(identity.PID)
	if err != nil {
		return fmt.Errorf("открыть процесс базы %q (PID %d): %w", base.Name, identity.PID, err)
	}
	defer waiter.Close() //nolint:errcheck // wait result, not handle cleanup, determines success

	nonce, err := processcontrol.NewNonce()
	if err != nil {
		return fmt.Errorf("подписать остановку базы %q: %w", base.Name, err)
	}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/debug/process/stop", base.Port), nil)
	if err != nil {
		return err
	}
	req.Header.Set(processcontrol.HeaderBaseID, base.ID)
	req.Header.Set(processcontrol.HeaderInstance, identity.Instance)
	req.Header.Set(processcontrol.HeaderNonce, nonce)
	req.Header.Set(processcontrol.HeaderProof,
		processcontrol.StopProof(base.ControlToken, base.ID, identity.Instance, nonce))
	resp, err := client.Do(req)
	if err != nil {
		if waiter.Wait(0) && portFree(base.Port) { // процесс мог завершиться между identity и POST
			return nil
		}
		return fmt.Errorf("остановить базу %q через control API: %w", base.Name, err)
	}
	resp.Body.Close() //nolint:errcheck,gosec // response body is empty JSON acknowledgement
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("остановить базу %q через control API: HTTP %d", base.Name, resp.StatusCode)
	}
	// Listener закрывается в начале http.Server.Shutdown, раньше scheduler,
	// активных handlers и DB cleanup. Поэтому успех подтверждает только exit
	// того PID/process generation, который доказал HMAC identity.
	if !waiter.Wait(controlStopTimeout) {
		return fmt.Errorf("процесс базы %q (PID %d) не завершился за %s",
			base.Name, identity.PID, controlStopTimeout)
	}
	if !portFree(base.Port) {
		return fmt.Errorf("процесс базы %q (PID %d) завершился, но порт %d уже занят другим процессом",
			base.Name, identity.PID, base.Port)
	}
	return nil
}

// holdStarts берёт внутрипроцессный lifecycle lease. Пока он удерживается,
// обычный Start и другая destructive-операция получают отказ, поэтому база не
// может снова открыть БД между stop и restore/delete/update.
func (r *Runner) holdStarts() error {
	if !r.lifecycleMu.TryLock() {
		return errors.New("другая операция с базами уже выполняется")
	}
	r.mu.Lock()
	r.stopping = true
	r.mu.Unlock()
	return nil
}

func waitManagedProcessExit(mp *managedProc, timeout time.Duration) bool {
	if mp == nil {
		return true
	}
	if mp.done == nil {
		// Совместимость тестовых/fake managedProc; production Start всегда
		// создаёт done и тем самым подтверждает именно exit, а не только порт.
		return waitPortFree(mp.port, timeout)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-mp.done:
		return true
	case <-timer.C:
		return false
	}
}

func (r *Runner) forgetManagedProc(baseID string, mp *managedProc) {
	r.mu.Lock()
	if current := r.procs[baseID]; current == mp {
		delete(r.procs, baseID)
	}
	r.mu.Unlock()
}

func (r *Runner) stopTrackedHeld(base *Base, mp *managedProc) error {
	port := base.Port
	if mp != nil {
		port = mp.port
	}
	var gracefulErr error
	if mp != nil && mp.cmd != nil && mp.controlToken != "" {
		owned := *base
		owned.Port = mp.port
		owned.ControlToken = mp.controlToken
		expectedPID := 0
		if mp.cmd.Process != nil {
			expectedPID = mp.cmd.Process.Pid
		}
		gracefulErr = requestControlStop(&owned, expectedPID)
		if gracefulErr == nil {
			r.forgetManagedProc(base.ID, mp)
			return nil
		}
	}
	// Только доказанно наш os.Process можно завершить жёстко. Это fallback для
	// процесса, который завис до поднятия control endpoint или не уложился в
	// graceful timeout; kill-by-port для adopted/чужих процессов не используется.
	if mp != nil && mp.cmd != nil {
		killProc(mp.cmd.Process)
	}
	if waitManagedProcessExit(mp, 5*time.Second) {
		r.forgetManagedProc(base.ID, mp)
		if !portFree(port) {
			return fmt.Errorf("собственный процесс базы %q завершён, но порт %d занят другим процессом", base.Name, port)
		}
		if gracefulErr != nil {
			respondLog().Warn("graceful stop базы не удался; завершён собственный процесс",
				"base", base.Name, "err", gracefulErr)
		}
		return nil
	}
	if gracefulErr != nil {
		return fmt.Errorf("база %q не завершилась после graceful stop (%v) и fallback", base.Name, gracefulErr)
	}
	return fmt.Errorf("процесс базы %q не завершился", base.Name)
}

// StopBase безопасно останавливает одну базу под lifecycle lease.
func (r *Runner) StopBase(base *Base) error {
	if err := r.holdStarts(); err != nil {
		return err
	}
	defer r.AllowStarts()
	return r.stopBaseHeld(base)
}

// stopBaseHeld требует уже взятый lifecycle lease.
func (r *Runner) stopBaseHeld(base *Base) error {
	if base == nil {
		return nil
	}
	r.mu.Lock()
	mp, tracked := r.procs[base.ID]
	r.mu.Unlock()
	if tracked && managedProcMatchesBase(mp, base) {
		return r.stopTrackedHeld(base, mp)
	}
	st := r.RuntimeStatus(base)
	if st.Controllable {
		return requestControlStop(base, 0)
	}
	if st.Running {
		return fmt.Errorf("база %q работает, но запущена не этим лаунчером и не поддерживает безопасную остановку; остановите её вручную один раз", base.Name)
	}
	if !portFree(base.Port) {
		return fmt.Errorf("порт %d базы %q занят другим процессом; он не был остановлен", base.Port, base.Name)
	}
	return nil
}

// StopAll останавливает все процессы текущего Runner и все аутентифицированно
// усыновлённые базы из snapshot. Пока операция идёт, Start отклоняется. При
// holdStarts=true запрет остаётся после успеха: close/update должен исключить
// запуск новой базы между проверенной остановкой и завершением launcher.
func (r *Runner) StopAll(bases []*Base, holdStarts bool) error {
	if err := r.holdStarts(); err != nil {
		return err
	}
	return r.stopAllHeld(bases, holdStarts)
}

// stopAllHeld требует lifecycle lease и делает полный preflight до первого
// stop. Неуправляемая/неизвестная занятость порта поэтому не оставляет уже
// остановленные базы при отказе операции.
func (r *Runner) stopAllHeld(bases []*Base, holdStarts bool) error {
	type procInfo struct {
		id   string
		name string
		port int
		mp   *managedProc
	}

	r.mu.Lock()
	trackedProcs := make(map[string]*managedProc, len(r.procs))
	all := make([]procInfo, 0, len(r.procs))
	for id, mp := range r.procs {
		trackedProcs[id] = mp
		all = append(all, procInfo{id: id, port: mp.port, mp: mp})
	}
	r.mu.Unlock()

	names := make(map[string]string, len(bases))
	for _, base := range bases {
		if base != nil {
			names[base.ID] = base.Name
		}
	}
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		errs = append(errs, err)
		errMu.Unlock()
	}
	// Preflight every untracked registered base before touching any process.
	statuses := make([]BaseRuntimeStatus, len(bases))
	for i, base := range bases {
		if base == nil || managedProcMatchesBase(trackedProcs[base.ID], base) {
			continue
		}
		i, base := i, base
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = r.RuntimeStatus(base)
		}()
	}
	wg.Wait()
	for i, base := range bases {
		if base == nil || managedProcMatchesBase(trackedProcs[base.ID], base) {
			continue
		}
		if statuses[i].Occupied && !statuses[i].Controllable {
			recordErr(fmt.Errorf("база %q или её порт %d заняты процессом без подтверждённого безопасного управления",
				base.Name, base.Port))
		}
	}
	if err := errors.Join(errs...); err != nil {
		r.AllowStarts()
		return err
	}

	for i := range all {
		all[i].name = names[all[i].id]
		pi := all[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := &Base{ID: pi.id, Name: pi.name, Port: pi.port}
			recordErr(r.stopTrackedHeld(base, pi.mp))
		}()
	}
	for i, base := range bases {
		base := base
		if base == nil || managedProcMatchesBase(trackedProcs[base.ID], base) || !statuses[i].Controllable {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordErr(requestControlStop(base, 0))
		}()
	}
	wg.Wait()
	err := errors.Join(errs...)
	if err != nil || !holdStarts {
		r.AllowStarts()
	}
	return err
}

// AllowStarts снимает gate после обычного «Стоп всё» или неудачного update.
func (r *Runner) AllowStarts() {
	r.mu.Lock()
	if !r.stopping {
		r.mu.Unlock()
		return
	}
	r.stopping = false
	r.mu.Unlock()
	r.lifecycleMu.Unlock()
}

// killProc terminates a tracked process directly — no external utilities, no CMD windows.
func killProc(p *os.Process) {
	if p == nil {
		return
	}
	// Процесс мог завершиться сам между проверкой и Kill — это не ошибка
	// вызывающего. Успех остановки подтверждается завершением этого же process generation.
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

// RunningIDs возвращает базы, процессы которых ведёт этот лаунчер. Снимок
// нужен обновлению платформы: базы придётся остановить ради подмены бинаря, а
// после перезапуска — поднять ровно те же.
func (r *Runner) RunningIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.procs))
	for id := range r.procs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Healthy сообщает свежую живость базы, включая безопасно усыновлённый процесс
// и совместимый старый onebase с маркированным /healthz.
func (r *Runner) Healthy(base *Base) bool {
	return r.RuntimeStatus(base).Running
}

func (r *Runner) BaseURL(base *Base) string {
	return fmt.Sprintf("http://127.0.0.1:%d", base.Port)
}

func (r *Runner) MigrateBase(ctx context.Context, base *Base) (string, error) {
	exe, err := exePath()
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
// База, пережившая перезапуск launcher, останавливает себя через control API;
// произвольный процесс на сохранённом порту никогда не завершается.
func (r *Runner) Restart(base *Base) error {
	if err := r.holdStarts(); err != nil {
		return err
	}
	defer r.AllowStarts()
	if err := r.stopBaseHeld(base); err != nil {
		return err
	}
	return r.startHeld(base)
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
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", base.Port)
	client := localControlClient(500 * time.Millisecond)
	// procExited ловит и мгновенное падение — процесс, успевший завершиться
	// между Start и WaitReady, из procs уже удалён.
	tracked := r.IsRunning(base.ID) || r.procExited(base.ID)
	if tracked && startupGraceTimeout > timeout {
		timeout = startupGraceTimeout
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if tracked {
			// Public /health=200 недостаточно: при гонке за портом он мог прийти
			// от другого процесса. HMAC identity доказывает именно запущенную базу.
			expectedPID, stillTracked := r.trackedProcessPID(base.ID)
			if stillTracked && controlIdentity(base, expectedPID) {
				return nil
			}
		} else if base.ControlToken != "" {
			// An adopted base has a persistent identity too. Do not fall back to
			// public /health after authenticating it in baseRunning: the original
			// process may have exited and a foreign listener may have won the port.
			if controlIdentity(base, 0) {
				return nil
			}
		} else {
			resp, err := client.Get(healthURL)
			if err == nil {
				resp.Body.Close() //nolint:errcheck,gosec // readiness status only
				if resp.StatusCode == http.StatusOK {
					return nil
				}
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

func (r *Runner) trackedProcessPID(baseID string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mp, ok := r.procs[baseID]
	if !ok {
		return 0, false
	}
	if mp.cmd == nil || mp.cmd.Process == nil {
		return 0, true
	}
	return mp.cmd.Process.Pid, true
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
// Реализация общая с отчётом об ошибке (план 116): тот же журнал, то же чтение
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
// ошибке (план 116): `onebase support` собирает пакет без запущенного лаунчера
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
