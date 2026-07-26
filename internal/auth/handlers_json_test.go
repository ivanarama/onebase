package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestLoginJSONSetsSessionCookieAndDoesNotReturnToken(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "ivan", "secret123", "Иван", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := &auth.Handlers{Repo: repo}

	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"login":"ivan","password":"secret123"}`))
	req.RemoteAddr = "10.0.0.1:55555"
	rec := httptest.NewRecorder()
	h.LoginJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("LoginJSON status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "onebase_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("LoginJSON did not set onebase_session cookie")
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/" {
		t.Fatalf("unexpected session cookie attrs: %+v", sessionCookie)
	}
	if _, err := repo.LookupSession(ctx, sessionCookie.Value); err != nil {
		t.Fatalf("cookie session token is not valid: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["token"]; ok {
		t.Fatalf("LoginJSON must not expose session token in JSON body: %v", body)
	}
	user, _ := body["user"].(map[string]any)
	if user["login"] != "ivan" {
		t.Fatalf("unexpected response user: %v", body["user"])
	}
}

func TestLoginJSONSetsSecureCookieForTLSOrExplicitProxyPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		force  bool
	}{
		{name: "direct TLS", target: "https://example.test/auth/login"},
		{name: "trusted HTTPS terminator", target: "http://127.0.0.1/auth/login", force: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, ctx := newTestRepo(t)
			if _, err := repo.Create(ctx, "ivan", "secret123", "Иван", false); err != nil {
				t.Fatalf("Create: %v", err)
			}
			h := &auth.Handlers{Repo: repo, SecureCookies: tc.force}
			req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(`{"login":"ivan","password":"secret123"}`))
			rec := httptest.NewRecorder()
			h.LoginJSON(rec, req)

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure {
				t.Fatalf("session cookie is not Secure: %+v", cookies)
			}
		})
	}
}

func TestStatusFailsClosedWhenUserStoreIsUnavailable(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	db.Close()
	h := &auth.Handlers{Repo: repo}
	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Status(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Status = %d, want 503", rec.Code)
	}
}
