package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

// Связывание SSO-личности с УЖЕ СУЩЕСТВУЮЩЕЙ локальной учёткой требует явного
// email_verified == true. Раньше защита срабатывала только когда claim есть и
// равен false, поэтому провайдер, который его вовсе не присылает (так делает
// Microsoft Entra ID), связывал кого угодно с локальным администратором —
// в том числе при выключенном автосоздании, то есть в самой строгой настройке.
func TestSSO_СвязываниеТребуетПодтверждённойПочты(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims map[string]any
		mutate func(*auth.OIDCProvider)
	}{
		{
			name:   "claim email_verified не передан",
			claims: map[string]any{"sub": "attacker-1", "email": "admin@example.com"},
		},
		{
			name:   "claim email_verified равен false",
			claims: map[string]any{"sub": "attacker-2", "email": "admin@example.com", "email_verified": false},
		},
		{
			name:   "логин не из почты — подтвердить его провайдер не может",
			claims: map[string]any{"sub": "attacker-3", "preferred_username": "admin@example.com"},
			mutate: func(p *auth.OIDCProvider) { p.LoginClaim = "preferred_username" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := newMockIssuer(t, tc.claims)
			repo, h := newSSORepo(t, issuer.srv.URL, func(p *auth.OIDCProvider) {
				p.AutoCreate = false
				if tc.mutate != nil {
					tc.mutate(p)
				}
			})
			// Локальная учётка с тем же логином ОБЯЗАНА существовать: иначе
			// вход отклонялся бы просто потому, что связывать не с чем, и
			// проба ничего бы не проверяла.
			if _, err := repo.Create(t.Context(), "admin@example.com", "S3cret-pass", "Админ", true); err != nil {
				t.Fatalf("Create: %v", err)
			}
			rec := runSSOLogin(t, h, issuer, "mock")
			if cookieNamed(rec, "onebase_session") != nil {
				t.Fatal("SSO связался с существующей учёткой без подтверждения владения адресом")
			}
		})
	}

	// Контроль: подтверждённая почта связывается как прежде.
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-ok", "email": "admin@example.com", "email_verified": true,
	})
	repo, h := newSSORepo(t, issuer.srv.URL, func(p *auth.OIDCProvider) { p.AutoCreate = false })
	ctx := t.Context()
	if _, err := repo.Create(ctx, "admin@example.com", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec := runSSOLogin(t, h, issuer, "mock"); cookieNamed(rec, "onebase_session") == nil {
		t.Fatalf("подтверждённая почта не связалась (код %d)", rec.Code)
	}
}

// Резервный код должен переживать оффлайн-перебор: хэш несолёный sha256, всю
// стойкость даёт длина. Прежних 8 символов (≈40 бит) не хватало — читатель
// таблицы восстанавливал полноценный второй фактор у себя.
func TestРезервныйКодДостаточноДлинный(t *testing.T) {
	repo, ctx := newTestRepo(t)
	u, err := repo.Create(ctx, "user", "S3cret-pass", "Пользователь", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if err := repo.EnableTOTP(ctx, u.ID, secret, 0); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}
	codes, err := repo.ReplaceBackupCodes(ctx, u.ID)
	if err != nil {
		t.Fatalf("ReplaceBackupCodes: %v", err)
	}
	if len(codes) == 0 {
		t.Fatal("коды не выпущены")
	}
	for _, code := range codes {
		clean := strings.ReplaceAll(code, "-", "")
		if len(clean) < 16 {
			t.Fatalf("резервный код %q короче 16 значащих символов (%d)", code, len(clean))
		}
	}
	// Выпущенный код по-прежнему принимается и гасится ровно один раз.
	if err := repo.VerifySecondFactor(ctx, u.ID, codes[0], time.Now()); err != nil {
		t.Fatalf("свежий резервный код отклонён: %v", err)
	}
	if err := repo.VerifySecondFactor(ctx, u.ID, codes[0], time.Now()); err == nil {
		t.Fatal("резервный код принят второй раз")
	}
}

// Код с разделителем — так его показывает часть аутентификаторов — обязан
// приниматься: пользователь переписывает то, что видит.
func TestКодСРазделителемПринимается(t *testing.T) {
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := time.Now()
	code, err := auth.TOTPCode(secret, auth.TOTPStep(now))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	for _, spaced := range []string{code[:3] + " " + code[3:], code[:3] + "-" + code[3:], " " + code + " "} {
		if _, ok := auth.VerifyTOTP(secret, spaced, now, 0); !ok {
			t.Fatalf("код %q отвергнут", spaced)
		}
	}
	if _, ok := auth.VerifyTOTP(secret, "000", now, 0); ok {
		t.Fatal("короткий код принят")
	}
}
