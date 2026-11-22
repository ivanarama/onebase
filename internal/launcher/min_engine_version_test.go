package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/version"
)

// Exercise the HTTP launch endpoint and its visible return page, including
// database-backed manifests. A private comparison alone would miss a dead call.
func TestStartMinimumEngineVersionWarning(t *testing.T) {
	const childEnv = "ONEBASE_MIN_ENGINE_VERSION_WARNING_CHILD"
	const childSentinel = "isolated-http-v1"
	if child := os.Getenv(childEnv); child != childSentinel {
		if child != "" {
			t.Fatalf("unexpected minimum engine warning child sentinel: %q", child)
		}
		// Other HTTP tests may leave update watchers reading version.Build.
		// Keep this artificial launcher version entirely in a fresh process.
		deadline := time.Now().Add(30 * time.Second)
		if outer, ok := t.Deadline(); ok && outer.Add(-5*time.Second).Before(deadline) {
			deadline = outer.Add(-5 * time.Second)
		}
		ctx, cancel := context.WithDeadline(t.Context(), deadline)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartMinimumEngineVersionWarning$", "-test.v", "-test.timeout=30s") //nolint:gosec // Fixed test pattern in the current test executable.
		cmd.Env = append(os.Environ(), childEnv+"="+childSentinel)
		cmd.WaitDelay = 5 * time.Second
		output, err := cmd.CombinedOutput()
		if err != nil || ctx.Err() != nil {
			t.Fatalf("minimum engine warning HTTP subprocess: %v; context: %v\n%s", err, ctx.Err(), output)
		}
		t.Logf("minimum engine warning HTTP subprocess:\n%s", output)
		return
	}
	// This child runs only this test, sets Build before any HTTP goroutines,
	// and waits for each httptest.Server cleanup before exiting.
	version.Build = "v9.0.0"
	for _, source := range []string{"file", "database"} {
		for _, requirement := range []string{"2.0.0", "1.2.3", "1.0.0", ""} {
			t.Run(source+"/"+requirement, func(t *testing.T) {
				control := authenticatedControlHandler(t, "secret", "base-control", nil)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/healthz" {
						w.Header().Set("X-OneBase-Version", "v1.2.3")
						w.WriteHeader(http.StatusOK)
						return
					}
					control.ServeHTTP(w, r)
				}))
				t.Cleanup(server.Close)
				base := controlTestBase(t, server, "secret")
				base.ConfigSource, base.DBType = source, "sqlite"
				base.Path, base.DBPath = t.TempDir(), filepath.Join(t.TempDir(), "base.db")
				body := []byte("name: warning-test\nmin_engine_version: '" + requirement + "'\n")
				if source == "file" {
					if err := os.Mkdir(filepath.Join(base.Path, "config"), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(base.Path, "config", "app.yaml"), body, 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					db, err := storage.ConnectSQLite(context.Background(), base.DBPath)
					if err != nil {
						t.Fatal(err)
					}
					repo := configdb.New(db)
					if err := repo.EnsureSchema(context.Background()); err != nil {
						t.Fatal(err)
					}
					if err := repo.SaveFile(context.Background(), "config/app.yaml", body); err != nil {
						t.Fatal(err)
					}
					db.Close()
				}
				store := newTestStore(t)
				if err := store.Add(base); err != nil {
					t.Fatal(err)
				}
				h := &handler{store: store, runner: NewRunner()}
				router := chi.NewRouter()
				router.Post("/bases/{id}/start", h.start)
				router.Get("/", h.index)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bases/"+base.ID+"/start?lang=ru", nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("startup blocked: %d %s", rec.Code, rec.Body.String())
				}
				var result map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["url"] == "" {
					t.Fatal("launch success lost its base URL")
				}
				if requirement != "2.0.0" {
					if result["launcher_url"] != "" {
						t.Fatalf("unnecessary warning: %+v", result)
					}
					return
				}
				returnURL, err := url.Parse(result["launcher_url"])
				if err != nil || returnURL.Query().Get("flash") == "" {
					t.Fatalf("warning lost on navigation: %+v", result)
				}
				page := httptest.NewRecorder()
				router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, result["launcher_url"]+"&lang=ru", nil))
				if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "требуется платформа не ниже 2.0.0, установлена v1.2.3") {
					t.Fatalf("warning is not visible: %d %s", page.Code, page.Body.String())
				}
			})
		}
	}
}
