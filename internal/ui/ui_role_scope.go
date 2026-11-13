package ui

import (
	"net/http"

	"github.com/ivantit66/onebase/internal/auth"
)

func (s *Server) uiEnhancementsAllowed(r *http.Request) bool {
	return s.cfg.UIEnhancements && auth.RoleScopeMatches(auth.UserFromContext(r.Context()),
		s.cfg.UIRoles, s.cfg.UIExceptRoles, len(s.cfg.UIRoles) > 0)
}

// Resolve a copy per request: roles must never mutate the shared server config.
func (s *Server) uiConfigForRequest(r *http.Request, supplied ...Config) Config {
	cfg := s.cfg
	if len(supplied) > 0 {
		cfg = supplied[0]
	}
	cfg.UIEnhancements = cfg.UIEnhancements && auth.RoleScopeMatches(auth.UserFromContext(r.Context()), cfg.UIRoles, cfg.UIExceptRoles, len(cfg.UIRoles) > 0)
	if !cfg.UIEnhancements {
		cfg.HideListDetailPanel = false
		cfg.HideListRowActions = false
		cfg.HideAttachments = false
		cfg.DefaultFormDensity = ""
	}
	return cfg
}
