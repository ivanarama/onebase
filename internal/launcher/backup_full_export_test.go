package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/storage"
)

func universalExportHandler(t *testing.T, withConfig bool) (*handler, *Base) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "export.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create products: %v", err)
	}
	db.Close()

	configDir := t.TempDir()
	if withConfig {
		if err := os.WriteFile(filepath.Join(configDir, "app.yaml"), []byte("name: Export test\n"), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	b := &Base{
		ID: "universal-export", Name: "export-test", ConfigSource: "file", Path: configDir,
		DBType: "sqlite", DBPath: dbPath, Port: waitReadyFreePort(t),
	}
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := store.Add(b); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	return &handler{store: store, runner: NewRunner()}, b
}

func postFullExport(t *testing.T, h *handler, b *Base, compatible bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"compatible": {"false"}}
	if compatible {
		form.Set("compatible", "true")
	}
	req := httptest.NewRequest(http.MethodPost,
		"/bases/"+b.ID+"/configurator/backup/full-export", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.backupFullExport(rec, req)
	return rec
}

func postUniversalExport(t *testing.T, h *handler, b *Base) *httptest.ResponseRecorder {
	t.Helper()
	return postFullExport(t, h, b, true)
}

func TestBackupFullExportUniversalPublishesOnlyCompleteArchive(t *testing.T) {
	h, b := universalExportHandler(t, true)
	rec := postUniversalExport(t, h, b)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, ".obz") {
		t.Fatalf("missing download disposition: %q", got)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a complete ZIP: %v", err)
	}
	want := map[string]bool{"config/app.yaml": false, "manifest.json": false, "META.txt": false}
	for _, f := range zr.File {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("archive is missing %s", name)
		}
	}
}

func TestAttachmentDispositionQuotesAndStripsUnsafeFilenameBytes(t *testing.T) {
	value := attachmentDisposition("base name\r\nX-Injected: yes.obz")
	if strings.ContainsAny(value, "\r\n") || strings.Contains(value, "X-Injected:") {
		t.Fatalf("unsafe Content-Disposition = %q", value)
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", value, err)
	}
	if mediaType != "attachment" || params["filename"] != "base nameX-Injected_ yes.obz" {
		t.Fatalf("parsed disposition = %q %#v", mediaType, params)
	}
}

func TestBackupFullExportRejectsIncompleteBinaryMode(t *testing.T) {
	h, b := universalExportHandler(t, true)
	rec := postFullExport(t, h, b, false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("export status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("rejected binary export advertised a download: %q", got)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "бинар") {
		t.Fatalf("binary rejection is not explained: %q", rec.Body.String())
	}
}

func TestBackupFullExportUniversalFailureHasNoAttachmentHeadersOrPartialZIP(t *testing.T) {
	h, b := universalExportHandler(t, false)
	rec := postUniversalExport(t, h, b)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("export status=%d, want 500; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("failed export advertised an attachment: %q", got)
	}
	if bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Fatalf("failed export leaked a partial ZIP response (%d bytes)", rec.Body.Len())
	}
	// AJAX errors may be JSON or plain text depending on the HTTP boundary; in
	// either form the response must name the missing configuration.
	body := rec.Body.String()
	var payload map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &payload) == nil {
		body, _ = payload["error"].(string)
	}
	if !strings.Contains(strings.ToLower(body), "config") && !strings.Contains(strings.ToLower(body), "конфигурац") {
		t.Fatalf("failure does not explain the missing configuration: %q", body)
	}
}

