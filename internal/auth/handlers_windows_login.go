package auth

import (
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
)

// Kerberos tickets containing group memberships can exceed 16 KiB.
const maxWindowsTicket = 64 << 10

// WindowsLoginAvailable describes the transport on which the browser may
// attempt Integrated Authentication. Forwarded headers never enable it.
func (h *Handlers) WindowsLoginAvailable(r *http.Request) bool {
	return runtime.GOOS == "windows" && h.windowsLoginTransport(r)
}

func (h *Handlers) windowsLoginTransport(r *http.Request) bool {
	if h.WindowsSPN == "" {
		return false
	}
	host := (&url.URL{Host: r.Host}).Hostname()
	if r.TLS == nil {
		public, err := url.Parse(h.BaseURL)
		if err != nil || public.Scheme != "https" || public.Hostname() == "" {
			return false
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(peer).IsLoopback() {
			return false
		}
		host = public.Hostname()
	}
	return host != "" && strings.EqualFold(h.WindowsSPN, "HTTP/"+host)
}

// WindowsLogin handles GET /auth/windows. Only an explicitly initiated
// same-origin fetch may trigger a Negotiate challenge in the browser.
func (h *Handlers) WindowsLogin(w http.ResponseWriter, r *http.Request) {
	if !h.WindowsLoginAvailable(r) || r.Header.Get("X-OneBase-Windows") != "1" {
		http.NotFound(w, r)
		return
	}
	h.negotiateWindows(w, r, acceptWindowsTicket)
}

func (h *Handlers) negotiateWindows(w http.ResponseWriter, r *http.Request, accept func(string, []byte) (string, []byte, error)) {
	w.Header().Set("Cache-Control", "no-store, private")
	authorization := r.Header.Get("Authorization")
	if authorization == "" {
		w.Header().Set("WWW-Authenticate", "Negotiate")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	scheme, encoded, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Negotiate") || encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(maxWindowsTicket) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ticket, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(ticket) == 0 || len(ticket) > maxWindowsTicket {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	sid, reply, err := accept(h.WindowsSPN, ticket)
	if err != nil || sid == "" {
		// Do not log tokens or issue a new challenge (which could cause a
		// password prompt/loop for clients that fell back to NTLM).
		authLog().Debug("Windows: билет не принят", "err", err)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if len(reply) > 0 {
		w.Header().Set("WWW-Authenticate", "Negotiate "+base64.StdEncoding.EncodeToString(reply))
	}
	h.finishWindowsLogin(w, r, sid, r.URL.Query().Get("return"), "", true)
}

// finishWindowsLogin receives a SID already established by SSPI or the local
// launcher handoff. Each sign-in, including completion after MFA, must still
// match an active user in this database.
func (h *Handlers) finishWindowsLogin(w http.ResponseWriter, r *http.Request, sid, returnURL, expectedUserID string, jsonResponse bool) {
	w.Header().Set("Cache-Control", "no-store, private")
	user, err := h.Repo.GetByWindowsSID(r.Context(), sid)
	if errors.Is(err, ErrAccountUnavailable) || (err == nil && expectedUserID != "" && expectedUserID != user.ID) {
		if jsonResponse {
			w.WriteHeader(http.StatusForbidden)
		} else {
			http.Redirect(w, r, "/login", http.StatusFound)
		}
		return
	}
	if err != nil {
		h.internalError(w, r, "поиск пользователя Windows", err)
		return
	}
	if !isLocalURL(returnURL) || strings.ContainsAny(returnURL, "\\\r\n\t") {
		returnURL = "/ui"
	}
	enabled, err := h.Repo.TOTPEnabled(r.Context(), user.ID)
	if err != nil {
		h.internalError(w, r, "чтение второго фактора при входе через Windows", err)
		return
	}
	policy := h.Repo.AuthPolicy(r.Context())
	if enabled || h.Repo.RequiresTwoFactor(r.Context(), policy, user) {
		ch := Challenge{UserID: user.ID, Login: user.Login, ReturnURL: returnURL, windowsSID: sid, Enroll: !enabled}
		if !h.issueLoginChallenge(w, r, &ch, policy.SelfEnroll2FA) {
			return
		}
		returnURL = "/login/2fa"
	} else {
		token, err := h.Repo.CreateWindowsSession(r.Context(), user.ID, sid, sessionMetaFromRequest(r))
		if errors.Is(err, ErrAccountUnavailable) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err != nil {
			h.internalError(w, r, "создание сессии после входа через Windows", err)
			return
		}
		if h.Auditor != nil {
			h.Auditor.LogAction(r.Context(), "login", "", "Windows", "", user.ID, user.Login, r.RemoteAddr)
		}
		h.setSessionCookie(w, r, token)
	}
	if jsonResponse {
		w.Header().Set("Content-Type", "application/json")
		respondJSONTo(w, map[string]string{"redirect": returnURL})
	} else {
		http.Redirect(w, r, returnURL, http.StatusFound)
	}
}
