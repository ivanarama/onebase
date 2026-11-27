package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"
)

// Follow the script URL in the real form: reading a JS file from disk does not
// prove that an embedded application actually serves it to the browser.
func TestReportPeriodVisibilityAssetHTTP(t *testing.T) {
	s := newReportExportTestServer(t, periodReport(t))
	router := chi.NewRouter()
	s.MountStatic(router)
	s.Mount(router)
	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/ui/report/Periods", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("report form status = %d", page.Code)
	}
	doc, err := html.Parse(strings.NewReader(page.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var src string
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "src" && strings.Contains(attr.Val, "report_params.js") {
					src = attr.Val
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(doc)
	if src == "" {
		t.Fatal("report form does not load parameter visibility script")
	}
	asset := httptest.NewRecorder()
	router.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, src, nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("report script %s status = %d", src, asset.Code)
	}
	if !strings.Contains(asset.Header().Get("Content-Type"), "application/javascript") ||
		!strings.Contains(asset.Body.String(), "obReportParamVisibilityInstalled") ||
		!strings.Contains(asset.Body.String(), "data-report-param-visible") {
		t.Fatal("report script URL did not return visibility JavaScript")
	}
	etag := asset.Header().Get("ETag")
	if etag == "" || asset.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("report script must revalidate after an engine update")
	}
	req := httptest.NewRequest(http.MethodGet, src, nil)
	req.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	router.ServeHTTP(cached, req)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatal("matching report script ETag must return 304 without a body")
	}
	req = httptest.NewRequest(http.MethodGet, src, nil)
	req.Header.Set("If-None-Match", `"old-engine"`)
	stale := httptest.NewRecorder()
	router.ServeHTTP(stale, req)
	if stale.Code != http.StatusOK || stale.Body.String() != asset.Body.String() {
		t.Fatal("stale report script ETag must return the current script")
	}
}
