package auth_test

// Принудительная привязка второго фактора на входе (issue #577): по одному
// паролю привязать 2FA к учётке нельзя — нужен одноразовый код привязки от
// администратора. Явная политика SelfEnroll2FA возвращает прежнюю самопривязку.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

// secretFromEnrollPage вытаскивает секрет TOTP со страницы настройки — он
// генерируется на сервере и клиенту иначе неизвестен.
func secretFromEnrollPage(t *testing.T, body string) string {
	t.Helper()
	const open = `<div class="secret">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("на странице настройки нет секрета:\n%s", firstLine(body))
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		t.Fatal("незакрытый блок секрета на странице настройки")
	}
	return auth.NormalizeTOTPSecret(rest[:j])
}

// requireAdminWith2FA создаёт админа и включает политику «второй фактор
// обязателен администраторам». По умолчанию (без SelfEnroll2FA) самопривязка на
// входе выключена.
func requireAdminWith2FA(t *testing.T, policy auth.Policy) (*auth.Handlers, *auth.Repo, string, context.Context) {
	t.Helper()
	repo, ctx := newTestRepo(t)
	admin, err := repo.Create(ctx, "admin", "S3cret-pass", "Админ", true)
	if err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	policy.Require2FAAdmins = true
	if err := repo.SaveAuthPolicy(ctx, policy); err != nil {
		t.Fatalf("SaveAuthPolicy: %v", err)
	}
	return &auth.Handlers{Repo: repo}, repo, admin.ID, ctx
}

// Сторона «одного пароля недостаточно»: вход админа без 2FA под обязательной
// политикой и выключенной самопривязкой не показывает ни QR, ни секрет — только
// запрос кода привязки, и второй фактор не включается.
func TestForcedEnrollGatedWithoutTicket(t *testing.T) {
	h, repo, adminID, ctx := requireAdminWith2FA(t, auth.Policy{})

	rec := postLogin(t, h, "admin", "S3cret-pass")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("сессия выдана по одному паролю")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Код привязки") {
		t.Fatalf("ожидался запрос кода привязки, получено: %s", firstLine(body))
	}
	if strings.Contains(body, "Отсканируйте QR") || strings.Contains(body, `class="secret"`) {
		t.Fatal("QR/секрет показаны по одному паролю — привязка не защищена")
	}

	challenge := cookieNamed(rec, "onebase_2fa")
	if challenge == nil {
		t.Fatal("не выдан challenge")
	}
	// Секрет не отдаётся и через endpoint QR: в challenge его нет.
	qrReq := httptest.NewRequest(http.MethodGet, "/login/2fa/qr", nil)
	qrReq.AddCookie(challenge)
	qrRec := httptest.NewRecorder()
	h.TwoFactorQR(qrRec, qrReq)
	if qrRec.Code != http.StatusNotFound {
		t.Fatalf("QR отдан без кода привязки (код %d)", qrRec.Code)
	}
	// Произвольный код привязки настройку не открывает и 2FA не включает.
	bad := postTwoFactor(t, h, challenge, "abcd-efgh-ijkl-mnop")
	if strings.Contains(bad.Body.String(), "Отсканируйте QR") {
		t.Fatal("неверный код привязки открыл настройку")
	}
	if enabled, _ := repo.TOTPEnabled(ctx, adminID); enabled {
		t.Fatal("второй фактор включён без кода привязки")
	}
}

// Сторона «сценарий проходим»: с кодом привязки от администратора учётка без
// 2FA проходит настройку до конца и получает сессию.
func TestForcedEnrollCompletableWithTicket(t *testing.T) {
	h, repo, adminID, ctx := requireAdminWith2FA(t, auth.Policy{})
	ticket, err := repo.IssueBindTicket(ctx, adminID)
	if err != nil {
		t.Fatalf("IssueBindTicket: %v", err)
	}

	rec := postLogin(t, h, "admin", "S3cret-pass")
	challenge := cookieNamed(rec, "onebase_2fa")
	if challenge == nil {
		t.Fatal("не выдан challenge")
	}
	if !strings.Contains(rec.Body.String(), "Код привязки") {
		t.Fatalf("ожидался запрос кода привязки: %s", firstLine(rec.Body.String()))
	}

	// Верный код привязки открывает настройку (QR + секрет).
	enroll := postTwoFactor(t, h, challenge, ticket)
	ebody := enroll.Body.String()
	if !strings.Contains(ebody, "Отсканируйте QR") {
		t.Fatalf("код привязки не открыл настройку: %s", firstLine(ebody))
	}
	secret := secretFromEnrollPage(t, ebody)

	// Настройка завершается кодом из приложения — выдаётся сессия.
	code, err := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	done := postTwoFactor(t, h, challenge, code)
	if cookieNamed(done, "onebase_session") == nil {
		t.Fatalf("сессия не выдана после настройки (код %d)", done.Code)
	}
	if enabled, _ := repo.TOTPEnabled(ctx, adminID); !enabled {
		t.Fatal("второй фактор не включился после настройки по коду привязки")
	}
	// Тикет одноразовый: повторно не подойдёт.
	if err := repo.ConsumeBindTicket(ctx, adminID, ticket, time.Now()); err == nil {
		t.Fatal("код привязки сработал повторно")
	}
}

// Явная политика SelfEnroll2FA возвращает прежнюю самопривязку: настройка
// открывается сразу после пароля, код привязки не запрашивается.
func TestForcedEnrollDirectWhenSelfEnrollAllowed(t *testing.T) {
	h, _, _, _ := requireAdminWith2FA(t, auth.Policy{SelfEnroll2FA: true})
	rec := postLogin(t, h, "admin", "S3cret-pass")
	body := rec.Body.String()
	if !strings.Contains(body, "Отсканируйте QR") {
		t.Fatalf("при разрешённой самопривязке ожидалась настройка сразу: %s", firstLine(body))
	}
	if strings.Contains(body, "Код привязки") {
		t.Fatal("запрошен код привязки вопреки разрешённой самопривязке")
	}
}

// Код привязки: одноразовый, привязан к учётке, истекает по TTL.
func TestBindTicketOneTimeUserScopedAndExpiry(t *testing.T) {
	repo, ctx := newTestRepo(t)
	a, err := repo.Create(ctx, "a", "S3cret-pass", "A", false)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	b, err := repo.Create(ctx, "b", "S3cret-pass", "B", false)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	now := time.Now()
	code, err := repo.IssueBindTicket(ctx, a.ID)
	if err != nil {
		t.Fatalf("IssueBindTicket: %v", err)
	}
	if err := repo.ConsumeBindTicket(ctx, b.ID, code, now); err == nil {
		t.Fatal("код привязки одной учётки сработал на другой")
	}
	if err := repo.ConsumeBindTicket(ctx, a.ID, "zzzz-zzzz-zzzz-zzzz", now); err == nil {
		t.Fatal("принят неверный код привязки")
	}
	if err := repo.ConsumeBindTicket(ctx, a.ID, code, now); err != nil {
		t.Fatalf("верный код привязки отклонён: %v", err)
	}
	if err := repo.ConsumeBindTicket(ctx, a.ID, code, now); err == nil {
		t.Fatal("код привязки сработал повторно")
	}
	// Свежий код после истечения TTL не подходит.
	code2, err := repo.IssueBindTicket(ctx, a.ID)
	if err != nil {
		t.Fatalf("IssueBindTicket #2: %v", err)
	}
	if err := repo.ConsumeBindTicket(ctx, a.ID, code2, now.Add(auth.BindTicketTTL+time.Minute)); err == nil {
		t.Fatal("принят просроченный код привязки")
	}
}
