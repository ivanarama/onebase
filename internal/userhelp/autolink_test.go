package userhelp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/userhelp"
)

const autolinkFixture = `
<ms-settings:privacy><search-ms:query=x><custom+app:open?x=1&y=2>

<tel:+123> <ftp://example.org/file> <file:///tmp/example> <data:text/plain,example>

<http://example.org/plain> <https://example.org/ok?q=1&x=2>
<HTTPS://example.org/case> <mailto:reader@example.org> <reader@example.org>

[ordinary blocked](ms-settings:privacy) [ordinary allowed](https://example.org/ordinary)
`

func assertAutolinkPolicy(t *testing.T, body string) {
	t.Helper()
	if regexp.MustCompile(`(?i)href=["'](?:ms-settings|search-ms|custom\+app|tel|ftp|file|data):`).MatchString(body) {
		t.Fatalf("non-allowlisted scheme remains clickable: %s", body)
	}
	for _, want := range []string{"ms-settings:privacy", "search-ms:query=x", "custom+app:open?x=1&amp;y=2", "tel:+123"} {
		if !strings.Contains(body, want) {
			t.Errorf("plain text %q was lost: %s", want, body)
		}
	}
	for _, target := range []string{"http://example.org/plain", "https://example.org/ok?q=1&amp;x=2",
		"HTTPS://example.org/case", "mailto:reader@example.org", "https://example.org/ordinary"} {
		if !strings.Contains(body, `href="`+target+`"`) {
			t.Errorf("allowed link %q was lost: %s", target, body)
		}
	}
}

func TestHelpExportAndBundleUseAutolinkAllowlist(t *testing.T) {
	dir := fixture(t)
	write(t, dir, "docs/help/operator.md", autolinkFixture)
	b := userhelp.Load(dir)
	if len(b.Warnings) != 0 {
		t.Fatal(b.Warnings)
	}
	out := t.TempDir()
	if err := b.Export(out); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "chapter-operator.html"))
	if err != nil {
		t.Fatal(err)
	}
	assertAutolinkPolicy(t, string(body))
	raw, err := os.ReadFile(filepath.Join(out, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bundle userhelp.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, page := range bundle.Pages {
		if page.ID == "operator" {
			assertAutolinkPolicy(t, page.HTML)
			found = true
		}
	}
	if !found {
		t.Fatal("exported bundle lost the operator chapter")
	}
}
