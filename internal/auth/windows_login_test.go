package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWindowsLoginTransport(t *testing.T) {
	h := Handlers{WindowsSPN: "HTTP/base.example.org", BaseURL: "https://base.example.org"}
	for _, tc := range []struct {
		target, peer string
		allowed      bool
	}{
		{"https://base.example.org", "192.0.2.1:80", true},
		{"https://other.example.org", "192.0.2.1:80", false},
		{"http://127.0.0.1:8080", "[::1]:12345", true},
		{"http://base.example.org", "192.0.2.1:80", false},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.target+"/auth/windows", nil)
		req.RemoteAddr = tc.peer
		req.Header.Set("X-Forwarded-Proto", "https")
		if got := h.windowsLoginTransport(req); got != tc.allowed {
			t.Errorf("%s from %s: allowed %v", tc.target, tc.peer, got)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "https://base.example.org/auth/windows", nil)
	rec := httptest.NewRecorder()
	h.WindowsLogin(rec, req)
	if rec.Code != http.StatusNotFound || rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("top-level navigation must not trigger a credentials prompt")
	}
}

func TestNegotiateOnlyAcceptsVerifiedTicket(t *testing.T) {
	const boundSID = "S-1-5-21-1-2-3-1001"
	repo, db, ctx := newCacheTestRepo(t)
	admin, err := repo.Create(ctx, "admin", "password123", "", true)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.Create(ctx, "operator", "password123", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE _users SET windows_sid=? WHERE id=?", boundSID, user.ID); err != nil {
		t.Fatal(err)
	}
	role := &Role{Name: "Operator"}
	if err := repo.SyncRoles(ctx, []*Role{role}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignRole(ctx, user.ID, role.ID); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{Repo: repo, WindowsSPN: "HTTP/base.example.org", SecureCookies: true}
	do := func(header, returnPath string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/auth/windows?sid="+boundSID+"&return="+returnPath, nil)
		req.Header.Set("Authorization", header)
		req.Header.Set("X-Remote-User", admin.Login)
		req.Header.Set("X-Windows-SID", boundSID)
		rec := httptest.NewRecorder()
		h.negotiateWindows(rec, req, func(spn string, ticket []byte) (string, []byte, error) {
			if spn != h.WindowsSPN {
				t.Fatal("incorrect service principal")
			}
			switch string(ticket) {
			case "verified":
				return boundSID, []byte("reply"), nil
			case "other":
				return "S-1-5-21-1-2-3-1002", nil, nil
			default:
				return "", nil, errors.New("invalid ticket")
			}
		})
		return rec
	}
	for _, tc := range []struct {
		header string
		status int
	}{
		{"", 401},
		{"Basic dmVyaWZpZWQ=", 403},
		{"Negotiate ###", 403},
		{"Negotiate " + strings.Repeat("a", maxWindowsTicket*2), 403},
		{"Negotiate YmFk", 403},
		{"Negotiate b3RoZXI=", 403},
	} {
		rec := do(tc.header, "/ui")
		if rec.Code != tc.status || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("invalid credential: status %d, cookies %d", rec.Code, len(rec.Result().Cookies()))
		}
		if (rec.Header().Get("WWW-Authenticate") == "Negotiate") != (tc.status == 401) {
			t.Fatal("unexpected credentials prompt")
		}
	}
	success := do("Negotiate dmVyaWZpZWQ=", "/%5Cother.example.org")
	if success.Code != 200 || success.Header().Get("WWW-Authenticate") != "Negotiate cmVwbHk=" {
		t.Fatalf("valid ticket: %d", success.Code)
	}
	var result map[string]string
	if err := json.Unmarshal(success.Body.Bytes(), &result); err != nil || result["redirect"] != "/ui" {
		t.Fatalf("unsafe return: %s", success.Body)
	}
	cookies := success.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatal("missing secure session cookie")
	}
	page := httptest.NewRequest(http.MethodGet, "/ui", nil)
	page.AddCookie(cookies[0])
	rec := httptest.NewRecorder()
	repo.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := UserFromContext(r.Context())
		if principal == nil || principal.ID != user.ID || principal.IsAdmin || len(principal.Roles) != 1 || principal.Roles[0].ID != role.ID {
			t.Fatal("Windows session did not inherit the linked user's roles")
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, page)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("session cookie rejected at protected page: HTTP %d", rec.Code)
	}
	if _, err := repo.LookupSessionKind(ctx, cookies[0].Value, SessionKindConfigurator); err == nil {
		t.Fatal("received configurator session")
	}
	if err := db.SaveSetting(ctx, authPolicyKey, "null"); err != nil {
		t.Fatal(err)
	}
	if rec := do("Negotiate dmVyaWZpZWQ=", "/ui"); rec.Code != 500 || len(rec.Result().Cookies()) != 0 {
		t.Fatal("malformed policy allowed Windows login")
	}
	if _, err := db.Exec(ctx, "ALTER TABLE _settings RENAME COLUMN value TO unavailable"); err != nil {
		t.Fatal(err)
	}
	if rec := do("Negotiate dmVyaWZpZWQ=", "/ui"); rec.Code != 500 || len(rec.Result().Cookies()) != 0 {
		t.Fatal("unreadable policy allowed Windows login")
	}
	if err := repo.SetDisabled(ctx, user.ID, true); err != nil {
		t.Fatal(err)
	}
	if rec := do("Negotiate dmVyaWZpZWQ=", "/ui"); rec.Code != 403 || len(rec.Result().Cookies()) != 0 {
		t.Fatal("disabled user received a session")
	}
}

func TestWindowsChallengeChecksBindingBeforeEnrollment(t *testing.T) {
	const sid = "S-1-5-21-1-2-3-1001"
	repo, db, ctx := newCacheTestRepo(t)
	user, err := repo.Create(ctx, "operator", "password123", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE _users SET windows_sid=? WHERE id=?", sid, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAuthPolicy(ctx, Policy{Require2FAAdmins: true}); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{Repo: repo, Challenges: NewChallenges(challengeTTL)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/windows", nil)
	h.finishWindowsLogin(rec, req, sid, "/ui", "", false)
	cookies := rec.Result().Cookies()
	if rec.Header().Get("Location") != "/login/2fa" || len(cookies) != 1 || cookies[0].Name != challengeCookie {
		t.Fatal("expected only a 2FA challenge")
	}
	ch, ok := h.challenges().Get(cookies[0].Value)
	if !ok || !ch.Enroll || ch.EnrollAuthorized || ch.windowsSID != sid {
		t.Fatal("incorrect MFA enrollment policy")
	}
	if err := repo.SetWindowsAccount(ctx, user.ID, ""); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/login/2fa", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	h.TwoFactorPage(rec, req)
	if rec.Header().Get("Location") != "/login" {
		t.Fatal("removed binding left an enrollment challenge usable")
	}
	if _, err := h.createChallengeSession(req, ch); !errors.Is(err, ErrAccountUnavailable) {
		t.Fatalf("session after unbinding: %v", err)
	}
}
