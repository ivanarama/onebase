package auth_test

// Единый вход против мок-провайдера OIDC (план 84): полный цикл
// start → провайдер → callback, маппинг ролей и отказы на битом id_token.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

// mockIssuer — минимальный провайдер OIDC: discovery, JWKS и token endpoint.
type mockIssuer struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	claims map[string]any
	// tamper портит подпись выданного id_token.
	tamper bool
	// lastForm — тело последнего запроса к token endpoint (проверка PKCE).
	lastForm url.Values
}

func newMockIssuer(t *testing.T, claims map[string]any) *mockIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	m := &mockIssuer{key: key, claims: claims}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 m.srv.URL,
			"authorization_endpoint": m.srv.URL + "/authorize",
			"token_endpoint":         m.srv.URL + "/token",
			"userinfo_endpoint":      m.srv.URL + "/userinfo",
			"jwks_uri":               m.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := m.key.Public().(*rsa.PublicKey)
		writeJSON(w, map[string]any{"keys": []any{map[string]any{
			"kty": "RSA",
			"kid": "test-key",
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		m.lastForm = r.Form
		writeJSON(w, map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"id_token":     m.idToken(t, m.claims),
		})
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// idToken подписывает JWT с claim'ами провайдера.
func (m *mockIssuer) idToken(t *testing.T, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": m.srv.URL,
		"aud": "onebase",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}
	segment := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signingInput := segment(header) + "." + segment(claims)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15: %v", err)
	}
	if m.tamper {
		sig[0] ^= 0xff
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// runSSOLogin проходит start → callback и возвращает ответ callback'а. Роль
// провайдера играет мок: nonce из редиректа он кладёт в выдаваемый id_token —
// именно так поступает настоящий issuer.
func runSSOLogin(t *testing.T, h *auth.Handlers, m *mockIssuer, providerID string) *httptest.ResponseRecorder {
	t.Helper()
	startReq := httptest.NewRequest(http.MethodGet, "/auth/oidc/"+providerID+"/start", nil)
	startRec := httptest.NewRecorder()
	h.OIDCStart(startRec, startReq)
	if startRec.Code != http.StatusFound {
		t.Fatalf("start вернул %d, ожидался редирект к провайдеру: %s", startRec.Code, startRec.Body.String())
	}
	location, err := url.Parse(startRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("разбор Location: %v", err)
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("в редиректе нет state")
	}
	if location.Query().Get("code_challenge") == "" || location.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("в редиректе нет PKCE-вызова")
	}
	stateCookie := cookieNamed(startRec, "onebase_oidc")
	if stateCookie == nil {
		t.Fatal("не выдана кука state")
	}
	nonce := location.Query().Get("nonce")
	if nonce == "" {
		t.Fatal("в редиректе нет nonce")
	}
	m.claims["nonce"] = nonce

	cbReq := httptest.NewRequest(http.MethodGet, "/auth/oidc/"+providerID+"/callback?code=abc&state="+url.QueryEscape(state), nil)
	cbReq.AddCookie(stateCookie)
	cbRec := httptest.NewRecorder()
	h.OIDCCallback(cbRec, cbReq)
	return cbRec
}

// newSSORepo — база с провайдером и одной ролью для маппинга.
func newSSORepo(t *testing.T, issuer string, mutate func(*auth.OIDCProvider)) (*auth.Repo, *auth.Handlers) {
	t.Helper()
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "admin", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	roles := []*auth.Role{{Name: "Бухгалтерия"}}
	if err := repo.SyncRoles(ctx, roles); err != nil {
		t.Fatalf("SyncRoles: %v", err)
	}
	p := &auth.OIDCProvider{
		ID:         "mock",
		Name:       "Мок",
		Issuer:     issuer,
		ClientID:   "onebase",
		Enabled:    true,
		AutoCreate: true,
		TrustMFA:   true,
		RoleMappings: []auth.OIDCRoleMapping{
			{Claim: "groups", Value: "erp-buh", Role: "Бухгалтерия"},
		},
	}
	if mutate != nil {
		mutate(p)
	}
	if err := repo.SaveAuthProviders(ctx, []*auth.OIDCProvider{p}); err != nil {
		t.Fatalf("SaveAuthProviders: %v", err)
	}
	return repo, &auth.Handlers{Repo: repo, OIDC: auth.NewOIDCClient()}
}

func TestOIDCCallbackCreatesUserAndAppliesRoleMapping(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub":            "ext-1",
		"email":          "ivan@example.com",
		"email_verified": true,
		"name":           "Иван Титов",
		"groups":         []any{"erp-buh", "all-staff"},
	})
	repo, h := newSSORepo(t, issuer.srv.URL, nil)
	ctx := t.Context()

	rec := runSSOLogin(t, h, issuer, "mock")
	if rec.Code != http.StatusFound {
		t.Fatalf("callback вернул %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); strings.HasPrefix(loc, "/login") {
		t.Fatalf("callback увёл на форму входа: %s", loc)
	}
	session := cookieNamed(rec, "onebase_session")
	if session == nil {
		t.Fatal("сессия не выдана")
	}
	// PKCE: verifier должен соответствовать вызову из redirect'а.
	if issuer.lastForm.Get("code_verifier") == "" {
		t.Fatal("token endpoint не получил code_verifier")
	}

	user, err := repo.LookupSession(ctx, session.Value)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if user.Login != "ivan@example.com" {
		t.Fatalf("логин учётки %q, ожидался ivan@example.com", user.Login)
	}
	if user.FullName != "Иван Титов" {
		t.Fatalf("имя учётки %q", user.FullName)
	}
	roles, err := repo.GetRolesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetRolesForUser: %v", err)
	}
	if len(roles) != 1 || roles[0].Name != "Бухгалтерия" {
		t.Fatalf("роли по маппингу: %+v", roles)
	}

	// Повторный вход находит ту же учётку, а не создаёт вторую.
	if rec2 := runSSOLogin(t, h, issuer, "mock"); cookieNamed(rec2, "onebase_session") == nil {
		t.Fatal("повторный вход не выдал сессию")
	}
	users, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 2 { // admin + заведённый через SSO
		t.Fatalf("пользователей в базе %d, ожидалось 2", len(users))
	}
}

