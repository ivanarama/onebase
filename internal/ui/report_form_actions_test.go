package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"golang.org/x/net/html"
)

func TestReportFormActionsThroughHTTP(t *testing.T) {
	s, _ := newSubmitTestServer(t, nil)
	s.reg.Load(runtime.LoadOptions{Reports: []*report.Report{
		{Name: "WithActions", Params: []report.Param{{Name: "From", Type: "date"}},
			FormActions: &report.FormActions{PrimaryBackground: "#1a4a80", Cancel: true}},
		{Name: "DefaultActions", Params: []report.Param{{Name: "From", Type: "date"}}},
	}})
	button := &metadata.FormElement{Name: "Generate", Kind: metadata.FormElementButton,
		Placement: "footer", Primary: true, Background: "#1a4a80",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Run"}}
	s.reg.LoadProcessors([]*processor.Processor{{Name: "WithFooter", Forms: []*metadata.FormModule{
		{LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{
			{Name: "Commands", Kind: metadata.FormElementGroupBox, Children: []*metadata.FormElement{button}},
		}},
	}}})

	router := chi.NewRouter()
	s.Mount(router)
	get := func(path string) *html.Node {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		doc, err := html.Parse(strings.NewReader(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	checkActionRow := func(submit *html.Node) {
		t.Helper()
		if submit == nil || semanticAttr(submit, "style") != "background:#1a4a80;" {
			t.Fatal("missing styled action")
		}
		for next := submit.NextSibling; next != nil; next = next.NextSibling {
			if next.Type != html.ElementNode {
				continue
			}
			if next.Data != "a" || semanticAttr(next, "class") != "btn btn-cancel" || semanticAttr(next, "data-ob-close-reason") != "close" {
				t.Fatalf("unexpected element beside primary action: %v", next.Attr)
			}
			return
		}
		t.Fatal("missing cancel beside primary action")
	}

	proc := get("/ui/processor/WithFooter")
	procButton := semanticFind(proc, "data-ob-fire-click", "Generate")
	checkActionRow(procButton)
	if group := semanticFind(proc, "data-ob-el", "Commands"); group == nil || semanticFind(group, "data-ob-fire-click", "Generate") != nil {
		t.Fatal("processor button must be moved out of its body group")
	}
	rep := get("/ui/report/WithActions")
	actions := semanticFind(rep, "class", "report-form-actions")
	if actions == nil {
		t.Fatal("missing report action row")
	}
	checkActionRow(semanticFind(actions, "type", "submit"))
	if semanticFind(get("/ui/report/DefaultActions"), "class", "report-form-actions") != nil {
		t.Fatal("report without form_actions changed its action row")
	}
}
