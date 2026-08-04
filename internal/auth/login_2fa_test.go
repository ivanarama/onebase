package auth_test

// Вход со вторым фактором и политиками (план 84): сессия не выдаётся, пока код
// не предъявлен; политика require_2fa не пускает учётку без второго фактора;
// sso_only отключает локальные пароли.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

// postLogin отправляет форму входа и возвращает ответ.
func postLogin(t *testing.T, h *auth.Handlers, login, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"login": {login}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.LoginSubmit(rec, req)
	return rec
}

// cookieNamed достаёт куку из ответа.
func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestLoginWithTOTPWithholdsSessionUntilCode(t *testing.T) {
	repo, ctx := newTestRepo(t)
	userID, secret := enable2FA(t, repo, ctx, "ivan")
	h := &auth.Handlers{Repo: repo}

	rec := postLogin(t, h, "ivan", "S3cret-pass")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("сессия выдана до предъявления второго фактора")
	}
	challenge := cookieNamed(rec, "onebase_2fa")
	if challenge == nil {
		t.Fatal("не выдан challenge второго фактора")
	}
	if !strings.Contains(rec.Body.String(), "Подтверждение входа") {
		t.Fatalf("ожидалась страница ввода кода, получено: %s", firstLine(rec.Body.String()))
	}

	// Неверный код сессию не даёт.
	bad := postTwoFactor(t, h, challenge, "000000")
	if cookieNamed(bad, "onebase_session") != nil {
		t.Fatal("сессия выдана по неверному коду")
	}

	now := time.Now()
	code, err := auth.TOTPCode(secret, auth.TOTPStep(now))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	ok := postTwoFactor(t, h, challenge, code)
	session := cookieNamed(ok, "onebase_session")
	if session == nil {
		t.Fatalf("сессия не выдана после верного кода (код ответа %d)", ok.Code)
	}
	user, err := repo.LookupSession(ctx, session.Value)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if user.ID != userID {
		t.Fatalf("сессия принадлежит %s, ожидался %s", user.ID, userID)
	}
}

func postTwoFactor(t *testing.T, h *auth.Handlers, challenge *http.Cookie, code string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"code": {code}}
	req := httptest.NewRequest(http.MethodPost, "/login/2fa", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(challenge)
	rec := httptest.NewRecorder()
	h.TwoFactorSubmit(rec, req)
	return rec
}

func TestLoginDemandsEnrollmentWhenPolicyRequiresTwoFactor(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "admin", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	user, err := repo.Create(ctx, "buh", "S3cret-pass", "Бухгалтер", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	roles := []*auth.Role{{Name: "Бухгалтерия"}}
	if err := repo.SyncRoles(ctx, roles); err != nil {
		t.Fatalf("SyncRoles: %v", err)
	}
	if err := repo.AssignRole(ctx, user.ID, roles[0].ID); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{Require2FARoles: []string{"Бухгалтерия"}}); err != nil {
		t.Fatalf("SaveAuthPolicy: %v", err)
	}

	h := &auth.Handlers{Repo: repo}
	rec := postLogin(t, h, "buh", "S3cret-pass")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("учётка без второго фактора вошла вопреки политике")
	}
	if !strings.Contains(rec.Body.String(), "Требуется второй фактор") {
		t.Fatalf("ожидалась принудительная настройка 2FA, получено: %s", firstLine(rec.Body.String()))
	}

	// Учётка, которой политика не касается, входит как раньше.
	other := postLogin(t, h, "admin", "S3cret-pass")
	if cookieNamed(other, "onebase_session") == nil {
		t.Fatal("политика подействовала на учётку без защищённой роли")
	}
}

func TestLoginUnaffectedWithoutPolicyAndTwoFactor(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "admin", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := &auth.Handlers{Repo: repo}
	rec := postLogin(t, h, "admin", "S3cret-pass")
	if rec.Code != http.StatusFound {
		t.Fatalf("обычный вход вернул %d, ожидался редирект", rec.Code)
	}
	if cookieNamed(rec, "onebase_session") == nil {
		t.Fatal("обычный вход не выдал сессию")
	}
}

func TestSSOOnlyRejectsPasswordLogin(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "admin", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{SSOOnly: true}); err != nil {
		t.Fatalf("SaveAuthPolicy: %v", err)
	}
	h := &auth.Handlers{Repo: repo}

	rec := postLogin(t, h, "admin", "S3cret-pass")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("вход по паролю вернул %d, ожидался 403", rec.Code)
	}
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("сессия выдана вопреки sso_only")
	}

	// Аварийный обход: администратор процесса может вернуть вход по паролю.
	t.Setenv("ONEBASE_ALLOW_PASSWORD_LOGIN", "1")
	rec = postLogin(t, h, "admin", "S3cret-pass")
	if cookieNamed(rec, "onebase_session") == nil {
		t.Fatalf("аварийный вход по паролю не сработал (код %d)", rec.Code)
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i > 0 && i < 200 {
		return s[:i]
	}
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
