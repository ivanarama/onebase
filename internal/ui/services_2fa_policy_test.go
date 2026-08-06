package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/httpservice"
)

// `auth: basic` в HTTP-сервисе обязан подчиняться политике входа. Иначе он
// становится обходом: при sso_only локальный пароль всё равно принимался, а
// учётке с обязательным вторым фактором хватало одного пароля — и код сервиса
// исполнялся под её личностью и ролями (ТекущийПользователь, аудит, RLS).
func TestService_BasicПодчиняетсяПолитикеВхода(t *testing.T) {
	call := func(t *testing.T, prepare func(context.Context, *Server, *auth.User)) int {
		t.Helper()
		s := newSecuredServiceServer(t, &httpservice.Service{
			Name: "T", RootURL: "t", Auth: "basic",
			Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
		})
		ctx := context.Background()
		if err := s.authRepo.EnsureSchema(ctx); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		u, err := s.authRepo.Create(ctx, "admin", "S3cret-pass", "Админ", true)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if prepare != nil {
			prepare(ctx, s, u)
		}
		r := httptest.NewRequest("GET", "/hs/t/", nil)
		r.SetBasicAuth("admin", "S3cret-pass")
		w := httptest.NewRecorder()
		s.serviceDispatch(w, r)
		return w.Code
	}

	// Контроль: без политик пароль работает как прежде.
	if code := call(t, nil); code != http.StatusOK {
		t.Fatalf("без политик basic должен работать, получено %d", code)
	}

	if code := call(t, func(ctx context.Context, s *Server, _ *auth.User) {
		if err := s.authRepo.SaveAuthPolicy(ctx, auth.Policy{SSOOnly: true}); err != nil {
			t.Fatalf("SaveAuthPolicy: %v", err)
		}
	}); code != http.StatusUnauthorized {
		t.Fatalf("при sso_only пароль обязан отклоняться, получено %d", code)
	}

	if code := call(t, func(ctx context.Context, s *Server, u *auth.User) {
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			t.Fatalf("GenerateTOTPSecret: %v", err)
		}
		if err := s.authRepo.EnableTOTP(ctx, u.ID, secret, 0); err != nil {
			t.Fatalf("EnableTOTP: %v", err)
		}
	}); code != http.StatusUnauthorized {
		t.Fatalf("учётке с включённым 2FA одного пароля мало, получено %d", code)
	}

	if code := call(t, func(ctx context.Context, s *Server, _ *auth.User) {
		if err := s.authRepo.SaveAuthPolicy(ctx, auth.Policy{Require2FAAdmins: true}); err != nil {
			t.Fatalf("SaveAuthPolicy: %v", err)
		}
	}); code != http.StatusUnauthorized {
		t.Fatalf("при require_2fa_admins администратору одного пароля мало, получено %d", code)
	}
}

// Перепривязка второго фактора требует подтверждения личности. Иначе угнанная
// (или просто оставленная открытой) сессия переносила фактор на устройство
// атакующего и отзывала резервные коды владельца — самая чувствительная
// операция была защищена слабее отключения, а заодно обходила его guard.
func TestProfile2FA_ПерепривязкаТребуетПодтверждения(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "T", RootURL: "t", Auth: "none",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})
	ctx := context.Background()
	if err := s.authRepo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	u, err := s.authRepo.Create(ctx, "user", "S3cret-pass", "Пользователь", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if err := s.authRepo.EnableTOTP(ctx, u.ID, secret, 0); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}

	start := func(confirm string) *httptest.ResponseRecorder {
		body := "action=start"
		if confirm != "" {
			body += "&confirm=" + confirm
		}
		r := httptest.NewRequest("POST", "/ui/profile/2fa", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(auth.ContextWithUser(ctx, u))
		w := httptest.NewRecorder()
		s.selfTwoFactor(w, r)
		return w
	}

	// Без подтверждения новый секрет не выдаётся.
	if w := start(""); cookieByName(w, auth.EnrollCookie) != nil {
		t.Fatal("перепривязка без подтверждения выдала новый секрет")
	}
	// С верным паролем — выдаётся, как и раньше.
	if w := start("S3cret-pass"); cookieByName(w, auth.EnrollCookie) == nil {
		t.Fatalf("перепривязка с паролем не начата (код %d)", w.Code)
	}
}

func cookieByName(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}
