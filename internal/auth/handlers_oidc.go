package auth

// Обработчики единого входа (план 84): /auth/oidc/<провайдер>/start и
// .../callback. Поток — Authorization Code + PKCE:
//
//	start    → редирект к провайдеру (state, nonce, code_challenge);
//	callback → сверка state, обмен кода на токены, проверка id_token,
//	           проекция на локальную учётку и обычная серверная сессия.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// oidcStateCookie — кука, привязывающая callback к тому же браузеру, который
// начинал вход. Без неё чужой callback-URL залогинил бы жертву в подставную
// учётку (login CSRF).
const oidcStateCookie = "onebase_oidc"

// oidcStateTTL — сколько живёт начатый вход. Пользователю нужно успеть ввести
// пароль у провайдера и, возможно, пройти его собственный второй фактор.
const oidcStateTTL = 15 * time.Minute

// oidcState — серверная половина начатого входа. В куке лежит только сам state,
// а verifier и nonce не покидают процесс.
type oidcState struct {
	provider  string
	verifier  string
	nonce     string
	returnURL string
	expires   time.Time
}

// Начатые входы копятся в памяти процесса на всё время жизни state, а начать
// вход может кто угодно: GET /auth/oidc/<id>/start не требует аутентификации.
// Без ограничений это неаутентифицированное исчерпание памяти — карта росла
// неограниченно, а чистка обходила её ЦЕЛИКОМ на каждой вставке под общим
// мьютексом, то есть деградировала квадратично (#615).
const (
	// maxOIDCStates — потолок числа начатых входов. При достижении вытесняется
	// самый старый: заблокировать вход всем на 15 минут хуже, чем потерять один
	// начатый вход, который к тому же всегда можно начать заново.
	maxOIDCStates = 10000
	// maxOIDCReturnURL — предел длины адреса возврата. Он приходит из query и
	// раньше ограничивался только размером заголовков (1 МиБ), то есть один
	// запрос удерживал мегабайт на 15 минут. Разумный адрес в тысячи раз короче.
	maxOIDCReturnURL = 2048
	// oidcSweepInterval — как часто вычищать протухшие. Чаще незачем: TTL
	// измеряется минутами, а обход карты стоит тем дороже, чем она больше.
	oidcSweepInterval = 30 * time.Second
)

var (
	oidcStatesMu    sync.Mutex
	oidcStates      = map[string]*oidcState{}
	oidcLastSweep   time.Time
	oidcStatesDrops int // вытеснено под давлением — видно в журнале
)

func putOIDCState(state string, s *oidcState) {
	oidcStatesMu.Lock()
	defer oidcStatesMu.Unlock()
	now := time.Now()
	if now.Sub(oidcLastSweep) >= oidcSweepInterval || len(oidcStates) >= maxOIDCStates {
		for k, v := range oidcStates {
			if now.After(v.expires) {
				delete(oidcStates, k)
			}
		}
		oidcLastSweep = now
	}
	// Чистка могла ничего не освободить: все записи свежие. Тогда вытесняем
	// самые старые, иначе потолок не соблюдается.
	for len(oidcStates) >= maxOIDCStates {
		oldestKey, oldest := "", time.Time{}
		for k, v := range oidcStates {
			if oldest.IsZero() || v.expires.Before(oldest) {
				oldestKey, oldest = k, v.expires
			}
		}
		if oldestKey == "" {
			break
		}
		delete(oidcStates, oldestKey)
		oidcStatesDrops++
	}
	oidcStates[state] = s
}

// oidcStateCount — размер карты начатых входов (для тестов и диагностики).
func oidcStateCount() int {
	oidcStatesMu.Lock()
	defer oidcStatesMu.Unlock()
	return len(oidcStates)
}

// takeOIDCState забирает состояние: одноразово, как и код авторизации.
func takeOIDCState(state string) (*oidcState, bool) {
	oidcStatesMu.Lock()
	defer oidcStatesMu.Unlock()
	s, ok := oidcStates[state]
	if !ok {
		return nil, false
	}
	delete(oidcStates, state)
	if time.Now().After(s.expires) {
		return nil, false
	}
	return s, true
}