func TestOIDCCallbackRevokesRoleWhenClaimDisappears(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-1", "email": "ivan@example.com", "email_verified": true,
		"groups": []any{"erp-buh"},
	})
	repo, h := newSSORepo(t, issuer.srv.URL, nil)
	ctx := t.Context()
	runSSOLogin(t, h, issuer, "mock")

	// Пользователя исключили из группы у провайдера — роль должна сняться.
	issuer.claims["groups"] = []any{"all-staff"}
	rec := runSSOLogin(t, h, issuer, "mock")
	session := cookieNamed(rec, "onebase_session")
	if session == nil {
		t.Fatalf("сессия не выдана: %s", rec.Header().Get("Location"))
	}
	user, err := repo.LookupSession(ctx, session.Value)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	roles, err := repo.GetRolesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetRolesForUser: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("роль осталась после исключения из группы: %+v", roles)
	}
}

func TestOIDCCallbackRejectsBrokenIDToken(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-1", "email": "ivan@example.com", "email_verified": true,
	})
	issuer.tamper = true
	_, h := newSSORepo(t, issuer.srv.URL, nil)

	rec := runSSOLogin(t, h, issuer, "mock")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("сессия выдана по id_token с испорченной подписью")
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("ожидался возврат на форму входа, получено %q", loc)
	}
}

func TestOIDCCallbackRejectsExpiredIDToken(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-1", "email": "ivan@example.com", "email_verified": true,
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	_, h := newSSORepo(t, issuer.srv.URL, nil)

	rec := runSSOLogin(t, h, issuer, "mock")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("сессия выдана по просроченному id_token")
	}
}

func TestOIDCCallbackRejectsForeignAudience(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-1", "email": "ivan@example.com", "email_verified": true,
		"aud": "другая-система",
	})
	_, h := newSSORepo(t, issuer.srv.URL, nil)

	rec := runSSOLogin(t, h, issuer, "mock")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("принят id_token, выданный другому клиенту")
	}
}

func TestOIDCCallbackRequiresStateCookie(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{"sub": "ext-1", "email": "ivan@example.com"})
	_, h := newSSORepo(t, issuer.srv.URL, nil)

	// Callback без куки state — попытка залогинить чужой браузер.
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/mock/callback?code=abc&state=подделка", nil)
	rec := httptest.NewRecorder()
	h.OIDCCallback(rec, req)
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("сессия выдана без куки state")
	}
}

func TestOIDCWithoutAutoCreateRefusesUnknownUser(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-1", "email": "чужой@example.com", "email_verified": true,
	})
	_, h := newSSORepo(t, issuer.srv.URL, func(p *auth.OIDCProvider) { p.AutoCreate = false })

	rec := runSSOLogin(t, h, issuer, "mock")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("заведена сессия для неизвестной учётки при выключенном автосоздании")
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "sso_user") {
		t.Fatalf("ожидалось сообщение о ненайденной учётке, получено %q", loc)
	}
}

func TestOIDCRequiresLocalSecondFactorUnlessTrusted(t *testing.T) {
	issuer := newMockIssuer(t, map[string]any{
		"sub": "ext-1", "email": "admin", "email_verified": true,
	})
	// Провайдер не считается источником MFA, а у учётки включён TOTP —
	// после SSO обязан спрашиваться локальный код.
	repo, h := newSSORepo(t, issuer.srv.URL, func(p *auth.OIDCProvider) { p.TrustMFA = false })
	ctx := t.Context()
	admin, err := repo.GetByLogin(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("GetByLogin: %v", err)
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if err := repo.EnableTOTP(ctx, admin.ID, secret, 0); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}

	rec := runSSOLogin(t, h, issuer, "mock")
	if cookieNamed(rec, "onebase_session") != nil {
		t.Fatal("SSO выдал сессию в обход локального второго фактора")
	}
	if cookieNamed(rec, "onebase_2fa") == nil {
		t.Fatalf("не предложен шаг второго фактора (код %d)", rec.Code)
	}
}
