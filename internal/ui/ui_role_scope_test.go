package ui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestUIRoleScopeKeepsOperatorSettingsAndSharedConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		admin  bool
		roles  []string
		active bool
	}{
		{"otk", false, []string{"OTK"}, true}, {"admin", true, nil, true},
		{"operator", false, []string{"Operator"}, false}, {"messenger", false, []string{"Messenger"}, false},
		{"pair", false, []string{"Operator", "Messenger"}, false}, {"mixed", false, []string{"OTK", "Messenger"}, false},
		{"admin-operator", true, []string{"Operator"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: Config{CompactLists: true, HideListDetailPanel: true, HideListRowActions: true, HideAttachments: true, DefaultFormDensity: "dense", UIEnhancements: true, UIRoles: []string{"OTK"}, UIExceptRoles: []string{"Operator", "Messenger"}}}
			u := &auth.User{IsAdmin: tc.admin}
			for _, role := range tc.roles {
				u.Roles = append(u.Roles, &auth.Role{Name: role})
			}
			r := httptest.NewRequest("GET", "/ui/", nil)
			r = r.WithContext(auth.ContextWithUser(r.Context(), u))
			cfg := s.uiConfigForRequest(r)
			if cfg.UIEnhancements != tc.active || cfg.HideListDetailPanel != tc.active || cfg.HideListRowActions != tc.active || cfg.HideAttachments != tc.active || (cfg.DefaultFormDensity != "") != tc.active {
				t.Fatalf("wrong presentation scope: %+v", cfg)
			}
			if !cfg.CompactLists || !s.cfg.HideAttachments || s.cfg.DefaultFormDensity != "dense" {
				t.Fatal("legacy list_density/shared server config changed")
			}
		})
	}
}

func TestUIListDetailsAndActionsThroughHTTP(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "hidden"}[disabled], func(t *testing.T) {
			s, e, _ := setupDeleteActionServer(t)
			s.cfg.UIEnhancements = disabled
			s.cfg.HideListDetailPanel = disabled
			s.cfg.HideListRowActions = disabled
			rec := httptest.NewRecorder()
			s.list(rec, reqWithChi("GET", "/ui/catalog/"+e.Name, nil, map[string]string{"kind": "catalog", "entity": e.Name}))
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, `id="ob-detail"`) == disabled || strings.Contains(body, `data-ob-detail-toggle`) == disabled {
				t.Fatal("details DOM does not follow global switch")
			}
			if strings.Contains(body, `class="btn btn-sm btn-primary" href="/ui/catalog/`) == disabled {
				t.Fatal("row action does not follow switch")
			}
		})
	}
}

func TestUIAttachmentsGlobalSwitchOverridesVisibleTrue(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, hidden := range []bool{false, true} {
			s, e, id := setupDeleteActionServer(t)
			s.cfg.UIEnhancements = hidden
			s.cfg.HideAttachments = hidden
			if managed {
				visible := true
				e.Forms[0].Actions = map[string]*metadata.FormAction{"attachments": {Visible: &visible}}
			} else {
				e.Forms = nil
			}
			body := renderObjectFormGET(t, s, e, id)
			if strings.Contains(body, "data-ob-attachments") == hidden {
				t.Fatalf("managed=%t hidden=%t: attachments DOM mismatch", managed, hidden)
			}
		}
	}
}
