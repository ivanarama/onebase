package auth_test

// SEC-01 / issue #776: публичный JSON-логин не должен принимать неограниченное
// тело и раздувать rate limiter гигантскими логинами.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestLoginJSON_RejectsOversizedBody(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "ivan", "secret123", "Иван", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := &auth.Handlers{Repo: repo, LoginLimit: auth.NewLoginLimiter(3, time.Minute)}

	// Тело заведомо больше предела (64 КиБ): MaxBytesReader обрывает чтение и
	// Decode падает — хендлер обязан ответить 400/413, а не пытаться прочитать
	// всё в память.
	body := `{"login":"ivan","password":"` + strings.Repeat("x", 70*1024) + `"}`
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.9:1234"
	rec := httptest.NewRecorder()
	h.LoginJSON(rec, req)

	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("оверсайз-тело: ожидался 400/413, получен %d", rec.Code)
	}
}

func TestLoginJSON_RejectsOverlongCredentials(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "ivan", "secret123", "Иван", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := &auth.Handlers{Repo: repo, LoginLimit: auth.NewLoginLimiter(3, time.Minute)}

	// Пароль в пределах тела, но патологически длинный — должен быть отклонён до
	// хеширования и до лимитера.
	body := `{"login":"ivan","password":"` + strings.Repeat("x", 2000) + `"}`
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.10:1234"
	rec := httptest.NewRecorder()
	h.LoginJSON(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("слишком длинный пароль: ожидался 400, получен %d", rec.Code)
	}
}

func TestLoginKey_TruncatesLongLogin(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"

	// Логины, различающиеся только за пределом усечения, обязаны дать один ключ
	// — иначе перебором уникальных «хвостов» можно раздувать map лимитера.
	base := strings.Repeat("x", 256)
	if k1, k2 := auth.LoginKey(r, base+"AAAA"), auth.LoginKey(r, base+"BBBB"); k1 != k2 {
		t.Fatalf("длинные логины не усечены до общего ключа:\n%q\n%q", k1, k2)
	}
	// Ключ не растёт с длиной логина.
	if huge := auth.LoginKey(r, strings.Repeat("y", 100_000)); len(huge) > len("1.2.3.4|")+300 {
		t.Fatalf("ключ лимитера не ограничен по длине: %d", len(huge))
	}
}
