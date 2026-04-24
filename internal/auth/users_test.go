package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Unit tests — no database required

func TestUserFromContext_Empty(t *testing.T) {
	ctx := context.Background()
	if UserFromContext(ctx) != nil {
		t.Fatal("should return nil for empty context")
	}
}

func TestUserFromContext_WithUser(t *testing.T) {
	u := &User{ID: "abc", Login: "admin", IsAdmin: true}
	ctx := context.WithValue(context.Background(), userKey, u)
	got := UserFromContext(ctx)
	if got == nil {
		t.Fatal("should return user")
	}
	if got.ID != "abc" || got.Login != "admin" || !got.IsAdmin {
		t.Fatalf("unexpected user: %+v", got)
	}
}

func TestIsLocalURL(t *testing.T) {
	cases := []struct {
		url   string
		local bool
	}{
		{"/ui", true},
		{"/login?return=/ui", true},
		{"http://evil.com", false},
		{"//evil.com", false},
		{"", false},
	}
	for _, c := range cases {
		got := isLocalURL(c.url)
		if got != c.local {
			t.Errorf("isLocalURL(%q) = %v, want %v", c.url, got, c.local)
		}
	}
}

func TestRedirectToLogin_HTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/goods", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rr := httptest.NewRecorder()
	redirectToLogin(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header")
	}
}

func TestRedirectToLogin_JSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	redirectToLogin(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