func TestFullExportSnapshotHoldsCfgLifecycleAndDatabaseLeases(t *testing.T) {
	h, b := universalExportHandler(t, true)
	gate := cfgAuthDBGate(b.ID)

	err := h.withFullExportSnapshot(context.Background(), b, func() error {
		if gate.TryRLock() {
			gate.RUnlock()
			t.Fatal("full export did not hold the configurator exclusive gate")
		}
		if h.runner.lifecycleMu.TryLock() {
			h.runner.lifecycleMu.Unlock()
			t.Fatal("full export did not hold the lifecycle gate")
		}
		lease, err := dblock.AcquireSQLite(b.DBPath)
		if err == nil {
			_ = lease.Close()
			t.Fatal("full export did not hold the cross-process database lease")
		}
		if !errors.Is(err, dblock.ErrLocked) {
			t.Fatalf("second database lease error = %v, want ErrLocked", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withFullExportSnapshot: %v", err)
	}
	if !gate.TryRLock() {
		t.Fatal("full export leaked the configurator gate")
	}
	gate.RUnlock()
	if !h.runner.lifecycleMu.TryLock() {
		t.Fatal("full export leaked the lifecycle gate")
	}
	h.runner.lifecycleMu.Unlock()
}

func TestFullExportSnapshotRestartsRunningBaseOnEveryExportOutcome(t *testing.T) {
	for _, tc := range []struct {
		name      string
		exportErr error
	}{
		{name: "success"},
		{name: "export failure", exportErr: errors.New("intentional export failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b := universalExportHandler(t, true)
			b.ControlToken = "full-export-generation"
			if err := h.store.Update(b); err != nil {
				t.Fatalf("store.Update: %v", err)
			}
			h.runner.procs[b.ID] = &managedProc{port: b.Port, controlToken: b.ControlToken}

			oldExePath := exePath
			defer func() { exePath = oldExePath }()
			restartFailure := errors.New("intentional restart failure")
			var starts atomic.Int32
			exePath = func() (string, error) {
				starts.Add(1)
				return "", restartFailure
			}

			err := h.withFullExportSnapshot(context.Background(), b, func() error {
				return tc.exportErr
			})
			if starts.Load() != 1 {
				t.Fatalf("restart attempts = %d, want 1", starts.Load())
			}
			if !errors.Is(err, restartFailure) {
				t.Fatalf("result = %v, want restart failure", err)
			}
			if tc.exportErr != nil && !errors.Is(err, tc.exportErr) {
				t.Fatalf("result = %v, want original export failure", err)
			}
			if !h.runner.lifecycleMu.TryLock() {
				t.Fatal("failed restart leaked the lifecycle gate")
			}
			h.runner.lifecycleMu.Unlock()
		})
	}
}

func TestFullExportSnapshotRestartsBaseWhenDatabaseLeaseAcquisitionFails(t *testing.T) {
	h, b := universalExportHandler(t, true)
	b.ControlToken = "full-export-generation"
	if err := h.store.Update(b); err != nil {
		t.Fatalf("store.Update: %v", err)
	}
	h.runner.procs[b.ID] = &managedProc{port: b.Port, controlToken: b.ControlToken}

	blocker, err := dblock.AcquireSQLite(b.DBPath)
	if err != nil {
		t.Fatalf("acquire blocking lease: %v", err)
	}
	defer blocker.Close() //nolint:errcheck // test cleanup

	oldExePath := exePath
	defer func() { exePath = oldExePath }()
	restartFailure := errors.New("intentional restart failure")
	var starts atomic.Int32
	exePath = func() (string, error) {
		starts.Add(1)
		return "", restartFailure
	}

	_, err = h.acquireFullExportSnapshot(context.Background(), b)
	if !errors.Is(err, dblock.ErrLocked) || !errors.Is(err, restartFailure) {
		t.Fatalf("acquire result = %v, want database and restart failures", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("restart attempts = %d, want 1", starts.Load())
	}
}

func TestFullExportSnapshotRejectsStaleBaseRecord(t *testing.T) {
	h, stale := universalExportHandler(t, true)
	updated := *stale
	updated.Name = "changed while export waited"
	if err := h.store.Update(&updated); err != nil {
		t.Fatalf("store.Update: %v", err)
	}
	called := false
	err := h.withFullExportSnapshot(context.Background(), stale, func() error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "параметры") {
		t.Fatalf("stale snapshot result = %v", err)
	}
	if called {
		t.Fatal("export callback ran with a stale Store record")
	}
	if !h.runner.lifecycleMu.TryLock() {
		t.Fatal("stale-record rejection leaked the lifecycle gate")
	}
	h.runner.lifecycleMu.Unlock()
}

func TestBackupFullExportAuthOnlyRouteDoesNotDeadlockOnExclusiveUpgrade(t *testing.T) {
	h, b := universalExportHandler(t, true)
	req := httptest.NewRequest(http.MethodPost,
		"/bases/"+b.ID+"/configurator/backup/full-export?compatible=true", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", b.ID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.cfgAuthMiddleware(http.HandlerFunc(h.backupFullExport)).ServeHTTP(rec, req)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("full export deadlocked while upgrading configurator auth read lease")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body.String())
	}
}
