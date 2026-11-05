package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Keep one owned browser for the matrix and read the production measurement
// directly. The headless --dump-dom command can time out loading an iframe or
// waiting to exit even after its measurement script has finished (issue #2226).
type mobileLayoutBrowser struct {
	conn      *websocket.Conn
	stopTree  func() error
	exited    chan struct{}
	waitErr   error // read only after exited closes
	logPath   string
	nextID    int
	readiness *mobileLayoutReadinessTrace
}

// Only the constructor and dial loop write this bounded, synchronous journal.
// Process state is observed through exited; cmd.Wait remains its sole owner.
type mobileLayoutReadinessTrace struct {
	started time.Time
	events  [128]string
	count   int
}

func newMobileLayoutReadinessTrace() *mobileLayoutReadinessTrace {
	return &mobileLayoutReadinessTrace{started: time.Now()}
}

func (r *mobileLayoutReadinessTrace) record(ctx context.Context, phase string, attempt int, since time.Time, status int, err error, exited <-chan struct{}) {
	now := time.Now()
	remaining := "unavailable"
	var parentErr error
	if ctx != nil {
		parentErr = ctx.Err()
		if deadline, ok := ctx.Deadline(); ok {
			remaining = deadline.Sub(now).String()
		}
	}
	processExited := false
	select {
	case <-exited:
		processExited = true
	default:
	}
	duration := time.Duration(0)
	if !since.IsZero() {
		duration = now.Sub(since)
	}
	r.events[r.count%len(r.events)] = fmt.Sprintf("at=%s elapsed=%s phase=%s attempt=%d duration=%s remaining=%s response=%t http_status=%d error_type=%T parent=%v process_exited=%t",
		now.UTC().Format(time.RFC3339Nano), now.Sub(r.started), phase, attempt, duration, remaining, status != 0, status, err, parentErr, processExited)
	r.count++
}

func (r *mobileLayoutReadinessTrace) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "browser readiness events=%d omitted=%d\n", r.count, max(0, r.count-len(r.events)))
	for i := max(0, r.count-len(r.events)); i < r.count; i++ {
		out.WriteString(r.events[i%len(r.events)])
		out.WriteByte('\n')
	}
	return out.String()
}

func newMobileLayoutBrowser(t *testing.T, executable string) *mobileLayoutBrowser {
	t.Helper()
	return newMobileLayoutBrowserSize(t, executable, 1440, 1000)
}

