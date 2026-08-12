package auth_test

// Тесты одноразовых bootstrap-кодов (план 53, этап 1): сессионный токен больше
// не передаётся в URL — конфигуратор получает короткоживущий single-use код и
// обменивает его на cookie через /auth/bootstrap.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

func TestOneTimeCodes_IssueExchangeSingleUse(t *testing.T) {
	codes := auth.NewOneTimeCodes(30 * time.Second)

	code, err := codes.Issue("tok-123")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if code == "" {
		t.Fatal("Issue вернул пустой код")
	}

	got, ok := codes.Exchange(code)
	if !ok {
		t.Fatal("Exchange не принял свежий код")
	}
	if got != "tok-123" {
		t.Fatalf("Exchange вернул %q, ожидался tok-123", got)
	}

	if _, ok := codes.Exchange(code); ok {
		t.Fatal("код сработал повторно — должен быть одноразовым")
	}
}

func TestOneTimeCodes_Expired(t *testing.T) {
	codes := auth.NewOneTimeCodes(-time.Second) // всё уже протухло
	code, err := codes.Issue("tok")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, ok := codes.Exchange(code); ok {
		t.Fatal("протухший код принят")
	}
}

func TestOneTimeCodes_Unknown(t *testing.T) {
	codes := auth.NewOneTimeCodes(time.Second)
	if _, ok := codes.Exchange("no-such-code"); ok {
		t.Fatal("неизвестный код принят")
	}
}

// newSessionFixture: база + пользователь + живая сессия.
func newSessionFixture(t *testing.T) (*auth.Handlers, string) {
	t.Helper()
	repo, ctx := newTestRepo(t)
	user, err := repo.Create(ctx, "ivan", "secret123", "Иван", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	h := &auth.Handlers{Repo: repo, Codes: auth.NewOneTimeCodes(30 * time.Second)}
	return h, token
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "onebase_session" {
			return c
		}
	}
	return nil
}

func TestBootstrap_ExchangesCodeForCookie(t *testing.T) {
	h, token := newSessionFixture(t)
	code, err := h.Codes.Issue(token)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	req := httptest.NewRequest("GET", "/auth/bootstrap?code="+code+"&return=/ui", nil)
	rec := httptest.NewRecorder()
	h.Bootstrap(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("ожидался 302, получен %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui" {
		t.Fatalf("redirect на %q, ожидался /ui", loc)
	}
	c := sessionCookie(resp)
	if c == nil || c.Value == "" || c.Value == token {
		t.Fatalf("bootstrap did not issue a distinct Enterprise cookie: %+v", c)
	}
	if _, err := h.Repo.LookupSessionKind(req.Context(), c.Value, auth.SessionKindEnterprise); err != nil {
		t.Fatalf("bootstrap cookie is not an Enterprise session: %v", err)
	}
	if _, err := h.Repo.LookupSessionKind(req.Context(), token, auth.SessionKindConfigurator); err != nil {
		t.Fatalf("bootstrap invalidated the configurator session: %v", err)
	}

	// повторное использование того же кода — отказ (redirect на /login, без cookie)
	rec2 := httptest.NewRecorder()
	h.Bootstrap(rec2, httptest.NewRequest("GET", "/auth/bootstrap?code="+code+"&return=/ui", nil))
	resp2 := rec2.Result()
	if c2 := sessionCookie(resp2); c2 != nil {
		t.Fatal("повторный обмен кода установил cookie")
	}
	if loc := resp2.Header.Get("Location"); loc != "/login" {
		t.Fatalf("повторный обмен: redirect на %q, ожидался /login", loc)
	}
}

// Сырой токен в query больше не принимается — только одноразовый код.
func TestBootstrap_RawTokenParamIgnored(t *testing.T) {
	h, token := newSessionFixture(t)

	req := httptest.NewRequest("GET", "/auth/bootstrap?token="+token+"&return=/ui", nil)
	rec := httptest.NewRecorder()
	h.Bootstrap(rec, req)

	if c := sessionCookie(rec.Result()); c != nil {
		t.Fatal("bootstrap по сырому токену установил cookie — токен в URL запрещён (план 53)")
	}
}

func TestIssueOneTimeCode_RequiresSession(t *testing.T) {
	h, token := newSessionFixture(t)

	// без cookie → 401
	rec := httptest.NewRecorder()
	h.IssueOneTimeCode(rec, httptest.NewRequest("POST", "/auth/one-time-code", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("без сессии ожидался 401, получен %d", rec.Code)
	}

	// с валидной cookie → 200 + код, который обменивается на тот же токен
	req := httptest.NewRequest("POST", "/auth/one-time-code", nil)
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: token})
	rec2 := httptest.NewRecorder()
	h.IssueOneTimeCode(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d: %s", rec2.Code, rec2.Body.String())
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, ok := h.Codes.Exchange(out.Code)
	if !ok || got != token {
		t.Fatalf("выданный код не обменивается на токен сессии")
	}

	// с протухшей/неверной cookie → 401
	req3 := httptest.NewRequest("POST", "/auth/one-time-code", nil)
	req3.AddCookie(&http.Cookie{Name: "onebase_session", Value: "bogus"})
	rec3 := httptest.NewRecorder()
	h.IssueOneTimeCode(rec3, req3)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("с неверной сессией ожидался 401, получен %d", rec3.Code)
	}
}

// Middleware больше не принимает токен из query (?_tk=) — только cookie.
func TestMiddleware_TkQueryParamRejected(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, err := repo.Create(ctx, "ivan", "secret123", "Иван", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	srv := repo.Middleware(next)

	req := httptest.NewRequest("GET", "/ui?_tk="+token, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if reached {
		t.Fatal("запрос с ?_tk= прошёл авторизацию — токен в URL запрещён (план 53)")
	}
	if rec.Code != http.StatusUnauthorized && !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Fatalf("ожидался отказ (401 или redirect /login), получен %d", rec.Code)
	}
}
