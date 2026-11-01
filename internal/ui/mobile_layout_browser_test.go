package ui

import (
	"context"
	"encoding/json"
	"fmt"
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
	conn     *websocket.Conn
	stopTree func() error
	exited   chan struct{}
	waitErr  error // read only after exited closes
	logPath  string
	nextID   int
}

func newMobileLayoutBrowser(t *testing.T, executable string) *mobileLayoutBrowser {
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
	args := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-extensions", "--disable-sync", "--no-first-run", "--no-default-browser-check", "--force-device-scale-factor=1", "--window-size=1440,1000", "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(processCtx, executable, args...) //nolint:gosec // closed browser allow-list, owned profile and loopback targets
	cmd.Stdout, cmd.Stderr = log, log
	stopTree, err := startMobileBrowserProcess(cmd)
	if err != nil {
		stop()
		_ = log.Close()
		t.Fatalf("start browser: %v", err)
	}
	b := &mobileLayoutBrowser{exited: make(chan struct{}), logPath: logPath, stopTree: stopTree}
	go func() { b.waitErr = cmd.Wait(); close(b.exited) }()
	t.Cleanup(func() {
		defer stop()
		defer func() { _ = log.Close() }()
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
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		data, readErr := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		parts := strings.Fields(string(data))
		if readErr == nil && len(parts) == 2 {
			port, portErr := strconv.Atoi(parts[0])
			if portErr != nil || port < 1 || port > 65535 || !strings.HasPrefix(parts[1], "/devtools/browser/") {
				t.Fatalf("invalid browser debugging endpoint: %q", data)
			}
			endpoint := (&url.URL{Scheme: "ws", Host: net.JoinHostPort("127.0.0.1", parts[0]), Path: parts[1]}).String()
			conn, response, dialErr := websocket.Dial(ctx, endpoint, nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if dialErr != nil {
				t.Fatalf("connect browser: %v\n%s", dialErr, b.diagnostics())
			}
			conn.SetReadLimit(1 << 20)
			b.conn = conn
			return b
		}
		select {
		case <-b.exited:
			t.Fatalf("browser stopped before ready: %v\n%s", b.waitErr, b.diagnostics())
		case <-ctx.Done():
			t.Fatalf("browser readiness: %v\n%s", ctx.Err(), b.diagnostics())
		case <-tick.C:
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

func (b *mobileLayoutBrowser) measure(ctx context.Context, target string) (data []byte, err error) {
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
			"expression":    `({measurement: document.getElementById('list-mobile-measure')?.textContent || '', error: document.getElementById('list-mobile-error')?.textContent || ''})`,
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
	return string(data[max(0, len(data)-4000):])
}
