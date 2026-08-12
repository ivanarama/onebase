package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestLookupSessionKindSeparatesConfiguratorAndEnterprise(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, err := repo.Create(ctx, "kind-user", "secret123", "Kind User", true)
	if err != nil {
		t.Fatal(err)
	}
	enterprise, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}
	configurator, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.LookupSessionKind(ctx, enterprise, auth.SessionKindEnterprise); err != nil {
		t.Fatalf("Enterprise session rejected for Enterprise: %v", err)
	}
	if _, err := repo.LookupSessionKind(ctx, configurator, auth.SessionKindConfigurator); err != nil {
		t.Fatalf("configurator session rejected for configurator: %v", err)
	}
	if _, err := repo.LookupSessionKind(ctx, enterprise, auth.SessionKindConfigurator); err == nil {
		t.Fatal("Enterprise session was accepted as configurator")
	}
	if _, err := repo.LookupSessionKind(ctx, configurator, auth.SessionKindEnterprise); err == nil {
		t.Fatal("configurator session was accepted as Enterprise")
	}
}

func TestMiddlewareRejectsConfiguratorSessionCookie(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, err := repo.Create(ctx, "middleware-kind", "secret123", "Kind User", true)
	if err != nil {
		t.Fatal(err)
	}
	configurator, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		t.Fatal(err)
	}
	enterprise, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}

	reached := false
	protected := repo.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	configuratorReq := httptest.NewRequest(http.MethodGet, "/ui", nil)
	configuratorReq.AddCookie(&http.Cookie{Name: "onebase_session", Value: configurator})
	configuratorRec := httptest.NewRecorder()
	protected.ServeHTTP(configuratorRec, configuratorReq)
	if reached || configuratorRec.Code != http.StatusUnauthorized {
		t.Fatalf("configurator cookie reached Enterprise handler: reached=%v status=%d", reached, configuratorRec.Code)
	}

	enterpriseReq := httptest.NewRequest(http.MethodGet, "/ui", nil)
	enterpriseReq.AddCookie(&http.Cookie{Name: "onebase_session", Value: enterprise})
	enterpriseRec := httptest.NewRecorder()
	protected.ServeHTTP(enterpriseRec, enterpriseReq)
	if !reached || enterpriseRec.Code != http.StatusNoContent {
		t.Fatalf("Enterprise cookie was rejected: reached=%v status=%d", reached, enterpriseRec.Code)
	}
}

func TestIssueOneTimeCodeRejectsEnterpriseSession(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, err := repo.Create(ctx, "otc-kind", "secret123", "Kind User", true)
	if err != nil {
		t.Fatal(err)
	}
	enterprise, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}
	h := &auth.Handlers{Repo: repo, Codes: auth.NewOneTimeCodes(30 * time.Second)}
	req := httptest.NewRequest(http.MethodPost, "/auth/one-time-code", nil)
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: enterprise})
	rec := httptest.NewRecorder()

	h.IssueOneTimeCode(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Enterprise session got configurator bootstrap code: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
