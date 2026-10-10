package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func TestAPIV2_OpenAPIReportDatetime(t *testing.T) {
	rep := &reportpkg.Report{
		Name: "Events",
		Params: []reportpkg.Param{
			{Name: "From", Type: "datetime"},
			{Name: "Until", Type: "DATETIME"},
			{Name: "Day", Type: "date"},
		},
		Query: "ВЫБРАТЬ 1 КАК Значение",
	}
	h, _ := newAPITestHandlerWithReports(t, nil, []*reportpkg.Report{rep}, nil)
	router := chi.NewRouter()
	h.mountV2(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("OpenAPI returned %d: %s", rec.Code, rec.Body.String())
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	props := spec.Components.Schemas["report_Events"].Properties
	for _, name := range []string{"From", "Until"} {
		t.Run(name, func(t *testing.T) {
			param := props[name]
			if param["type"] != "string" {
				t.Fatalf("datetime type = %#v", param)
			}
			description, _ := param["description"].(string)
			for _, text := range []string{
				"YYYY-MM-DDTHH:MM[:SS]", "YYYY-MM-DD", "server's local time zone",
				"without a time zone suffix", "midnight",
				"2026-09-28T12:30", "2026-09-28T12:30:41", "2026-09-28",
			} {
				if !strings.Contains(description, text) {
					t.Errorf("datetime description lacks %q: %q", text, description)
				}
			}
			if param["example"] != "2026-09-28T12:30:41" {
				t.Errorf("datetime example = %#v", param["example"])
			}
			for _, key := range []string{"format", "pattern", "oneOf", "anyOf", "enum"} {
				if _, exists := param[key]; exists {
					t.Errorf("datetime schema unexpectedly constrains clients with %s", key)
				}
			}
		})
	}
	if props["Day"]["type"] != "string" || props["Day"]["format"] != "date" {
		t.Fatalf("date schema = %#v", props["Day"])
	}
}
