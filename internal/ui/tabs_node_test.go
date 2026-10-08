package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
)

// Execute the shell fetched through its public route and real authentication.
// The same storage area is then reused across logout/login in the Node harness.
func TestTabsBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the tabs behavior regression test")
	}
	s := newServerForFormMode(t)
	repo := auth.NewRepo(s.store)
	ctx := context.Background()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s.authRepo = repo
	handlers := &auth.Handlers{Repo: repo}
	router := chi.NewRouter()
	router.Get("/login", handlers.LoginPage)
	router.Post("/auth/login", handlers.LoginJSON)
	router.Post("/logout", handlers.Logout)
	router.Group(func(r chi.Router) { r.Use(repo.Middleware); s.Mount(r) })
	dir := t.TempDir()
	savePage := func(name string, rec *httptest.ResponseRecorder) string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d: %s", name, rec.Code, rec.Body.String())
		}
		path := filepath.Join(dir, name+".html")
		if err := os.WriteFile(path, rec.Body.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// Verify the no-auth deployment through the same middleware before users exist.
	anonymousRequest := httptest.NewRequest(http.MethodGet, "/ui/app", nil)
	anonymousResponse := httptest.NewRecorder()
	router.ServeHTTP(anonymousResponse, anonymousRequest)
	anonymousPage := savePage("shell-open", anonymousResponse)
	for _, login := range []string{"operator", "admin"} {
		if _, err := repo.Create(ctx, login, "secret123", login, login == "admin"); err != nil {
			t.Fatal(err)
		}
	}
	scopePattern := regexp.MustCompile(`var STORAGE_SCOPE=([^;]+);`)
	loginShell := func(login, name string) (*http.Cookie, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"login":"`+login+`","password":"secret123"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login: %d: %s", rec.Code, rec.Body.String())
		}
		var cookie *http.Cookie
		for _, c := range rec.Result().Cookies() {
			if c.Name == "onebase_session" {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatal("session cookie missing")
		}
		req = httptest.NewRequest(http.MethodGet, "/ui/app", nil)
		req.AddCookie(cookie)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), cookie.Value) {
			t.Fatal("session secret exposed in HTML")
		}
		firstScope := scopePattern.FindString(rec.Body.String())
		reload := httptest.NewRequest(http.MethodGet, "/ui/app?home=1", nil)
		reload.AddCookie(cookie)
		reloadRec := httptest.NewRecorder()
		router.ServeHTTP(reloadRec, reload)
		if reloadRec.Code != http.StatusOK || scopePattern.FindString(reloadRec.Body.String()) != firstScope {
			t.Fatal("reload changed the session scope")
		}
		return cookie, savePage(name, rec)
	}
	logout := func(cookie *http.Cookie, name string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/logout", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
			t.Fatalf("logout: %d", rec.Code)
		}
		if _, err := repo.LookupSessionKind(ctx, cookie.Value, auth.SessionKindEnterprise); err == nil {
			t.Fatal("logout kept session valid")
		}
		req = httptest.NewRequest(http.MethodGet, "/login", nil)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return savePage(name, rec)
	}
	firstCookie, first := loginShell("operator", "shell-a")
	loginPage := logout(firstCookie, "login")
	secondCookie, second := loginShell("admin", "shell-b")
	logout(secondCookie, "login-b")
	thirdCookie, third := loginShell("operator", "shell-a-new")
	// A new session for the same account must also start empty.
	logout(thirdCookie, "login-a-new")
	_, fourth := loginShell("operator", "shell-a-again")
	cmd := exec.Command(node, "--test", "static/tabs_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "ONEBASE_TABS_HTML="+first, "ONEBASE_TABS_B_HTML="+second, "ONEBASE_TABS_A_NEW_HTML="+third, "ONEBASE_TABS_A_AGAIN_HTML="+fourth, "ONEBASE_LOGIN_HTML="+loginPage, "ONEBASE_TABS_OPEN_HTML="+anonymousPage)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node tabs behavior test: %v\n%s", err, output)
	}
	t.Logf("node tabs behavior test:\n%s", output)
}
