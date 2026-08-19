package ui

// Заголовки безопасности уровня сервиса (план 128) и заявка #1002: extra не
// должен обходить проверки выделенных полей.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivantit66/onebase/internal/httpservice"
)

// applyHeaders прогоняет запрос через тот же путь, что и serviceDispatch:
// заголовки ставятся ДО обработчика, поэтому смотрим прямо на recorder.
func applyHeaders(t *testing.T, cfg *httpservice.SecurityHeadersConfig, tls bool) http.Header {
	t.Helper()
	svc := &httpservice.Service{Name: "Site", RootURL: "site", Auth: "none", SecurityHeaders: cfg}
	svc.Normalize()
	r := httptest.NewRequest("GET", "/hs/site/", nil)
	if tls {
		r.Header.Set("X-Forwarded-Proto", "https")
	}
	w := httptest.NewRecorder()
	applyServiceSecurityHeaders(w, r, svc)
	return w.Header()
}

// #1002: HSTS через extra уезжал и по чистому HTTP — браузер запоминал домен
// как HTTPS-only, а выделенное поле hsts именно поэтому ставится только за TLS.
func TestServiceSecurityHeaders_ExtraCannotBypassHSTS(t *testing.T) {
	h := applyHeaders(t, &httpservice.SecurityHeadersConfig{
		Extra: map[string]string{"Strict-Transport-Security": "max-age=31536000"},
	}, false)
	if got := h.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS из extra уехал по HTTP: %q", got)
	}
}

// Выделенное поле по-прежнему работает — запрет extra не задел штатный путь.
func TestServiceSecurityHeaders_DedicatedFieldsStillWork(t *testing.T) {
	h := applyHeaders(t, &httpservice.SecurityHeadersConfig{
		HSTS: 3600, FrameOptions: "DENY", CSP: "default-src 'self'", ReferrerPolicy: "no-referrer",
	}, true)
	cases := map[string]string{
		"Strict-Transport-Security": "max-age=3600",
		"X-Frame-Options":           "DENY",
		"Content-Security-Policy":   "default-src 'self'",
		"Referrer-Policy":           "no-referrer",
		"X-Content-Type-Options":    "nosniff",
	}
	for name, want := range cases {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, ожидалось %q", name, got, want)
		}
	}
}

// #1002: значение X-Frame-Options, которое onebase check отвергает, через extra
// доезжало до браузера. Теперь не доезжает — и не затирает выделенное поле.
func TestServiceSecurityHeaders_ExtraCannotBypassFrameOptions(t *testing.T) {
	h := applyHeaders(t, &httpservice.SecurityHeadersConfig{
		FrameOptions: "DENY",
		Extra:        map[string]string{"X-Frame-Options": "ALLOWALL"},
	}, false)
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, ожидалось DENY: extra переехал выделенное поле", got)
	}
}

func TestServiceSecurityHeaders_ExtraCannotBypassCSPAndReferrer(t *testing.T) {
	h := applyHeaders(t, &httpservice.SecurityHeadersConfig{
		CSP:            "default-src 'self'",
		ReferrerPolicy: "no-referrer",
		Extra: map[string]string{
			"Content-Security-Policy": "default-src *",
			"Referrer-Policy":         "unsafe-url",
		},
	}, false)
	if got := h.Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("CSP = %q — extra переехал поле csp", got)
	}
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q — extra переехал поле referrer_policy", got)
	}
}

// Обычные заголовки extra по-прежнему проходят: запрет точечный, а не «extra
// больше не работает».
func TestServiceSecurityHeaders_ExtraStillWorksForOtherHeaders(t *testing.T) {
	h := applyHeaders(t, &httpservice.SecurityHeadersConfig{
		Extra: map[string]string{"Permissions-Policy": "geolocation=()"},
	}, false)
	if got := h.Get("Permissions-Policy"); got != "geolocation=()" {
		t.Fatalf("Permissions-Policy = %q, ожидалось geolocation=()", got)
	}
}
