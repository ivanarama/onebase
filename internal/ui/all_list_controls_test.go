package ui

import (
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Hit every business-list render template over an HTTP handler, including the
// less frequently used accounting lists. Excluded users must get identical HTML.
func TestAllBusinessListTemplatesRoleScopeThroughHTTP(t *testing.T) {
	entity := &metadata.Entity{Name: "ScopeProbe", Kind: metadata.KindCatalog}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	s.cfg.UIRoles = []string{"OTK", "Chief"}
	s.cfg.UIExceptRoles = []string{"Operator", "Messenger"}
	s.cfg.HideListDetailPanel = true
	s.cfg.HideListRowActions = true
	pages := []string{"page-list", "page-inforeg-list", "page-register-movements", "page-register-balances", "page-journal", "page-accounts", "page-accountreg-movements", "page-accountreg-balances"}
	for _, profile := range []struct {
		name          string
		roles         []string
		admin, active bool
	}{
		{"otk", []string{"OTK"}, false, true}, {"chief", []string{"Chief"}, false, true}, {"admin", nil, true, true},
		{"operator", []string{"Operator"}, false, false}, {"messenger", []string{"Messenger"}, false, false},
		{"pair", []string{"Operator", "Messenger"}, false, false}, {"mixed", []string{"OTK", "Messenger"}, false, false},
		{"admin-operator", []string{"Operator"}, true, false}, {"other", []string{"Other"}, false, false},
	} {
		t.Run(profile.name, func(t *testing.T) {
			user := &auth.User{ID: "scope-" + profile.name, Login: profile.name, IsAdmin: profile.admin}
			for _, name := range profile.roles {
				user.Roles = append(user.Roles, &auth.Role{Name: name})
			}
			for _, page := range pages {
				t.Run(page, func(t *testing.T) {
					render := func(enabled bool) string {
						s.cfg.UIEnhancements = enabled
						data := map[string]any{"Entity": entity, "InfoReg": &metadata.InfoRegister{Name: "ScopeProbe"}, "Register": &metadata.Register{Name: "ScopeProbe"}, "Journal": &metadata.Journal{Name: "ScopeProbe"}, "Chart": &metadata.ChartOfAccounts{Name: "ScopeProbe"}, "Rows": []map[string]any{}, "Params": storage.ListParams{}, "Page": 1, "TotalPages": 1, "Total": 0, "RequestURI": "/scope-probe", "Lang": "ru"}
						if strings.HasPrefix(page, "page-accountreg") {
							data["Register"] = &metadata.AccountRegister{Name: "ScopeProbe"}
						}
						handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.render(w, r, page, data) })
						req := httptest.NewRequest(http.MethodGet, "/scope-probe", nil)
						req = req.WithContext(auth.ContextWithUser(req.Context(), user))
						rec := httptest.NewRecorder()
						handler.ServeHTTP(rec, req)
						body := rec.Body.String()
						if rec.Code != 200 || !strings.HasSuffix(strings.TrimSpace(body), "</html>") {
							t.Fatalf("incomplete template response: %d %.180s", rec.Code, body[len(body)-min(180, len(body)):])
						}
						return body
					}
					before, after := render(false), render(true)
					if !profile.active && before != after {
						t.Fatal("excluded profile HTML changed")
					}
					if profile.active && !strings.Contains(after, `data-ob-list-panel-scope="id:scope-`+profile.name+`"`) {
						t.Fatal("scoped list root missing")
					}
					if profile.active && strings.Contains(after, `data-ob-detail-toggle`) {
						t.Fatal("hidden detail toggle left in DOM")
					}
					if strings.Contains(after, `<div style="margin-bottom:8px"></div>`) {
						t.Fatal("empty details spacer")
					}
				})
			}
		})
	}
}
