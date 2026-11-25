package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestProfile2FAEnrollmentCookieSecurePolicy(t *testing.T) {
	const httpURL = "http://onebase.example/ui/profile/2fa"
	const httpsURL = "https://onebase.example/ui/profile/2fa"

	for _, tc := range []struct {
		name           string
		requestURL     string
		secureCookies  string
		publicURL      string
		forwardedProto string
		wantSecure     bool
	}{
		{name: "local HTTP", requestURL: httpURL},
		{name: "direct TLS", requestURL: httpsURL, wantSecure: true},
		{name: "configured proxy flag", requestURL: httpURL, secureCookies: "true", wantSecure: true},
		{name: "configured HTTPS public URL", requestURL: httpURL, publicURL: "https://onebase.example", wantSecure: true},
		{name: "forwarded header alone", requestURL: httpURL, forwardedProto: "https"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ONEBASE_DEBUG_TOKEN", "")
			t.Setenv("ONEBASE_SECURE_COOKIES", tc.secureCookies)
			t.Setenv("ONEBASE_PUBLIC_URL", tc.publicURL)

			ctx := t.Context()
			db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "profile.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			repo := auth.NewRepo(db)
			if err := repo.EnsureSchema(ctx); err != nil {
				t.Fatal(err)
			}
			user, err := repo.Create(ctx, "profile-test", "temporary-profile-test-password", "", false)
			if err != nil {
				t.Fatal(err)
			}
			session, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
			if err != nil {
				t.Fatal(err)
			}
			application, err := Build(ctx, Config{
				Store: db, Registry: runtime.NewRegistry(), Interpreter: interpreter.New(),
				AuthRepo: repo, Host: "127.0.0.1",
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := application.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			handler := application.Server().Handler()

			submit := func(form url.Values, enrollment *http.Cookie) *http.Cookie {
				t.Helper()
				r := httptest.NewRequestWithContext(ctx, http.MethodPost, tc.requestURL, strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if tc.forwardedProto != "" {
					r.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
				}
				r.AddCookie(&http.Cookie{Name: "onebase_session", Value: session})
				if enrollment != nil {
					r.AddCookie(enrollment)
				}

				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				action := form.Get("action")
				if w.Code != http.StatusOK {
					t.Fatalf("%s returned status %d", action, w.Code)
				}
				for _, cookie := range w.Result().Cookies() {
					if cookie.Name != auth.EnrollCookie {
						continue
					}
					if cookie.Secure != tc.wantSecure {
						t.Errorf("%s enrollment cookie Secure = %v, want %v", action, cookie.Secure, tc.wantSecure)
					}
					if cookie.Path != "/ui/profile" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
						t.Errorf("%s enrollment cookie has unexpected path or attributes", action)
					}
					return cookie
				}
				t.Fatalf("%s did not set the enrollment cookie", action)
				return nil
			}

			issued := submit(url.Values{"action": {"start"}}, nil)
			if issued.Value == "" || issued.MaxAge != 0 {
				t.Fatal("start did not issue a session enrollment cookie")
			}
			t.Cleanup(func() { auth.FinishEnrollment(issued.Value) })
			challenge, ok := auth.Enrollment(issued.Value)
			if !ok {
				t.Fatal("enrollment challenge is missing")
			}

			code, err := auth.TOTPCode(challenge.Secret, auth.TOTPStep(time.Now()))
			if err != nil {
				t.Fatal(err)
			}
			cleared := submit(url.Values{"action": {"confirm"}, "code": {code}}, issued)
			if cleared.Value != "" || cleared.MaxAge >= 0 {
				t.Fatal("confirm did not expire the enrollment cookie")
			}
			enabled, err := repo.TOTPEnabled(ctx, user.ID)
			if err != nil || !enabled {
				t.Fatalf("TOTP was not enabled: %v", err)
			}
		})
	}
}
