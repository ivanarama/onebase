package webassets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMonacoHandlerServesCriticalFiles guards the offline self-hosting: every
// file the templates load from /vendor/monaco/ must be embedded and served.
func TestMonacoHandlerServesCriticalFiles(t *testing.T) {
	h := http.StripPrefix("/vendor/monaco/", MonacoHandler())
	files := []string{
		"vs/loader.js",
		"vs/editor/editor.main.js",
		"vs/editor/editor.main.css",
		"vs/base/worker/workerMain.js",
		"vs/base/browser/ui/codicons/codicon/codicon.ttf",
		"vs/basic-languages/yaml/yaml.js",
	}
	for _, f := range files {
		req := httptest.NewRequest(http.MethodGet, "/vendor/monaco/"+f, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", f, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", f)
		}
	}

	// A path outside the embedded tree must 404, not leak the filesystem.
	req := httptest.NewRequest(http.MethodGet, "/vendor/monaco/vs/language/typescript/tsMode.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("absent file: status = %d, want 404", rec.Code)
	}

	// Cache header must be set for long-lived versioned assets.
	req = httptest.NewRequest(http.MethodGet, "/vendor/monaco/vs/loader.js", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want max-age", cc)
	}
}
