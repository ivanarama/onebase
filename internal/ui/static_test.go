package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestStaticUIJS(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	req := httptest.NewRequest(http.MethodGet, "/static/ui.js", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("/static/ui.js status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("/static/ui.js content-type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"window.obOpenInShell",
		"openRefPicker",
		"function obImageUpload",
		"function addTpRow",
		"function obInitFormDelegates",
		"data-ob-popup-cancel",
		"data-ob-add-tp-row",
		"data-ob-image-upload",
		"data-ob-ref-current",
		"function openItemPicker",
		"function obInitRichText",
		"function listMenuItems",
		"function obInitListDelegates",
		"data-ob-list-actions",
		"data-ob-list-row",
		"data-ob-auto-submit",
		"data-ob-nav-toggle",
		"data-ob-toggle-target",
		"function obInitFeed",
		"function toggleTreeNode",
		"obInitMappedCharts",
		"window.rsBeforeSubmit",
		"function obInitReportDelegates",
		"function obInitJournalDelegates",
		"function jlCollect",
		"data-ob-journal-open-url",
		"data-ob-rs-before-submit",
		"data-ob-report-variant-submit",
		"data-ob-attachments",
		"window.onebaseDevice",
		"onebase:звонок.входящий",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/static/ui.js не содержит %q", want)
		}
	}
}

func TestStaticManagedJS(t *testing.T) {
	r := chi.NewRouter()
	mountStatic(r)

	req := httptest.NewRequest(http.MethodGet, "/static/managed.js", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("/static/managed.js status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/javascript") {
		t.Fatalf("/static/managed.js content-type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"function obManagedConfig",
		"window.obFire",
		"function addVtRow",
		"window.obGridSync",
		"function gridCellEventParams",
		"function obManagedInitDelegates",
		"data-ob-fire-click",
		"data-ob-add-tp",
		"obManagedSwitchTab",
		"ПриОткрытии",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/static/managed.js не содержит %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Error("/static/managed.js содержит Go-template маркеры")
	}
}
