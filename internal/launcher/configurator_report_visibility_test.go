package launcher

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/report"
	"gopkg.in/yaml.v3"
)

func TestConfiguratorReportVisibilityRoundTripHTTP(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	path := writeCfgFile(t, cfgDir, "reports", "Periods.yaml", `name: Periods
params:
  - {name: ByHours, type: bool}
  - name: EndTime
    type: datetime
    default: "{{today | end_of_day}}"
    visible_when: {param: ByHours, equals: true}
query: ВЫБРАТЬ 1 КАК N
`)
	router := chi.NewRouter()
	router.Get("/bases/{id}/configurator", h.configuratorPage)
	router.Post("/bases/{id}/configurator/report", h.configuratorSaveReport)
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/bases/test/configurator?tab=tree", nil))
	if get.Code != 200 {
		t.Fatalf("GET: %d", get.Code)
	}
	form := browserSubmit(t, get.Body.String(), "/configurator/report")
	form.Set("title", "Period title")
	post := postCfg(t, "test", "/bases/test/configurator/report", form, h.configuratorSaveReport)
	if ok, message := cfgResponse(t, post); !ok {
		t.Fatal(message)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved report.Report
	if err := yaml.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Params) != 2 || saved.Params[1].VisibleWhen == nil || saved.Params[1].VisibleWhen.Equals == nil || !*saved.Params[1].VisibleWhen.Equals || saved.Params[1].Default != "{{today | end_of_day}}" {
		t.Fatalf("visibility/default lost: %+v", saved.Params)
	}
}