func (h *Handlers) oidcClient() *OIDCClient {
	if h.OIDC != nil {
		return h.OIDC
	}
	return DefaultOIDCClient()
}

// providerIDFromPath вытаскивает идентификатор провайдера из /auth/oidc/<id>/…
// Разбор пути, а не chi.URLParam: пакет auth не должен зависеть от роутера —
// его обработчики монтируются и из лаунчера, и из тестов напрямую.
func providerIDFromPath(path string) string {
	rest := strings.TrimPrefix(path, "/auth/oidc/")
	if rest == path {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// redirectURI — адрес callback'а, который уходит провайдеру и должен совпадать
// с зарегистрированным у него. Внешний адрес берётся из настройки, если задан:
// за обратным прокси Host и схема запроса могут не совпадать с публичными.
func (h *Handlers) redirectURI(r *http.Request, providerID string) string {
	base := strings.TrimRight(strings.TrimSpace(h.BaseURL), "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		usedForwarded := false
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
			usedForwarded = true
		}
		host := r.Host
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = strings.TrimSpace(strings.Split(fwd, ",")[0])
			usedForwarded = true
		}
		if usedForwarded {
			warnForwardedWithoutPublicURL()
		}
		base = scheme + "://" + host
	}
	return base + "/auth/oidc/" + url.PathEscape(providerID) + "/callback"
}

var forwardedWarnOnce sync.Once

// warnForwardedWithoutPublicURL один раз за процесс предупреждает, что
// redirect_uri OIDC строится из X-Forwarded-* при незаданном ONEBASE_PUBLIC_URL.
// За НЕдоверенным прокси эти заголовки подделываются, и callback мог бы
// указывать на чужой хост. Это не жёсткая ошибка (провайдер всё равно сверяет
// redirect_uri с зарегистрированным списком, а state-кука привязывает callback
// к браузеру), но за обратным прокси следует задать ONEBASE_PUBLIC_URL (SEC-05).
func warnForwardedWithoutPublicURL() {
	forwardedWarnOnce.Do(func() {
		authLog().Warn("OIDC redirect_uri построен из X-Forwarded-* без ONEBASE_PUBLIC_URL",
			"подсказка", "за обратным прокси задайте ONEBASE_PUBLIC_URL=https://ваш-домен (SEC-05)")
	})
}

