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
	}
	formRoles, formExcept := cfg.UIRoles, cfg.UIExceptRoles
	if cfg.UIFormRoles != nil {
		formRoles = cfg.UIFormRoles
	}
	if cfg.UIFormExceptRoles != nil {
		formExcept = cfg.UIFormExceptRoles
	}
	if !s.cfg.UIEnhancements || !auth.RoleScopeMatches(auth.UserFromContext(r.Context()), formRoles, formExcept, len(formRoles) > 0) {
		cfg.HideAttachments = false
		cfg.DefaultFormDensity = ""
	}
	reportRoles, reportExcept := cfg.UIRoles, cfg.UIExceptRoles
	if cfg.UIReportRoles != nil {
		reportRoles = cfg.UIReportRoles
	}
	if cfg.UIReportExceptRoles != nil {
		reportExcept = cfg.UIReportExceptRoles
	}
	if !auth.RoleScopeMatches(auth.UserFromContext(r.Context()), reportRoles, reportExcept, len(reportRoles) > 0) {
		cfg.DefaultReportDensity = ""
	}
	return cfg
}
