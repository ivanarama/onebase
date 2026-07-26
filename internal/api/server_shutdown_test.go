package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

func TestShutdownClosesSSEAndOwnedBackgroundResources(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "shutdown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	srv := New(runtime.NewRegistry(), db, interpreter.New(), authRepo, "127.0.0.1", 0, ui.Config{}, nil)
	t.Cleanup(srv.uiSrv.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.srv.Serve(listener) }()

	resp, err := http.Get("http://" + listener.Addr().String() + "/ui/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", resp.StatusCode)
	}
	deadline := time.Now().Add(time.Second)
	for srv.uiSrv.SSESubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.uiSrv.SSESubscriberCount() != 1 {
		t.Fatal("SSE subscriber was not registered")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("read closed SSE response: %v", err)
	}
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop")
	}
}
