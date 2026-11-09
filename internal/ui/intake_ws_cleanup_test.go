package ui

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Hijacked WebSocket connections are not closed by httptest.Server.Close.
// A hub session waiting for its context must finish when its test scope ends.
func TestWSIntakeTestServer_ShutdownReleasesSession(t *testing.T) {
	started, stopped, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release) // Also release the handler when testing the old helper.
	t.Run("server-scope", func(t *testing.T) {
		srv := wsIntakeTestServer(t, func(ctx context.Context, _ int64, _ *websocket.Conn) {
			close(started)
			defer close(stopped)
			select {
			case <-ctx.Done():
			case <-release:
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, resp, err := websocket.Dial(ctx, wsTestURL(srv), nil)
		if resp != nil && resp.Body != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("hub session did not start")
		}
	})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("test server cleanup left a hijacked session running")
	}
}
