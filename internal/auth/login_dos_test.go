package auth_test

// SEC-01 / issue #776: публичный JSON-логин не должен принимать неограниченное
// тело и раздувать rate limiter гигантскими логинами.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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

// Тот же лимит обязан действовать на HTML-форме входа.
//
// #808 (issue #776) закрыл только JSON-путь: у формы предел стоял лишь на тело
// (64 КиБ), поэтому пара «логин 60 КиБ / пароль 60 КиБ» проходила разбор формы
// и доезжала до ключа rate-limiter'а и до Authenticate — тот же вектор на
// соседнем публичном маршруте, который не требует аутентификации (#864).
//
// Проверяются ОБА входа одним набором данных: раздельные тесты и позволили
// лимитам разъехаться.
func TestLogin_ОверлонгОтвергаетсяНаОбоихВходах(t *testing.T) {
	long := strings.Repeat("x", 60*1024)

	cases := []struct {
		name  string
		call  func(h *auth.Handlers, rec *httptest.ResponseRecorder)
		codes []int
	}{
		{
			name: "HTML-форма, длинный пароль",
			call: func(h *auth.Handlers, rec *httptest.ResponseRecorder) {
				form := url.Values{"login": {"ivan"}, "password": {long}}
				req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.RemoteAddr = "10.0.0.11:1234"
				h.LoginSubmit(rec, req)
			},
			codes: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge},
		},
		{
			name: "HTML-форма, длинный логин",
			call: func(h *auth.Handlers, rec *httptest.ResponseRecorder) {
				form := url.Values{"login": {long}, "password": {"secret123"}}
				req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.RemoteAddr = "10.0.0.12:1234"
				h.LoginSubmit(rec, req)
			},
			codes: []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge},
		},
		{
			name: "JSON, длинный пароль",
			call: func(h *auth.Handlers, rec *httptest.ResponseRecorder) {
				body := `{"login":"ivan","password":"` + strings.Repeat("x", 2000) + `"}`
				req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
				req.RemoteAddr = "10.0.0.13:1234"
				h.LoginJSON(rec, req)
			},
			codes: []int{http.StatusBadRequest},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, ctx := newTestRepo(t)
			if _, err := repo.Create(ctx, "ivan", "secret123", "Иван", false); err != nil {
				t.Fatalf("Create: %v", err)
			}
			h := &auth.Handlers{Repo: repo, LoginLimit: auth.NewLoginLimiter(3, time.Minute)}

			rec := httptest.NewRecorder()
			c.call(h, rec)

			ok := false
			for _, code := range c.codes {
				if rec.Code == code {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("получен %d, ожидался один из %v — значение доехало до аутентификации", rec.Code, c.codes)
			}
			// Ответ не должен создавать сессию: 200 с куки означал бы, что
			// отсечка стоит после Authenticate, а не до.
			if cookie := rec.Header().Get("Set-Cookie"); cookie != "" {
				t.Errorf("выдана кука на отклонённом входе: %q", cookie)
			}
		})
	}
}

// Значение в пределах лимита по-прежнему проходит: отсечка не должна ломать
// обычный вход длинным, но разумным паролем.
func TestLoginSubmit_ДлинныйНоДопустимыйПарольПроходит(t *testing.T) {
	repo, ctx := newTestRepo(t)
	password := strings.Repeat("p", 64) // предел bcrypt — 72 байта
	if _, err := repo.Create(ctx, "ivan", password, "Иван", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := &auth.Handlers{Repo: repo, LoginLimit: auth.NewLoginLimiter(3, time.Minute)}

	form := url.Values{"login": {"ivan"}, "password": {password}}
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.0.0.14:1234"
	rec := httptest.NewRecorder()
	h.LoginSubmit(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("допустимый пароль отвергнут: %d %s", rec.Code, rec.Body.String())
	}
}
