package ui

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
)

// setLang changes only the authenticated user's preference. An empty value
// removes the override and restores the existing base/browser fallback.
func (s *Server) setLang(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil || s.authRepo == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	lang := strings.ToLower(strings.TrimSpace(r.FormValue("lang")))
	if lang != "" {
		available := false
		if s.cfg.Bundle != nil {
			for _, option := range s.cfg.Bundle.Available() {
				if option.Code == lang {
					available = true
					break
				}
			}
		}
		if !available {
			http.Error(w, s.tr(s.resolveLang(r), "Язык интерфейса недоступен"), http.StatusBadRequest)
			return
		}
	}
	if err := s.authRepo.SetUserLang(r.Context(), u.ID, lang); err != nil {
		http.Error(w, s.tr(s.resolveLang(r), "Не удалось сохранить язык интерфейса"), http.StatusInternalServerError)
		return
	}
	back := "/ui/"
	if ref, err := url.Parse(r.Referer()); err == nil && (ref.Host == "" || ref.Host == r.Host) &&
		(ref.Scheme == "" || ref.Scheme == "http" || ref.Scheme == "https") &&
		(ref.Path == "/ui" || strings.HasPrefix(ref.Path, "/ui/")) {
		back = ref.RequestURI()
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
