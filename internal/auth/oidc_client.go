package auth

// Клиент OpenID Connect: discovery, обмен кода на токены, проверка подписи
// id_token по JWKS (план 84). Только то, что нужно потоку Authorization Code +
// PKCE, — без регистрации клиентов, без refresh, без back-channel logout.

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// oidcHTTPTimeout — потолок на один поход к провайдеру. Вход не должен висеть
// дольше терпения пользователя, а зависший провайдер не должен занимать
// воркеры сервера.
const oidcHTTPTimeout = 15 * time.Second

// oidcDiscoveryTTL — как долго живёт кэш метаданных и ключей. Ключи ротируются
// редко, но неизвестный kid вызывает внеочередное обновление (см. keyFor).
const oidcDiscoveryTTL = time.Hour

// oidcClockSkew — допуск на расхождение часов с провайдером.
const oidcClockSkew = 2 * time.Minute

// maxOIDCResponseBytes ограничивает ответы провайдера: discovery, JWKS и
// токены — это килобайты, а читать неограниченный поток от внешней стороны
// нельзя.
const maxOIDCResponseBytes = 1 << 20

// oidcDiscovery — нужная часть openid-configuration.
type oidcDiscovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	AuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
}

// jwk — ключ подписи из JWKS (поддерживается RSA: RS256/RS384/RS512).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// oidcCacheEntry — закэшированные метаданные провайдера и его ключи.
type oidcCacheEntry struct {
	discovery   *oidcDiscovery
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	keysFetched time.Time
}

// OIDCClient — HTTP-клиент провайдеров с кэшем discovery и ключей.
// Потокобезопасен; один на процесс (см. defaultOIDCClient).
type OIDCClient struct {
	HTTP *http.Client

	mu    sync.Mutex
	cache map[string]*oidcCacheEntry // ключ — issuer
	// now подменяется в тестах (проверка exp/iat).
	now func() time.Time
}

// NewOIDCClient собирает клиента с разумными таймаутами.
func NewOIDCClient() *OIDCClient {
	return &OIDCClient{
		HTTP:  &http.Client{Timeout: oidcHTTPTimeout},
		cache: make(map[string]*oidcCacheEntry),
		now:   time.Now,
	}
}

var (
	defaultOIDCOnce   sync.Once
	defaultOIDCClient *OIDCClient
)

// DefaultOIDCClient — общий клиент процесса: кэш discovery/JWKS переживает
// отдельные входы, иначе каждая попытка входа стоила бы двух запросов наружу.
func DefaultOIDCClient() *OIDCClient {
	defaultOIDCOnce.Do(func() { defaultOIDCClient = NewOIDCClient() })
	return defaultOIDCClient
}