// OIDCStart начинает вход через внешнего провайдера.
func (h *Handlers) OIDCStart(w http.ResponseWriter, r *http.Request) {
	id := providerIDFromPath(r.URL.Path)
	p, ok := h.Repo.AuthProvider(r.Context(), id)
	if !ok || !p.Enabled {
		http.NotFound(w, r)
		return
	}
	doc, err := h.oidcClient().Discovery(r.Context(), p.Issuer)
	if err != nil {
		authLog().Error("SSO: не удалось прочитать метаданные провайдера", "провайдер", p.ID, "err", err)
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	verifier, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	returnURL := r.URL.Query().Get("return")
	// Длину режем ДО проверки локальности: неразумно длинный адрес возврата —
	// не вход пользователя, а способ удержать память процесса (#615).
	if returnURL == "" || len(returnURL) > maxOIDCReturnURL || !isLocalURL(returnURL) {
		returnURL = "/ui"
	}
	putOIDCState(state, &oidcState{
		provider:  p.ID,
		verifier:  verifier,
		nonce:     nonce,
		returnURL: returnURL,
		expires:   time.Now().Add(oidcStateTTL),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.SecureCookies || r.TLS != nil,
		// Lax недостаточно: провайдер возвращает пользователя кросс-сайтовым
		// GET, и при Strict кука до callback'а не доедет.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oidcStateTTL.Seconds()),
	})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", h.redirectURI(r, p.ID))
	q.Set("scope", strings.Join(p.ScopeList(), " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(doc.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	http.Redirect(w, r, doc.AuthorizationEndpoint+sep+q.Encode(), http.StatusFound)
}

// OIDCCallback завершает вход: сверяет state, меняет код на токены, проверяет
// id_token и заводит сессию.
func (h *Handlers) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	id := providerIDFromPath(r.URL.Path)
	p, ok := h.Repo.AuthProvider(r.Context(), id)
	if !ok || !p.Enabled {
		http.NotFound(w, r)
		return
	}
	h.clearOIDCStateCookie(w, r)

	if errCode := r.URL.Query().Get("error"); errCode != "" {
		authLog().Warn("SSO: провайдер отклонил вход", "провайдер", p.ID,
			"error", errCode, "описание", truncateForLog(r.URL.Query().Get("error_description")))
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}
	state := r.URL.Query().Get("state")
	cookie, cerr := r.Cookie(oidcStateCookie)
	if state == "" || cerr != nil || cookie.Value == "" || cookie.Value != state {
		authLog().Warn("SSO: state callback'а не совпал с кукой", "провайдер", p.ID)
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}
	st, ok := takeOIDCState(state)
	if !ok || st.provider != p.ID {
		authLog().Warn("SSO: неизвестный или просроченный state", "провайдер", p.ID)
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}

	client := h.oidcClient()
	tokens, err := client.Exchange(r.Context(), p, code, h.redirectURI(r, p.ID), st.verifier)
	if err != nil {
		authLog().Error("SSO: обмен кода не удался", "провайдер", p.ID, "err", err)
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}
	claims, err := client.VerifyIDToken(r.Context(), p, tokens.IDToken, st.nonce)
	if err != nil {
		authLog().Error("SSO: id_token не принят", "провайдер", p.ID, "err", err)
		http.Redirect(w, r, "/login?err=sso", http.StatusFound)
		return
	}
	// Провайдеры, не кладущие почту в id_token, отдают её в userinfo.
	if claimString(claims, p.LoginClaimName()) == "" {
		if extra, uerr := client.Userinfo(r.Context(), p, tokens.AccessToken); uerr != nil {
			authLog().Warn("SSO: userinfo недоступен", "провайдер", p.ID, "err", uerr)
		} else {
			for k, v := range extra {
				if _, exists := claims[k]; !exists {
					claims[k] = v
				}
			}
		}
	}

	user, err := h.Repo.UpsertSSOUser(r.Context(), p, claims)
	if err != nil {
		authLog().Error("SSO: не удалось сопоставить учётную запись", "провайдер", p.ID, "err", err)
		target := "/login?err=sso"
		if strings.Contains(err.Error(), ErrSSOUserNotFound.Error()) {
			target = "/login?err=sso_user"
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	// Второй фактор после SSO. Провайдер может обеспечивать его сам — тогда у
	// него стоит «доверять MFA провайдера»; иначе локальная политика обязана
	// действовать и здесь, иначе единый вход стал бы обходом требования 2FA.
	if !p.TrustMFA {
		policy := h.Repo.AuthPolicy(r.Context())
		switch enabled, terr := h.Repo.TOTPEnabled(r.Context(), user.ID); {
		case terr != nil:
			authLog().Error("SSO: не удалось проверить состояние 2FA", "логин", user.Login, "err", terr)
			http.Redirect(w, r, "/login?err=sso", http.StatusFound)
			return
		case enabled:
			h.beginSecondFactor(w, r, user, false, false, st.returnURL)
			return
		case h.Repo.RequiresTwoFactor(r.Context(), policy, user):
			// Через SSO личность подтвердил провайдер, а не один пароль, поэтому
			// первичную привязку разрешаем сразу, без кода привязки (issue #577).
			h.beginSecondFactor(w, r, user, true, true, st.returnURL)
			return
		}
	}

	token, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if h.Auditor != nil {
		h.Auditor.LogAction(r.Context(), "login_sso", "", p.ID, "", user.ID, user.Login, r.RemoteAddr)
	}
	h.setSessionCookie(w, r, token)
	http.Redirect(w, r, st.returnURL, http.StatusFound)
}

func (h *Handlers) clearOIDCStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.SecureCookies || r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// randomToken — 256 бит случайности в base64url: state, nonce и PKCE-verifier.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// pkceChallenge — S256-преобразование verifier'а (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
