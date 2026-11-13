package ui

import (
	"encoding/json"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise one shared cached widget through the same shell and refresh endpoints
// for non-admin, mixed and admin-with-excluded-role users.
func TestRoleOverridesThroughHTTP(t *testing.T) {
	s, router, _, _ := newWidgetPartialFixture(t)
	var cfg struct {
		Overrides []metadata.RoleOverride `yaml:"role_overrides"`
	}
	if err := yaml.Unmarshal([]byte(`role_overrides:
 - roles: [OTK, Chief]
   except_roles: [Operator, Messenger]
   include_admin: true
   background: "#f3f6fa"
`), &cfg); err != nil {
		t.Fatal(err)
	}
	w := s.reg.GetWidget("Товары")
	w.Background = "#f1ead6"
	w.RoleOverrides = cfg.Overrides
	actions := s.reg.GetWidget("Действия")
	actions.Items = []metadata.WidgetAction{{Label: "RoleOverrideActionProof", URL: "/ui/"}}
	hidden := false
	actions.RoleOverrides = []metadata.RoleOverride{{Roles: []string{"OTK", "Chief"}, ExceptRoles: []string{"Operator", "Messenger"}, IncludeAdmin: true, Visible: &hidden}}
	sp := &metadata.HomeSidePanel{Widget: w.Name, Background: w.Background, RoleOverrides: cfg.Overrides}
	s.reg.LoadHomePage(&metadata.HomePage{Rows: []metadata.HomePageRow{{Widgets: []string{w.Name, actions.Name}}}, SidePanel: sp})
	get := func(target string, u *auth.User) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r = r.WithContext(auth.ContextWithUser(r.Context(), u))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		return rec
	}
	cases := []struct {
		name  string
		admin bool
		roles []string
		cold  bool
	}{
		{"operator", false, []string{"Operator"}, false},
		{"pair", false, []string{"Operator", "Messenger"}, false},
		{"messenger", false, []string{"Messenger"}, false},
		{"otk", false, []string{"OTK"}, true},
		{"chief", false, []string{"Chief"}, true},
		{"other", false, []string{"Other"}, false},
		{"admin", true, nil, true},
		{"admin-operator", true, []string{"Operator"}, false},
		{"otk-messenger", false, []string{"OTK", "Messenger"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &auth.User{ID: tc.name, Login: tc.name, IsAdmin: tc.admin}
			for _, name := range tc.roles {
				u.Roles = append(u.Roles, &auth.Role{Name: name, Permissions: auth.Permission{Catalogs: map[string][]string{"Товар": {"read"}}}})
			}
			color := "#f1ead6"
			if tc.cold {
				color = "#f3f6fa"
			}
			shell := get("/ui/app?home=1", u)
			if shell.Code != 200 {
				t.Fatalf("shell: %d %.200s", shell.Code, shell.Body.String())
			}
			if strings.Count(shell.Body.String(), "background-color:"+color) < 2 {
				t.Fatalf("shell/panel color %s missing", color)
			}
			data := s.sidePanelData(httptest.NewRequest(http.MethodGet, "/ui/app", nil).WithContext(auth.ContextWithUser(t.Context(), u)))
			if data == nil || data["Background"] != color {
				t.Fatalf("panel background: %#v", data)
			}
			doc, err := html.Parse(strings.NewReader(shell.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			home := elementByID(doc, "ob-tabhome")
			if home == nil {
				t.Fatal("no home container")
			}
			visible := strings.Contains(sidePanelText(home), "RoleOverrideActionProof")
			if visible == tc.cold {
				t.Fatalf("quick actions visible=%v, scoped hide=%v", visible, tc.cold)
			}
			rec := get("/ui/_widget/"+w.Name, u)
			if rec.Code != 200 {
				t.Fatalf("refresh: %d", rec.Code)
			}
			var body struct {
				HTML string `json:"html"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body.HTML, "старое") {
				t.Fatal("visible rows lost")
			}
			if got := s.reg.GetWidget("Товары").Background; got != "#f1ead6" {
				t.Fatalf("shared metadata mutated: %s", got)
			}
		})
	}
	// Unsafe per-role colors remain fail-closed at render time.
	bad := "red;background-image:url(https://invalid/)"
	w.RoleOverrides[0].Background = &bad
	rec := get("/ui/app?home=1", &auth.User{Login: "admin", IsAdmin: true})
	if strings.Contains(rec.Body.String(), "background-image:url") {
		t.Fatal("unsafe override emitted")
	}
}
