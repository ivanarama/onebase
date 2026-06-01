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
}

func NewRunner() *Runner {
	return &Runner{procs: make(map[string]*managedProc)}
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

func (r *Runner) Start(base *Base) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.procs[base.ID]; ok {
		return fmt.Errorf("база %q уже запущена", base.Name)
	}

	// check port conflict with other running bases (tracked)
	for _, mp := range r.procs {
		if mp.port == base.Port {
			return fmt.Errorf("порт %d уже занят другой запущенной базой", base.Port)
		}
	}

	// OS-level port availability check: catches leftover processes not tracked by runner
	if !portFree(base.Port) {
		return fmt.Errorf("порт %d уже занят другим процессом — остановите его вручную или смените порт базы", base.Port)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("runner: executable: %w", err)
	}

	logPath, err := baseLogPath(base.ID)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("runner: log: %w", err)
	}

	var args []string
	if base.DBType == "sqlite" {
		args = []string{"run", "--sqlite", base.DBPath, "--port", fmt.Sprintf("%d", base.Port)}
	} else {
		args = []string{"run", "--db", base.DB, "--port", fmt.Sprintf("%d", base.Port)}
	}
	if base.ConfigSource == "file" {
		args = append(args, "--project", base.Path)
	} else {
		args = append(args, "--config-source", "database")
	}

	// Per-base секрет для debug API: процесс базы примет запросы к /debug/global/*
	// только с этим токеном (см. ui.MountDebug). Конфигуратор-прокси его прикладывает.
	debugToken, err := generateDebugToken()
	if err != nil {
		logFile.Close()
		return fmt.Errorf("runner: debug token: %w", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "ONEBASE_DEBUG_TOKEN="+debugToken)
	noWindow(cmd)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("runner: start: %w", err)
	}

	r.procs[base.ID] = &managedProc{cmd: cmd, port: base.Port, startedAt: time.Now(), debugToken: debugToken}

	go func() {
		cmd.Wait()
		logFile.Close()
		r.mu.Lock()
		delete(r.procs, base.ID)
		r.mu.Unlock()
	}()

	return nil
}

func (r *Runner) Stop(baseID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	mp, ok := r.procs[baseID]
	if !ok {
		return nil
	}
	killProc(mp.cmd.Process)
	delete(r.procs, baseID)
	return nil
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
		waitPortFree(port, 3*time.Second)
	}
}

// killByPort finds and kills any process listening on the given TCP port.
func killByPort(port int) {
	switch runtime.GOOS {
	case "windows":
		// runPowerShell runs with -WindowStyle Hidden — no CMD flash.
		runPowerShell(fmt.Sprintf(
			`$c = Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue
			 if ($c) { Stop-Process -Id $c.OwningProcess -Force -ErrorAction SilentlyContinue }`,
			port))
	case "darwin":
		target := fmt.Sprintf(":%d", port)
		out, _ := exec.Command("lsof", "-ti", target).Output()
		if pid := strings.TrimSpace(string(out)); pid != "" {
			for _, p := range strings.Fields(pid) {
				exec.Command("kill", "-9", p).Run()
			}
		}
	case "linux":
		target := fmt.Sprintf(":%d", port)
		out, _ := exec.Command("sh", "-c", fmt.Sprintf("ss -tlnp 2>/dev/null | grep '%s '", target)).Output()
		for _, line := range strings.Split(string(out), "\n") {
			if idx := strings.Index(line, "pid="); idx >= 0 {
				rest := line[idx+4:]
				if end := strings.IndexAny(rest, ",\n "); end > 0 {
					exec.Command("kill", "-9", rest[:end]).Run()
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
	p.Kill()
}

// portFree reports whether the TCP port is free on localhost.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// waitPortFree blocks until the port becomes free or timeout expires.
func waitPortFree(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portFree(port) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
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

	cmd := exec.CommandContext(ctx, exe, args...)
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
func (r *Runner) Restart(base *Base) error {
	r.Stop(base.ID)
	waitPortFree(base.Port, 3*time.Second)
	return r.Start(base)
}

// WaitReady polls /health on the base's port until it responds 200 or timeout.
func (r *Runner) WaitReady(base *Base, timeout time.Duration) error {
	url := fmt.Sprintf("http://localhost:%d/health", base.Port)
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("сервер не ответил на порту %d за %s", base.Port, timeout)
}

func baseLogPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".onebase", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".log"), nil
}

// migrateMarkerPath возвращает путь к файлу-метке времени последней успешной
// миграции базы. Метка лежит в служебной папке лаунчера (а не в каталоге
// конфигурации), чтобы не попадать в скан .os/.yaml и в git.
func migrateMarkerPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".onebase", "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	if err := os.WriteFile(p, []byte(now.Format(time.RFC3339)), 0o644); err == nil {
		os.Chtimes(p, now, now)
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
