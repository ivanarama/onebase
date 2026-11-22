package ui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/version"
)

func TestAboutDistributionHTTP(t *testing.T) {
	name, ver, supplier := version.DistributionName, version.DistributionVersion, version.DistributionSupplier
	t.Cleanup(func() {
		version.DistributionName, version.DistributionVersion, version.DistributionSupplier = name, ver, supplier
	})
	for _, fork := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream", true: "fork"}[fork], func(t *testing.T) {
			version.DistributionName, version.DistributionVersion, version.DistributionSupplier = "", "", ""
			if fork {
				version.DistributionName, version.DistributionVersion, version.DistributionSupplier = " <Fork> ", " 2.1 ", " <Supplier> "
			}
			fixture := newServerForFormMode(t)
			s := New(fixture.reg, fixture.store, interpreter.New(), nil, Config{PlatVersion: "v0.13.0", PlatAuthor: version.Author, PlatLicense: version.License, AppName: "Application", AppAuthor: "Configuration Author"}, nil)
			defer s.BeginShutdown()
			r := chi.NewRouter()
			s.Mount(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("GET", "/ui/about", nil))
			if rec.Code != 200 {
				t.Fatalf("about: %d %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range []string{"v0.13.0", version.Author, "MIT", "Application", "Configuration Author"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "Изменённая сборка") != fork {
				t.Fatal("incorrect distribution visibility")
			}
			if fork {
				for _, want := range []string{"&lt;Fork&gt;", "2.1", "&lt;Supplier&gt;"} {
					if !strings.Contains(body, want) {
						t.Errorf("missing %q", want)
					}
				}
				if strings.Contains(body, "<Fork>") || strings.Contains(body, "<Supplier>") {
					t.Fatal("distribution metadata was not escaped")
				}
			}
		})
	}
}
