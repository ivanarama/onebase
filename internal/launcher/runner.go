package launcher

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type managedProc struct {
	cmd  *exec.Cmd
	port int
}

// Runner tracks running base processes.
type Runner struct {
	mu    sync.Mutex
	procs map[string]*managedProc
}

func NewRunner() *Runner {
	return &Runner{procs: make(map[string]*managedProc)}
}

func (r *Runner) Start(base *Base) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.procs[base.ID]; ok {
		return fmt.Errorf("база %q уже запущена", base.Name)
	}

	// check port conflict with other running bases
	for _, mp := range r.procs {
		if mp.port == base.Port {
			return fmt.Errorf("порт %d уже занят другой запущенной базой", base.Port)
		}
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

	args := []string{"run", "--db", base.DB, "--port", fmt.Sprintf("%d", base.Port)}
	if base.ConfigSource == "file" {
		args = append(args, "--project", base.Path)
	} else {
		args = append(args, "--config-source", "database")
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("runner: start: %w", err)
	}

	r.procs[base.ID] = &managedProc{cmd: cmd, port: base.Port}

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

// StopAll kills all running base processes.
func (r *Runner) StopAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.procs))
	for id := range r.procs {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	for _, id := range ids {
		r.mu.Lock()
		mp, ok := r.procs[id]
		if ok {
			killProc(mp.cmd.Process)
			delete(r.procs, id)
		}
		r.mu.Unlock()
	}
}

// killProc terminates a process. On Windows uses taskkill /F /T to also kill children.
func killProc(p *os.Process) {
	if p == nil {
		return
	}
	if runtime.GOOS == "windows" {
		exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", p.Pid)).Run()
		return
	}
	p.Kill()
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

	args := []string{"migrate", "--db", base.DB}
	if base.ConfigSource == "file" {
		args = append(args, "--project", base.Path)
	} else {
		args = append(args, "--config-source", "database")
	}

	cmd := exec.CommandContext(ctx, exe, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
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
