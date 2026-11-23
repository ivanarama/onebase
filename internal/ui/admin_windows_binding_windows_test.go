package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"golang.org/x/sys/windows"
)

// Drive the administrator's actual user card against the Windows account
// running the tests. A domain controller is unnecessary for a local account.
func TestAdminCardWindowsAuthentication(t *testing.T) {
	s, ctx := newSubmitTestServer(t, nil)
	repo := auth.NewRepo(s.store)
	s.authRepo = repo
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := repo.Create(ctx, "admin", "password1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := repo.Create(ctx, "operator", "password2", "", false)
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name, domain, _, err := current.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatal(err)
	}
	account := domain + `\` + name
	router := chi.NewRouter()
	s.Mount(router)
	post := func(actor *auth.User, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/ui/admin/users/"+operator.ID, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req.WithContext(auth.ContextWithUser(req.Context(), actor)))
		return res
	}
	bind := url.Values{"action": {"windows_account"}, "windows_enabled": {"1"}, "windows_account": {account}}
	if res := post(operator, bind); res.Code != http.StatusForbidden {
		t.Fatalf("ordinary user setting a binding: HTTP %d", res.Code)
	}
	if res := post(admin, bind); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Привязка Windows сохранена") {
		t.Fatalf("could not bind account through card: HTTP %d", res.Code)
	}
	sid := current.User.Sid.String()
	if u, err := repo.GetByWindowsSID(ctx, sid); err != nil || u.ID != operator.ID || u.IsAdmin {
		t.Fatalf("wrong principal after binding: %v", err)
	}

	// A protected page prompts the browser to negotiate. Explicit logout
	// redirects to /login, where the user must be able to choose another method.
	h := auth.Handlers{Repo: repo, WindowsSPN: "HTTP/erp.example.test"}
	for _, tc := range []struct {
		path string
		auto bool
	}{
		{"/login?return=%2Fui", true},
		{"/login", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "https://erp.example.test"+tc.path, nil)
		res := httptest.NewRecorder()
		h.LoginPage(res, req)
		body := res.Body.String()
		if !strings.Contains(body, "Войти через Windows") || !strings.Contains(body, `data-auto="`+strconv.FormatBool(tc.auto)+`"`) {
			t.Fatalf("incorrect login choices at %s", tc.path)
		}
		if tc.auto && !strings.Contains(body, `data-url="/auth/windows?return=%2Fui"`) {
			t.Fatal("login page lost the original return target")
		}
	}
	post(admin, url.Values{"action": {"account_status"}, "disabled": {"1"}})
	if _, err := repo.GetByWindowsSID(ctx, sid); err == nil {
		t.Fatal("disabled account can still sign in")
	}
	post(admin, url.Values{"action": {"account_status"}})
	if u, err := repo.GetByWindowsSID(ctx, sid); err != nil || u.ID != operator.ID {
		t.Fatal("reactivated account lost its binding")
	}
	post(admin, url.Values{"action": {"windows_account"}, "windows_account": {account}})
	if _, err := repo.GetByWindowsSID(ctx, sid); err == nil {
		t.Fatal("clearing the checkbox did not unlink the account")
	}
}