func newMobileLayoutBrowserSize(t *testing.T, executable string, width, height int) *mobileLayoutBrowser {
	t.Helper()
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	logPath := filepath.Join(dir, "browser.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// testing cancels t.Context before cleanup; keep the process alive until
	// cleanup stops its owned process tree and releases the owned profile.
	processCtx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check", "--force-device-scale-factor=1", "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"}
	// Zero preserves the browser default viewport for legacy scenarios.
	if width > 0 && height > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", width, height))
	}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(processCtx, executable, args...) //nolint:gosec // closed browser allow-list, owned profile and loopback targets
	cmd.Stdout, cmd.Stderr = log, log
	trace := newMobileLayoutReadinessTrace()
	launchStarted := time.Now()
	trace.record(nil, "launch-start", 0, time.Time{}, 0, nil, nil)
	stopTree, err := startMobileBrowserProcess(cmd)
	trace.record(nil, "launch-end", 0, launchStarted, 0, err, nil)
	if err != nil {
		stop()
		_ = log.Close()
		(&mobileLayoutBrowser{logPath: logPath, readiness: trace}).preserveDiagnostics(t)
		t.Fatalf("start browser: %v", err)
	}
	b := &mobileLayoutBrowser{exited: make(chan struct{}), logPath: logPath, stopTree: stopTree, readiness: trace}
	go func() { b.waitErr = cmd.Wait(); close(b.exited) }()
	t.Cleanup(func() {
		defer stop()
		defer func() {
			_ = log.Close()
			if t.Failed() || os.Getenv("ONEBASE_BROWSER_DIAGNOSTICS_DIR") != "" {
				b.preserveDiagnostics(t)
			}
		}()
		if b.conn != nil {
			_ = b.conn.CloseNow()
		}
		select {
		case <-b.exited:
			if b.waitErr != nil {
				t.Errorf("browser exited before teardown: %v\n%s", b.waitErr, b.diagnostics())
			}
		default:
		}
		if err := b.stopTree(); err != nil {
			t.Errorf("stop owned browser process tree: %v", err)
		}
		select {
		case <-b.exited:
		case <-time.After(5 * time.Second):
			t.Errorf("browser process tree did not exit\n%s", b.diagnostics())
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	trace.record(ctx, "readiness-start", 0, time.Time{}, 0, nil, b.exited)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		data, readErr := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		parts := strings.Fields(string(data))
		if readErr == nil && len(parts) == 2 {
			trace.record(ctx, "devtools-port-observed", 0, time.Time{}, 0, nil, b.exited)
			port, portErr := strconv.Atoi(parts[0])
			if portErr != nil || port < 1 || port > 65535 || !strings.HasPrefix(parts[1], "/devtools/browser/") {
				t.Fatalf("invalid browser debugging endpoint: %q", data)
			}
			endpoint := (&url.URL{Scheme: "ws", Host: net.JoinHostPort("127.0.0.1", parts[0]), Path: parts[1]}).String()
			conn, dialErr := dialMobileLayoutBrowser(ctx, endpoint, b.exited, trace)
			if dialErr != nil {
				t.Fatalf("connect browser: %v\n%s", dialErr, b.diagnostics())
			}
			conn.SetReadLimit(1 << 20)
			b.conn = conn
			return b
		}
		select {
		case <-b.exited:
			trace.record(ctx, "process-exited-before-port", 0, time.Time{}, 0, b.waitErr, b.exited)
			t.Fatalf("browser stopped before ready: %v\n%s", b.waitErr, b.diagnostics())
		case <-ctx.Done():
			trace.record(ctx, "port-deadline", 0, time.Time{}, 0, ctx.Err(), b.exited)
			t.Fatalf("browser readiness: %v\n%s", ctx.Err(), b.diagnostics())
		case <-tick.C:
		}
	}
}

// A published DevTools port can precede a responsive WebSocket endpoint. Bound
// each transport handshake within the original readiness budget; HTTP/protocol
// failures remain terminal and no browser or test body is restarted.
func dialMobileLayoutBrowser(ctx context.Context, endpoint string, exited <-chan struct{}, trace *mobileLayoutReadinessTrace) (*websocket.Conn, error) {
	var lastTransport error
	attempt := 0
	stateError := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-exited:
			return fmt.Errorf("owned browser stopped before ready")
		default:
			return nil
		}
	}
	stopped := func(err error) (*websocket.Conn, error) {
		trace.record(ctx, "stopped", attempt, time.Time{}, 0, err, exited)
		return nil, fmt.Errorf("browser readiness: %w; last transport error: %v", err, lastTransport)
	}
	for {
		if err := stateError(); err != nil {
			return stopped(err)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		attempt++
		attemptStarted := time.Now()
		trace.record(ctx, "handshake-start", attempt, time.Time{}, 0, nil, exited)
		finished := make(chan struct{})
		go func() {
			select {
			case <-exited:
				cancel()
			case <-finished:
			}
		}()
		conn, response, err := websocket.Dial(attemptCtx, endpoint, nil)
		close(finished)
		cancel()
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		trace.record(ctx, "handshake-end", attempt, attemptStarted, status, err, exited)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			lastTransport = err
		}
		if stateErr := stateError(); stateErr != nil {
			if conn != nil {
				_ = conn.CloseNow()
			}
			return stopped(stateErr)
		}
		if err == nil {
			trace.record(ctx, "ready", attempt, time.Time{}, status, nil, exited)
			return conn, nil
		}
		if conn != nil {
			_ = conn.CloseNow()
		}
		if response != nil {
			trace.record(ctx, "terminal-response", attempt, time.Time{}, status, err, exited)
			return nil, fmt.Errorf("browser WebSocket handshake: %w", err)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-exited:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (b *mobileLayoutBrowser) call(ctx context.Context, session, method string, params any, out any) error {
	b.nextID++
	id := b.nextID
	request := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		request["sessionId"] = session
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := b.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return err
	}
	for {
		_, data, err := b.conn.Read(ctx)
		if err != nil {
			return err
		}
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			return err
		}
		if reply.ID != id {
			continue // protocol events from page creation/navigation
		}
		if reply.Error != nil {
			return fmt.Errorf("%s: %s", method, reply.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(reply.Result, out)
	}
}

func (b *mobileLayoutBrowser) measure(ctx context.Context, target string) ([]byte, error) {
	return b.measureElement(ctx, target, "list-mobile-measure")
}

func (b *mobileLayoutBrowser) measureElement(ctx context.Context, target, marker string) (data []byte, err error) {
	markerJSON, _ := json.Marshal(marker)
	var isolated struct {
		ID string `json:"browserContextId"`
	}
	if err := b.call(ctx, "", "Target.createBrowserContext", nil, &isolated); err != nil {
		return nil, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if closeErr := b.call(closeCtx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": isolated.ID}, nil); closeErr != nil && err == nil {
			err = fmt.Errorf("close browser context: %w", closeErr)
		}
	}()
	var page struct {
		TargetID string `json:"targetId"`
	}
	if err := b.call(ctx, "", "Target.createTarget", map[string]any{"url": target, "browserContextId": isolated.ID}, &page); err != nil {
		return nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": page.TargetID, "flatten": true}, &attached); err != nil {
		return nil, err
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var evaluation struct {
			Result struct {
				Value struct {
					Measurement string `json:"measurement"`
					Error       string `json:"error"`
				} `json:"value"`
			} `json:"result"`
			ExceptionDetails json.RawMessage `json:"exceptionDetails"`
		}
		err := b.call(ctx, attached.SessionID, "Runtime.evaluate", map[string]any{
			"expression":    fmt.Sprintf(`({measurement: document.getElementById(%s)?.textContent || '', error: document.getElementById('list-mobile-error')?.textContent || ''})`, markerJSON),
			"returnByValue": true,
		}, &evaluation)
		if err != nil {
			return nil, err
		}
		if len(evaluation.ExceptionDetails) != 0 {
			return nil, fmt.Errorf("measurement evaluation: %s", evaluation.ExceptionDetails)
		}
		if evaluation.Result.Value.Error != "" {
			return nil, fmt.Errorf("measurement script: %s", evaluation.Result.Value.Error)
		}
		if value := evaluation.Result.Value.Measurement; value != "" {
			return []byte(value), nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("measurement was not produced: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

func (b *mobileLayoutBrowser) diagnostics() string {
	data, _ := os.ReadFile(b.logPath)
	return b.readiness.String() + string(data[max(0, len(data)-4000):])
}

func (b *mobileLayoutBrowser) preserveDiagnostics(t *testing.T) {
	t.Helper()
	dir := os.Getenv("ONEBASE_BROWSER_DIAGNOSTICS_DIR")
	var err error
	if dir == "" {
		dir, err = os.MkdirTemp("", "onebase-browser-diagnostics-")
	} else {
		err = os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		t.Errorf("preserve owned browser diagnostics: %v", err)
		return
	}
	saved, err := os.CreateTemp(dir, "browser-*.log")
	if err != nil {
		t.Errorf("preserve owned browser diagnostics: %v", err)
		return
	}
	defer func() { _ = saved.Close() }()
	full, err := os.Open(b.logPath)
	if err != nil {
		t.Errorf("read owned browser diagnostics: %v", err)
		return
	}
	defer func() { _ = full.Close() }()
	if _, err := io.WriteString(saved, b.readiness.String()); err != nil {
		t.Errorf("preserve readiness journal: %v", err)
		return
	}
	if _, err := io.Copy(saved, full); err != nil {
		t.Errorf("preserve full owned browser stdout/stderr: %v", err)
		return
	}
	t.Logf("Full owned browser diagnostics preserved privately: %s", saved.Name())
}
