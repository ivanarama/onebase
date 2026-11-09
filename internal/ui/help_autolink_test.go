package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/userhelp"
)

func TestHelpAutolinkAllowlistPublicRoute(t *testing.T) {
	base := newServerForFormMode(t)
	s := New(base.reg, base.store, interpreter.New(), nil, Config{}, nil)
	defer s.BeginShutdown()
	dir := t.TempDir()
	helpDir := filepath.Join(dir, "docs", "help")
	if err := os.MkdirAll(helpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(helpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for name, text := range map[string]string{
		"index.yaml": "title: Help\nchapters:\n  - id: links\n    title: Links\n    file: links.md\n",
		"links.md": "<ms-settings:privacy><search-ms:query=x><https://example.org/after>\n\n" +
			"<mailto:reader@example.org> <reader@example.org>\n\n" +
			"[ordinary blocked](ms-settings:privacy) [ordinary allowed](http://example.org/plain)",
	} {
		if err := root.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	book := userhelp.Load(dir)
	if len(book.Warnings) != 0 {
		t.Fatal(book.Warnings)
	}
	s.reg.LoadHelp(book)
	router := chi.NewRouter()
	s.Mount(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ui/help/chapter/links", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("chapter: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if regexp.MustCompile(`(?i)href=["'](?:ms-settings|search-ms):`).MatchString(body) {
		t.Fatalf("help rendered a non-allowlisted autolink: %s", body)
	}
	for _, want := range []string{"ms-settings:privacy", "search-ms:query=x",
		`href="https://example.org/after"`, `href="mailto:reader@example.org"`, `href="http://example.org/plain"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing text/link %q: %s", want, body)
		}
	}
}
