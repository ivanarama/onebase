package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

// Сорвавшаяся привязка не должна оставлять пользователя без всего сразу.
//
// Код привязки гасился в САМОМ НАЧАЛЕ второго шага — до того, как второй фактор
// действительно привязан. Любой сбой на шаге сканирования QR (истёкшие десять
// минут challenge'а, пять опечаток в коде TOTP, закрытая вкладка) оставлял
// учётку и без второго фактора, и без кода привязки: за новым надо было идти к
// администратору, а если это последний админ — идти было не к кому (#615).
//
// Проверяем через публичный путь входа: неверный код на шаге настройки, затем
// новая попытка входа тем же кодом привязки.
func TestBindTicket_ПереживаетСорвавшуюсяПривязку(t *testing.T) {
	h, repo, adminID, ctx := requireAdminWith2FA(t, auth.Policy{})
	ticket, err := repo.IssueBindTicket(ctx, adminID)
	if err != nil {
		t.Fatalf("IssueBindTicket: %v", err)
	}

	first := postLogin(t, h, "admin", "S3cret-pass")
	challenge := cookieNamed(first, "onebase_2fa")
	if challenge == nil {
		t.Fatal("не выдан challenge")
	}
	enroll := postTwoFactor(t, h, challenge, ticket)
	if !strings.Contains(enroll.Body.String(), "Отсканируйте QR") {
		t.Fatalf("код привязки не открыл настройку: %s", firstLine(enroll.Body.String()))
	}

	// Привязка срывается: код из приложения введён неверно.
	postTwoFactor(t, h, challenge, "000000")
	if enabled, _ := repo.TOTPEnabled(ctx, adminID); enabled {
		t.Fatal("второй фактор включился по неверному коду — проба некорректна")
	}

	// Пользователь начинает заново тем же кодом привязки.
	second := postLogin(t, h, "admin", "S3cret-pass")
	challenge2 := cookieNamed(second, "onebase_2fa")
	if challenge2 == nil {
		t.Fatal("не выдан challenge на повторном входе")
	}
	retry := postTwoFactor(t, h, challenge2, ticket)
	if !strings.Contains(retry.Body.String(), "Отсканируйте QR") {
		t.Fatalf("код привязки сгорел на сорвавшейся попытке — пользователь заперт: %s",
			firstLine(retry.Body.String()))
	}

	// И доводит настройку до конца.
	secret := secretFromEnrollPage(t, retry.Body.String())
	code, err := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	done := postTwoFactor(t, h, challenge2, code)
	if cookieNamed(done, "onebase_session") == nil {
		t.Fatalf("сессия не выдана после повторной настройки (код %d)", done.Code)
	}

	// А вот теперь код привязки обязан быть погашен: привязка состоялась.
	if err := repo.ConsumeBindTicket(ctx, adminID, ticket, time.Now()); err == nil {
		t.Fatal("код привязки не погашен после успешной настройки")
	}
}
