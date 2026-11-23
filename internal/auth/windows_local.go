package auth

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/processcontrol"
)

// MountLocalWindowsLogin connects the launcher to the base it started under
// the same Windows account. Both processes read their own access token. No
// username or SID from the HTTP caller participates in the lookup.
func (h *Handlers) MountLocalWindowsLogin(r chi.Router, secret, baseID, instance, sid string) {
	if sid == "" || secret == "" || baseID == "" || instance == "" {
		return
	}
	codes := NewOneTimeCodes(30 * time.Second)
	// Authenticate the destination socket too. A listener forwarding requests
	// from a different configured port must never receive the browser grant.
	verify := func(r *http.Request, signature, challenge string) bool {
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		if err != nil || !net.ParseIP(peer).IsLoopback() || !ok || !local.IP.IsLoopback() {
			return false
		}
		want := processcontrol.WindowsLoginProof(secret, baseID, instance, sid, local.Port, challenge)
		return processcontrol.Verify(signature, want)
	}
	r.Post("/debug/process/windows-login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !verify(r, r.Header.Get(processcontrol.HeaderProof), "prepare") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		user, err := h.Repo.GetByWindowsSID(r.Context(), sid)
		if errors.Is(err, ErrAccountUnavailable) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if err != nil {
			h.internalErrorJSON(w, r, "проверка учётной записи Windows", err)
			return
		}
		// The challenge alone is not a login credential. Only the launcher
		// holding the base secret can sign it for delivery to a local window.
		code, err := codes.Issue(user.ID)
		if err != nil {
			h.internalErrorJSON(w, r, "выдача кода входа Windows", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		respondJSONTo(w, map[string]string{"code": code})
	})
	r.Get("/auth/windows/local", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		code, signature, ok := strings.Cut(r.URL.Query().Get("code"), ".")
		if !ok || !processcontrol.ValidNonce(code) || !verify(r, signature, code) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		expectedUserID, ok := codes.Exchange(code)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		h.finishWindowsLogin(w, r, sid, "/ui", expectedUserID, false)
	})
}
