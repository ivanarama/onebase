package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
)

func newNavSortServer(t *testing.T) *Server {
	t.Helper()
	s := newServerForFormMode(t)
	bundle, err := i18n.Load(os.DirFS(t.TempDir()), "")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Bundle = bundle

	entities := []*metadata.Entity{
		{Name: "ZCatalog", Kind: metadata.KindCatalog, Title: "Альфа", Titles: map[string]string{"en": "Zulu"}},
		{Name: "ACatalog", Kind: metadata.KindCatalog, Title: "Якорь", Titles: map[string]string{"en": "Alpha"}},
		{Name: "BTie", Kind: metadata.KindCatalog, Title: "Одинаково", Titles: map[string]string{"en": "Same"}},
		{Name: "ATie", Kind: metadata.KindCatalog, Title: "Одинаково", Titles: map[string]string{"en": "Same"}},
	}
	registers := []*metadata.Register{
		{Name: "ZRegister", Title: "Альфа-регистр", Titles: map[string]string{"en": "Zulu register"}},
		{Name: "ARegister", Title: "Якорь-регистр", Titles: map[string]string{"en": "Alpha register"}},
	}
	reports := []*report.Report{
		{Name: "ZReport", Title: "Альфа-отчёт", Titles: map[string]string{"en": "Zulu report"}, External: true},
		{Name: "AReport", Title: "Якорь-отчёт", Titles: map[string]string{"en": "Alpha report"}},
	}
	s.reg.Load(runtime.LoadOptions{Entities: entities, Registers: registers, Reports: reports})
	s.reg.LoadSubsystems([]*metadata.Subsystem{{
		Name:  "Sales",
		Title: "Продажи",
		Contents: metadata.SubsystemContents{
			Catalogs:  []string{"ZCatalog", "ACatalog", "BTie", "ATie"},
			Registers: []string{"ZRegister", "ARegister"},
			Reports:   []string{"ZReport", "AReport"},
		},
	}})
	return s
}

func renderIndexForNavSort(t *testing.T, s *Server, lang, subsystem string) string {
	t.Helper()
	s.cfg.Lang = lang
	path := "/ui/"
	if subsystem != "" {
		path += "?subsystem=" + subsystem
	}
	rec := httptest.NewRecorder()
	s.index(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func requireNavURLOrder(t *testing.T, body string, urls ...string) {
	t.Helper()
	previous := -1
	for _, url := range urls {
		marker := `href="` + url + `"`
		pos := strings.Index(body, marker)
		if pos < 0 {
			t.Fatalf("navigation does not contain %s", marker)
		}
		if pos <= previous {
			t.Fatalf("navigation URL %q is out of order in %v", url, urls)
		}
		previous = pos
	}
}

func TestIndex_SortsNavigationByVisibleLocalizedLabel(t *testing.T) {
	s := newNavSortServer(t)

	tests := []struct {
		name      string
		lang      string
		subsystem string
		urls      []string
	}{
		{
			name: "global Russian",
			lang: "ru",
			urls: []string{
				"/ui/catalog/ZCatalog",
				"/ui/catalog/ATie",
				"/ui/catalog/BTie",
				"/ui/catalog/ACatalog",
			},
		},
		{
			name: "global English",
			lang: "en",
			urls: []string{
				"/ui/catalog/ACatalog",
				"/ui/catalog/ATie",
				"/ui/catalog/BTie",
				"/ui/catalog/ZCatalog",
			},
		},
		{
			name:      "subsystem Russian",
			lang:      "ru",
			subsystem: "Sales",
			urls: []string{
				"/ui/catalog/ZCatalog?subsystem=Sales",
				"/ui/catalog/ATie?subsystem=Sales",
				"/ui/catalog/BTie?subsystem=Sales",
				"/ui/catalog/ACatalog?subsystem=Sales",
			},
		},
		{
			name:      "subsystem English",
			lang:      "en",
			subsystem: "Sales",
			urls: []string{
				"/ui/catalog/ACatalog?subsystem=Sales",
				"/ui/catalog/ATie?subsystem=Sales",
				"/ui/catalog/BTie?subsystem=Sales",
				"/ui/catalog/ZCatalog?subsystem=Sales",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := renderIndexForNavSort(t, s, tt.lang, tt.subsystem)
			requireNavURLOrder(t, body, tt.urls...)
		})
	}
}

func TestIndex_SortsNavigationByFinalLabelsWithSuffixes(t *testing.T) {
	s := newNavSortServer(t)
	body := renderIndexForNavSort(t, s, "ru", "")

	// Register movement/balance suffixes and the external-report suffix are part
	// of the final label, so sorting must happen after they are appended.
	requireNavURLOrder(t, body,
		"/ui/register/zregister",
		"/ui/register/zregister/balances",
		"/ui/register/aregister",
		"/ui/register/aregister/balances",
	)
	requireNavURLOrder(t, body,
		"/ui/report/zreport",
		"/ui/report/areport",
	)
}
