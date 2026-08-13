package auth

import (
	"net/http/httptest"
	"testing"
)

// SEC-05 / issue #780: redirect_uri OIDC берётся из заданного публичного адреса
// (ONEBASE_PUBLIC_URL), а не из подделываемых X-Forwarded-* заголовков.
func TestRedirectURI_PrefersPublicURL(t *testing.T) {
	h := &Handlers{BaseURL: "https://erp.example.com/"}
	r := httptest.NewRequest("GET", "/auth/oidc/google/start", nil)
	r.Host = "attacker.example"
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	r.Header.Set("X-Forwarded-Proto", "http")

	got := h.redirectURI(r, "google")
	want := "https://erp.example.com/auth/oidc/google/callback"
	if got != want {
		t.Fatalf("redirectURI = %q, ожидался %q — public URL должен побеждать forwarded-заголовки", got, want)
	}
}

// Без public URL callback строится из запроса — совместимость сохранена.
func TestRedirectURI_FallsBackToRequest(t *testing.T) {
	h := &Handlers{}
	r := httptest.NewRequest("GET", "/auth/oidc/google/start", nil)
	r.Host = "erp.local:8080"

	got := h.redirectURI(r, "google")
	want := "http://erp.local:8080/auth/oidc/google/callback"
	if got != want {
		t.Fatalf("redirectURI = %q, ожидался %q", got, want)
	}
}
