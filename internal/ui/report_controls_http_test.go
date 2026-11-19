package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestCompactReportsRoleScopeThroughHTTP(t *testing.T) {
	for _, kind := range []string{"parameters", "table", "composed", "no-parameters"} {
		t.Run(kind, func(t *testing.T) {
			report := &reportpkg.Report{Name: "CompactProbe", Query: "ВЫБРАТЬ &Число КАК Число", Params: []reportpkg.Param{{Name: "Число", Type: "number", Default: "1"}, {Name: "Day", Type: "date"}}}
			if kind == "composed" {
				report.Composition = &reportpkg.Composition{Groupings: []string{"Число"}, Detail: true}
			}
			if kind == "no-parameters" {
				report.Query = "ВЫБРАТЬ 1 КАК Число"
				report.Params = nil
			}
			server := newReportExportTestServer(t, report)
			server.cfg.UIRoles = []string{"OTK"}
			server.cfg.UIExceptRoles = []string{"Operator", "Messenger"}
			server.cfg.UIReportRoles = []string{}
			server.cfg.UIReportExceptRoles = []string{"Operator", "Messenger"}
			server.cfg.ShortDateInput = true
			router := chi.NewRouter()
			server.Mount(router)
			for _, profile := range []struct {
				name, role string
				admin      bool
				active     bool
			}{
				{"otk", "OTK", false, true}, {"chief", "Chief", false, true}, {"other", "Warehouse", false, true}, {"admin", "", true, true},
				{"operator", "Operator", false, false}, {"messenger", "Messenger", false, false}, {"pair", "Operator", false, false}, {"mixed", "Messenger", false, false}, {"admin-operator", "Operator", true, false},
			} {
				t.Run(profile.name, func(t *testing.T) {
					user := &auth.User{ID: profile.name, Login: profile.name, IsAdmin: profile.admin, Roles: []*auth.Role{{Name: profile.role, Permissions: auth.Permission{Reports: map[string][]string{report.Name: {"run"}}}}}}
					if profile.name == "pair" {
						user.Roles = append(user.Roles, &auth.Role{Name: "Messenger"})
					}
					if profile.name == "mixed" {
						user.Roles = append(user.Roles, &auth.Role{Name: "OTK"})
					}
					get := func(density string) string {
						server.cfg.DefaultReportDensity = density
						path := "/ui/report/compactprobe"
						if kind != "parameters" {
							path += "?__run=1"
						}
						request := httptest.NewRequest(http.MethodGet, path, nil)
						request = request.WithContext(auth.ContextWithUser(request.Context(), user))
						recorder := httptest.NewRecorder()
						router.ServeHTTP(recorder, request)
						if recorder.Code != http.StatusOK {
							t.Fatalf("GET status=%d %.200s", recorder.Code, recorder.Body.String())
						}
						return recorder.Body.String()
					}
					before, after := get(""), get("compact")
					if strings.Contains(after, `class="ob-report-compact`) != profile.active || strings.Contains(after, `/static/report-controls.js`) != profile.active {
						t.Fatal("report scope differs")
					}
					if !profile.active && after != before {
						t.Fatal("excluded operator report HTML changed")
					}
					if !strings.Contains(after, "/static/date-input.js") {
						t.Fatal("short date behavior must include operator profiles")
					}
					if profile.active && strings.Count(after, `form="ob-report-params"`) != 1 {
						t.Fatal("one explicit generate button must target the parameter form")
					}
					if kind != "parameters" {
						for _, format := range []string{"excel", "pdf"} {
							if strings.Count(after, "/ui/report/compactprobe/export/"+format) != 1 {
								t.Fatalf("duplicate/missing %s export", format)
							}
						}
					}
					if server.cfg.DefaultReportDensity != "compact" {
						t.Fatal("role resolution mutated shared config")
					}
				})
			}
		})
	}
}