func (c *OIDCClient) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// checkIssuerScheme требует https везде, кроме локальных адресов: по http
// id_token и код авторизации едут открытым текстом. Локальный http оставлен
// осознанно — на нём работают отладочные Keycloak/мок-issuer.
func checkIssuerScheme(issuer string) error {
	u, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil {
		return fmt.Errorf("auth: issuer %q: %w", issuer, err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLocalHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("auth: issuer %q: http допустим только для localhost", issuer)
	default:
		return fmt.Errorf("auth: issuer %q: ожидался https", issuer)
	}
}

func isLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Discovery возвращает метаданные провайдера (из кэша, пока они свежие).
func (c *OIDCClient) Discovery(ctx context.Context, issuer string) (*oidcDiscovery, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if err := checkIssuerScheme(issuer); err != nil {
		return nil, err
	}
	c.mu.Lock()
	entry := c.cache[issuer]
	if entry != nil && entry.discovery != nil && c.timeNow().Sub(entry.fetchedAt) < oidcDiscoveryTTL {
		d := entry.discovery
		c.mu.Unlock()
		return d, nil
	}
	c.mu.Unlock()

	var doc oidcDiscovery
	if err := c.getJSON(ctx, issuer+"/.well-known/openid-configuration", &doc); err != nil {
		return nil, fmt.Errorf("auth: openid-configuration %s: %w", issuer, err)
	}
	// Совпадение issuer в документе с запрошенным — обязательная проверка
	// спецификации: без неё подменённый DNS увёл бы вход к чужому провайдеру,
	// а проверка iss в id_token это бы пропустила.
	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return nil, fmt.Errorf("auth: провайдер вернул issuer %q вместо %q", doc.Issuer, issuer)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, fmt.Errorf("auth: неполный openid-configuration у %s", issuer)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry = c.cache[issuer]
	if entry == nil {
		entry = &oidcCacheEntry{}
		c.cache[issuer] = entry
	}
	entry.discovery = &doc
	entry.fetchedAt = c.timeNow()
	return &doc, nil
}

// keyFor возвращает открытый ключ по kid, при необходимости перечитывая JWKS.
// Неизвестный kid — штатная ситуация ротации ключей, а не ошибка: провайдер
// подписал токен новым ключом, и его нужно догрузить. Повторное чтение
// ограничено: без ограничения любой мусорный kid стал бы усилителем запросов
// к провайдеру.
func (c *OIDCClient) keyFor(ctx context.Context, issuer, jwksURI, kid string) (*rsa.PublicKey, error) {
	if key := c.cachedKey(issuer, kid); key != nil {
		return key, nil
	}
	c.mu.Lock()
	entry := c.cache[issuer]
	tooSoon := entry != nil && c.timeNow().Sub(entry.keysFetched) < time.Minute
	c.mu.Unlock()
	if tooSoon {
		return nil, fmt.Errorf("auth: ключ подписи %q не найден в JWKS", kid)
	}

	var set jwkSet
	if err := c.getJSON(ctx, jwksURI, &set); err != nil {
		return nil, fmt.Errorf("auth: чтение JWKS: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if !strings.EqualFold(k.Kty, "RSA") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k)
		if err != nil {
			authLog().Warn("непригодный ключ в JWKS", "kid", k.Kid, "err", err)
			continue
		}
		keys[k.Kid] = pub
	}

	c.mu.Lock()
	entry = c.cache[issuer]
	if entry == nil {
		entry = &oidcCacheEntry{}
		c.cache[issuer] = entry
	}
	entry.keys = keys
	entry.keysFetched = c.timeNow()
	c.mu.Unlock()

	if key := c.cachedKey(issuer, kid); key != nil {
		return key, nil
	}
	// Единственный ключ без kid — распространённый случай у мелких провайдеров.
	if kid == "" && len(keys) == 1 {
		for _, k := range keys {
			return k, nil
		}
	}
	return nil, fmt.Errorf("auth: ключ подписи %q не найден в JWKS", kid)
}

func (c *OIDCClient) cachedKey(issuer, kid string) *rsa.PublicKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.cache[issuer]
	if entry == nil || entry.keys == nil {
		return nil
	}
	if key, ok := entry.keys[kid]; ok {
		return key
	}
	if kid == "" && len(entry.keys) == 1 {
		for _, key := range entry.keys {
			return key
		}
	}
	return nil
}

func rsaPublicKeyFromJWK(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.N, "="))
	if err != nil {
		return nil, fmt.Errorf("модуль: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
	if err != nil {
		return nil, fmt.Errorf("экспонента: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, errors.New("пустые параметры ключа")
	}
	// Экспонента приходит big-endian переменной длины (обычно три байта AQAB).
	var e uint64
	if len(eBytes) > 8 {
		return nil, errors.New("слишком длинная экспонента")
	}
	padded := make([]byte, 8)
	copy(padded[8-len(eBytes):], eBytes)
	e = binary.BigEndian.Uint64(padded)
	if e < 3 || e > 1<<31 {
		return nil, fmt.Errorf("недопустимая экспонента %d", e)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}

// OIDCTokens — ответ token endpoint в объёме, который нам нужен.
type OIDCTokens struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// Exchange меняет код авторизации на токены (Authorization Code + PKCE).
func (c *OIDCClient) Exchange(ctx context.Context, p *OIDCProvider, code, redirectURI, verifier string) (*OIDCTokens, error) {
	doc, err := c.Discovery(ctx, p.Issuer)
	if err != nil {
		return nil, err
	}
	secret, err := p.ResolvedClientSecret()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", p.ClientID)
	form.Set("code_verifier", verifier)

	useBasic := secret != "" && supportsAuthMethod(doc.AuthMethods, "client_secret_basic")
	if secret != "" && !useBasic {
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if useBasic {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(secret))
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: обмен кода: %w", err)
	}
	defer drainAndClose(resp)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOIDCResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("auth: обмен кода: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Тело ответа провайдера не показываем пользователю (см. вызывающий
		// код), но в журнал оно нужно: без него диагноз «SSO не работает»
		// нечем подтвердить.
		authLog().Error("token endpoint вернул ошибку", "провайдер", p.ID,
			"код", resp.StatusCode, "тело", truncateForLog(string(body)))
		return nil, fmt.Errorf("auth: провайдер отклонил обмен кода (HTTP %d)", resp.StatusCode)
	}
	var tokens OIDCTokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("auth: разбор ответа token endpoint: %w", err)
	}
	if strings.TrimSpace(tokens.IDToken) == "" {
		return nil, errors.New("auth: провайдер не вернул id_token")
	}
	return &tokens, nil
}

func supportsAuthMethod(methods []string, want string) bool {
	for _, m := range methods {
		if strings.EqualFold(strings.TrimSpace(m), want) {
			return true
		}
	}
	return false
}

// VerifyIDToken проверяет подпись и обязательные claim'ы id_token и возвращает
// его полезную нагрузку.
func (c *OIDCClient) VerifyIDToken(ctx context.Context, p *OIDCProvider, idToken, nonce string) (map[string]any, error) {
	doc, err := c.Discovery(ctx, p.Issuer)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("auth: id_token не является JWS")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("auth: заголовок id_token: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("auth: заголовок id_token: %w", err)
	}
	hash, err := hashForAlg(header.Alg)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("auth: подпись id_token: %w", err)
	}
	key, err := c.keyFor(ctx, strings.TrimRight(p.Issuer, "/"), doc.JWKSURI, header.Kid)
	if err != nil {
		return nil, err
	}
	digest := hashBytes(hash, []byte(parts[0]+"."+parts[1]))
	if err := rsa.VerifyPKCS1v15(key, hash, digest, sig); err != nil {
		return nil, errors.New("auth: подпись id_token не подтверждена")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("auth: тело id_token: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("auth: тело id_token: %w", err)
	}
	if err := c.checkClaims(p, claims, nonce); err != nil {
		return nil, err
	}
	return claims, nil
}

// checkClaims проверяет iss/aud/exp/iat/nonce. Каждая проверка обязательна:
// без aud чужой клиент того же провайдера пускал бы к нам, без nonce — повтор
// перехваченного id_token.
func (c *OIDCClient) checkClaims(p *OIDCProvider, claims map[string]any, nonce string) error {
	iss := strings.TrimRight(claimString(claims, "iss"), "/")
	if iss != strings.TrimRight(strings.TrimSpace(p.Issuer), "/") {
		return fmt.Errorf("auth: id_token выдан другим issuer (%q)", iss)
	}
	if !audienceContains(claims["aud"], p.ClientID) {
		return errors.New("auth: id_token выдан другому клиенту")
	}
	now := c.timeNow()
	exp, ok := claimTime(claims, "exp")
	if !ok {
		return errors.New("auth: в id_token нет exp")
	}
	if now.After(exp.Add(oidcClockSkew)) {
		return errors.New("auth: id_token просрочен")
	}
	if iat, ok := claimTime(claims, "iat"); ok && iat.After(now.Add(oidcClockSkew)) {
		return errors.New("auth: id_token выдан будущим временем")
	}
	if nonce != "" && claimString(claims, "nonce") != nonce {
		return errors.New("auth: nonce id_token не совпадает")
	}
	if strings.TrimSpace(claimString(claims, "sub")) == "" {
		return errors.New("auth: в id_token нет sub")
	}
	return nil
}

func audienceContains(aud any, clientID string) bool {
	for _, v := range claimStrings(aud) {
		if strings.TrimSpace(v) == clientID {
			return true
		}
	}
	return false
}

func claimTime(claims map[string]any, name string) (time.Time, bool) {
	switch t := claims[name].(type) {
	case float64:
		return time.Unix(int64(t), 0), true
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return time.Unix(n, 0), true
		}
	}
	return time.Time{}, false
}

func hashForAlg(alg string) (crypto.Hash, error) {
	switch strings.ToUpper(strings.TrimSpace(alg)) {
	case "RS256":
		return crypto.SHA256, nil
	case "RS384":
		return crypto.SHA384, nil
	case "RS512":
		return crypto.SHA512, nil
	default:
		// В том числе alg=none и HS*: симметричные подписи на клиентском
		// секрете здесь не поддерживаются намеренно — исторически именно на них
		// строились обходы проверки подписи.
		return 0, fmt.Errorf("auth: неподдерживаемый алгоритм подписи id_token: %q", alg)
	}
}

func hashBytes(h crypto.Hash, data []byte) []byte {
	switch h {
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		sum := sha256.Sum256(data)
		return sum[:]
	}
}

// Userinfo дочитывает claim'ы из userinfo endpoint. Нужен провайдерам, которые
// не кладут почту в id_token; вызывается только если нужного claim'а не нашлось.
func (c *OIDCClient) Userinfo(ctx context.Context, p *OIDCProvider, accessToken string) (map[string]any, error) {
	doc, err := c.Discovery(ctx, p.Issuer)
	if err != nil {
		return nil, err
	}
	if doc.UserinfoEndpoint == "" || strings.TrimSpace(accessToken) == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, doc.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer drainAndClose(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: userinfo вернул HTTP %d", resp.StatusCode)
	}
	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOIDCResponseBytes)).Decode(&claims); err != nil {
		return nil, fmt.Errorf("auth: разбор userinfo: %w", err)
	}
	return claims, nil
}

func (c *OIDCClient) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer drainAndClose(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxOIDCResponseBytes)).Decode(out)
}

func (c *OIDCClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: oidcHTTPTimeout}
}

// drainAndClose закрывает тело ответа, дочитывая остаток: иначе соединение не
// вернётся в пул keep-alive.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}

// truncateForLog режет чужой ответ до размера, который не забьёт журнал.
func truncateForLog(s string) string {
	const limit = 512
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
