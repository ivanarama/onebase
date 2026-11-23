package launcher

import (
	"net/http"
	"net/http/httptest"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/api"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

// Exercise both Enterprise buttons with a real process token, a database
// binding resolved by Windows and the production base HTTP router. The only
// substituted side effect is actually opening the desktop window.
func TestEnterpriseWindowsLogin(t *testing.T) {
	const secret = "test-base-control-secret"
	t.Setenv("ONEBASE_CONTROL_TOKEN", secret)
	t.Setenv("ONEBASE_BASE_ID", "base-control")
	ctx := t.Context()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "base.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	operator, err := repo.Create(ctx, "operator", "onebase-test-password", "", false)
	if err != nil {
		t.Fatal(err)
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetWindowsAccount(ctx, operator.ID, account.Username); err != nil {
		t.Fatal(err)
	}
	reg, interp := runtime.NewRegistry(), interpreter.New()
	frontend := ui.New(reg, db, interp, repo, ui.Config{}, nil)
	baseServer := api.New(reg, db, interp, repo, "", 0, api.Config{}, frontend, nil)
	ts := httptest.NewServer(baseServer.Handler())
	t.Cleanup(ts.Close)
	base := controlTestBase(t, ts, secret)
	base.DBType, base.ConfigSource, base.Path = "sqlite", "file", t.TempDir()
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	window := &fakeBrowser{}
	h := &handler{store: store, runner: NewRunner(), isoBrowser: window}
	router := chi.NewRouter()
	router.Post("/bases/{id}/start", h.start)
	router.Post("/bases/{id}/start-native", h.startNative)
	start := func(path string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bases/"+base.ID+path, nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "code=") {
			t.Fatal("launcher failed to open Enterprise or disclosed a private URL")
		}
		return rec.Body.String()
	}
	redeem := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		baseServer.Handler().ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{"/start?open=enterprise", "/start-native"} {
		previous := window.calls
		start(path)
		if window.calls != previous+1 || !strings.HasPrefix(window.url, ts.URL+"/auth/windows/local?code=") {
			t.Fatal("launcher did not open a signed URL for the bound identity")
		}
		unsigned := window.url[:strings.LastIndexByte(window.url, '.')]
		if got := redeem(unsigned); got.Header().Get("Set-Cookie") != "" {
			t.Fatal("unsigned challenge opened a session")
		}
		remote := httptest.NewRecorder()
		baseServer.Handler().ServeHTTP(remote, httptest.NewRequest(http.MethodGet, window.url, nil))
		if remote.Code != http.StatusNotFound || remote.Header().Get("Set-Cookie") != "" {
			t.Fatal("local code must be unusable on a remote connection")
		}
		got := redeem(window.url)
		if got.Code != http.StatusFound || got.Header().Get("Location") != "/ui" {
			t.Fatalf("bootstrap failed: HTTP %d", got.Code)
		}
		cookies := got.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatal("missing Enterprise session")
		}
		var token string
		for _, c := range cookies {
			if c.Name == "onebase_session" {
				token = c.Value
			}
		}
		active, err := repo.LookupSessionKind(ctx, token, auth.SessionKindEnterprise)
		if err != nil || active == nil || active.ID != operator.ID || active.IsAdmin {
			t.Fatal("the session must belong to the bound ordinary Enterprise user")
		}
		if got := redeem(window.url); got.Header().Get("Set-Cookie") != "" {
			t.Fatal("replayed one-time code opened a session")
		}
	}
	start("/start?open=enterprise")
	if err := repo.SetDisabled(ctx, operator.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := redeem(window.url); got.Header().Get("Set-Cookie") != "" {
		t.Fatal("disabled account opened a session with an outstanding code")
	}
	if err := repo.SetDisabled(ctx, operator.ID, false); err != nil {
		t.Fatal(err)
	}
	start("/start?open=enterprise")
	pendingURL := window.url
	if err := repo.SetWindowsAccount(ctx, operator.ID, ""); err != nil {
		t.Fatal(err)
	}
	other, err := repo.Create(ctx, "alternate", "onebase-test-password", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetWindowsAccount(ctx, other.ID, account.Username); err != nil {
		t.Fatal(err)
	}
	if got := redeem(pendingURL); got.Header().Get("Set-Cookie") != "" {
		t.Fatal("reassigning the SID changed the target user of an outstanding code")
	}
	start("/start?open=enterprise")
	pendingURL = window.url
	if err := repo.SetWindowsAccount(ctx, other.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := redeem(pendingURL); got.Header().Get("Set-Cookie") != "" {
		t.Fatal("a code issued before unbinding opened a session")
	}
	previous := window.calls
	if body := start("/start?open=enterprise"); window.calls != previous || !strings.Contains(body, ts.URL) {
		t.Fatal("unbound user did not receive the ordinary base URL")
	}
}
