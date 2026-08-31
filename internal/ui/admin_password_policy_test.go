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

func passwordPolicyServer(t *testing.T) (*Server, *auth.User, context.Context) {
	t.Helper()
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "T", RootURL: "t", Auth: "none",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})
	ctx := context.Background()
	if err := s.authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := s.authRepo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Администратор", true)
	if err != nil {
		t.Fatal(err)
	}
	return s, admin, ctx
}

func postPasswordPolicy(t *testing.T, s *Server, admin *auth.User, form string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/ui/admin/auth/password-policy", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(auth.ContextWithUser(context.Background(), admin))
	w := httptest.NewRecorder()
	s.adminAuthPasswordPolicySave(w, r)
	return w
}

// Политика паролей правится и в Предприятии, и её сохранение обязано применяться
// сразу — как и в конфигураторе.
func TestAdminPasswordPolicySaveApplies(t *testing.T) {
	s, admin, ctx := passwordPolicyServer(t)

	if w := postPasswordPolicy(t, s, admin, "password_min_length=4&allow_empty_passwords=1"); w.Code != http.StatusFound {
		t.Fatalf("сохранение политики: код=%d тело=%q", w.Code, w.Body.String())
	}
	policy := s.authRepo.EffectivePasswordPolicy(ctx)
	if policy.MinLength != 4 || !policy.AllowEmpty {
		t.Fatalf("политика не применилась: %+v", policy)
	}
	if err := s.authRepo.UpdatePassword(ctx, admin.ID, ""); err != nil {
		t.Errorf("пустой пароль отвергнут после разрешения: %v", err)
	}
}

// Обе формы правят одну запись политики. Сохранение любой из них не должно
// ронять поля другой: иначе включение 2FA молча возвращало бы минимум пароля к
// умолчанию, а смена минимума — снимала требование второго фактора.
func TestAdminPolicyFormsDoNotOverwriteEachOther(t *testing.T) {
	s, admin, ctx := passwordPolicyServer(t)

	if w := postPasswordPolicy(t, s, admin, "password_min_length=4&allow_empty_passwords=1"); w.Code != http.StatusFound {
		t.Fatalf("сохранение политики паролей: код=%d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/ui/admin/auth/policy",
		strings.NewReader("require_2fa_admins=1&allow_self_enroll_2fa=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(auth.ContextWithUser(context.Background(), admin))
	w := httptest.NewRecorder()
	s.adminAuthPolicySave(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("сохранение политик входа: код=%d тело=%q", w.Code, w.Body.String())
	}

	policy := s.authRepo.AuthPolicy(ctx)
	if !policy.Require2FAAdmins {
		t.Error("требование второго фактора не сохранено")
	}
	if policy.PasswordMinLength != 4 || !policy.AllowEmptyPasswords {
		t.Errorf("политика паролей затёрта формой политик входа: %+v", policy)
	}

	// И симметрично: правка паролей не трогает второй фактор.
	if w := postPasswordPolicy(t, s, admin, "password_min_length=6"); w.Code != http.StatusFound {
		t.Fatalf("повторное сохранение политики паролей: код=%d", w.Code)
	}
	policy = s.authRepo.AuthPolicy(ctx)
	if !policy.Require2FAAdmins || !policy.SelfEnroll2FA {
		t.Errorf("политика второго фактора затёрта формой паролей: %+v", policy)
	}
	if policy.AllowEmptyPasswords {
		t.Error("снятая галка не сняла разрешение пустых паролей")
	}
}

// Длина вне диапазона не сохраняется и возвращает форму с ошибкой, а не 500.
func TestAdminPasswordPolicyRejectsOutOfRange(t *testing.T) {
	s, admin, ctx := passwordPolicyServer(t)

	for _, form := range []string{"password_min_length=0", "password_min_length=500", "password_min_length="} {
		w := postPasswordPolicy(t, s, admin, form)
		if w.Code != http.StatusOK {
			t.Errorf("%q: ожидалась форма с ошибкой, получен код %d", form, w.Code)
		}
		if !strings.Contains(w.Body.String(), "Минимальная длина пароля должна быть числом") {
			t.Errorf("%q: в ответе нет объяснения отказа", form)
		}
	}
	if got := s.authRepo.AuthPolicy(ctx).PasswordMinLength; got != 0 {
		t.Errorf("невалидное значение сохранено: %d", got)
	}
}
