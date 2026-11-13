package ui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func TestGlobalFormDensityThroughHTTPKeepsOperatorsAndOwnDensity(t *testing.T) {
	for _, density := range []string{"compact", "dense"} {
		t.Run(density, func(t *testing.T) {
			fields := []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}
			auto := &metadata.Entity{Name: "AutoProbe", Kind: metadata.KindCatalog, Fields: fields}
			doc := &metadata.Entity{Name: "DocumentProbe", Kind: metadata.KindDocument, Fields: fields}
			managed := layoutTestEntity(fieldEl("Caption", "Объект.Наименование"))
			managed.Name = "ManagedProbe"
			managed.Forms[0].EntityName = managed.Name
			own := layoutTestEntity(fieldEl("Caption", "Объект.Наименование"))
			own.Name = "OwnProbe"
			own.Forms[0].EntityName = own.Name
			own.Forms[0].Density = "compact"
			s, _ := newSubmitTestServer(t, []*metadata.Entity{auto, doc, managed, own})
			s.reg.LoadProcessors([]*processor.Processor{{Name: "AutoProcessor", Params: []processor.Param{{Name: "Caption", Type: "string"}}}})
			s.cfg.UIEnhancements = true
			s.cfg.UIRoles = []string{"OTK"}
			s.cfg.UIExceptRoles = []string{"Operator", "Messenger"}
			s.cfg.UIFormRoles = []string{}
			s.cfg.UIFormExceptRoles = []string{"Operator", "Messenger"}
			router := chi.NewRouter()
			s.Mount(router)
			for _, profile := range []struct {
				name          string
				roles         []string
				admin, active bool
			}{
				{"other", []string{"Warehouse"}, false, true}, {"otk", []string{"OTK"}, false, true}, {"admin", nil, true, true},
				{"operator", []string{"Operator"}, false, false}, {"messenger", []string{"Messenger"}, false, false}, {"pair", []string{"Operator", "Messenger"}, false, false},
				{"mixed", []string{"Warehouse", "Messenger"}, false, false}, {"admin-operator", []string{"Operator"}, true, false},
			} {
				t.Run(profile.name, func(t *testing.T) {
					u := &auth.User{ID: "density-test-user", Login: "density-test-user", IsAdmin: profile.admin}
					for _, name := range profile.roles {
						u.Roles = append(u.Roles, &auth.Role{Name: name, Permissions: auth.Permission{Catalogs: map[string][]string{auto.Name: {"read", "write"}, managed.Name: {"read", "write"}, own.Name: {"read", "write"}}, Documents: map[string][]string{doc.Name: {"read", "write"}}}})
					}
					for _, page := range []struct {
						path           string
						automatic, own bool
					}{
						{"/ui/catalog/AutoProbe/new", true, false}, {"/ui/document/DocumentProbe/new", true, false}, {"/ui/catalog/ManagedProbe/new", false, false}, {"/ui/catalog/OwnProbe/new", false, true}, {"/ui/processor/AutoProcessor", true, false},
					} {
						t.Run(page.path, func(t *testing.T) {
							get := func() string {
								req := httptest.NewRequest(http.MethodGet, page.path, nil)
								req = req.WithContext(auth.ContextWithUser(req.Context(), u))
								rec := httptest.NewRecorder()
								router.ServeHTTP(rec, req)
								if rec.Code != 200 {
									t.Fatalf("GET %d %.160s", rec.Code, rec.Body.String())
								}
								return rec.Body.String()
							}
							s.cfg.DefaultFormDensity = ""
							before := get()
							s.cfg.DefaultFormDensity = density
							after := get()
							htmlTag := regexp.MustCompile(`<html\b[^>]*>`)
							root := htmlTag.FindString(after)
							expected := ""
							if page.own {
								expected = "compact"
							} else if profile.active {
								expected = density
							}
							if strings.Contains(root, "ob-density-compact") != (expected != "") || strings.Contains(root, "ob-density-dense") != (expected == "dense") {
								t.Fatalf("wrong root density: %s expected%q", root, expected)
							}
							autoStyle := profile.active && page.automatic
							if strings.Contains(after, `id="ob-auto-form-density-css"`) != autoStyle || strings.Contains(root, "ob-auto-form-density") != autoStyle {
								t.Fatal("automatic-form CSS leaked across scope or own density")
							}
							sources := regexp.MustCompile(`<script[^>]*\bsrc=[^>]*>`)
							if strings.Join(sources.FindAllString(before, -1), "\n") != strings.Join(sources.FindAllString(after, -1), "\n") {
								t.Fatal("density added JS resources")
							}
							if !profile.active || page.own {
								if htmlTag.FindString(before) != root || strings.Count(before, "<style") != strings.Count(after, "<style") {
									t.Fatal("excluded profile/explicit form density changed")
								}
							}
						})
					}
				})
			}
			if s.cfg.DefaultFormDensity != density {
				t.Fatal("request role scope mutated shared density config")
			}
		})
	}
}
