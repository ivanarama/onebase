package userhelp_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/userhelp"
)

func write(t *testing.T, dir, file, body string) {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "docs/help/index.yaml", `title: Колл-центр
chapters:
  - id: operator
    title: Работа оператора
    file: operator.md
  - id: calls
    title: Звонки
    file: operator/calls.md
    parent: operator
    objects: [document/А_Звонок]
  - id: list
    title: Список звонков
    file: list.md
    forms: [document/А_Звонок/списка]
`)
	write(t, dir, "docs/help/operator.md", "# Оператор\n\n[Оформить звонок](operator/calls.md#call)\n")
	write(t, dir, "docs/help/operator/calls.md", "# Call\n\nСоздайте заявку.\n\n![Звонок](../images/call.png)\n\n[Назад](../operator.md)\n\n| Поле | Значение |\n|---|---|\n| Номер | 123 |\n")
	write(t, dir, "docs/help/list.md", "Список документов.")
	write(t, dir, "docs/help/images/call.png", "\x89PNG\r\n\x1a\nexample")
	return dir
}

func TestLoadRenderAndExport(t *testing.T) {
	dir := fixture(t)
	b := userhelp.Load(dir)
	if len(b.Warnings) > 0 || !b.Available() || !b.AutoUpdate || len(b.Chapters) != 3 {
		t.Fatalf("load: %+v", b)
	}
	if c := b.Context("document/а_звонок", "списка"); c == nil || c.ID != "list" {
		t.Fatalf("form binding: %+v", c)
	}
	if c := b.Context("document/А_Звонок", "объекта"); c == nil || c.ID != "calls" {
		t.Fatalf("object fallback: %+v", c)
	}
	if got := b.Search("СОЗДАЙТЕ заявку"); len(got) != 1 || got[0].ID != "calls" {
		t.Fatalf("search: %+v", got)
	}
	// A DB reload closes its temporary directory immediately after publication.
	if err := os.RemoveAll(filepath.Join(dir, "docs")); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := b.Export(out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "chapter-calls.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`src="assets/images/call.png"`, `href="chapter-operator.html"`, "<table>"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %s: %s", want, raw)
		}
	}
	raw, err = os.ReadFile(filepath.Join(out, "chapter-operator.html"))
	if err != nil || !strings.Contains(string(raw), `href="chapter-calls.html#call"`) {
		t.Fatalf("cross-chapter fragment: %s, %v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(out, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bundle userhelp.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Pages) != 3 || len(bundle.Assets) != 1 || bundle.Assets[0].Base64 != base64.StdEncoding.EncodeToString(b.Assets["images/call.png"].Data) {
		t.Fatalf("bundle lost content: %+v", bundle)
	}
}

func TestOptionalAndInvalidDocumentation(t *testing.T) {
	for _, tc := range []struct {
		name, index   string
		warn, enabled bool
	}{
		{"malformed", "chapters: [", true, true},
		{"unknown", "typo: true", true, true},
		{"multiple", "title: A\n---\ntitle: B", true, true},
		{"disabled", "enabled: false\nchapters: [{id: missing, title: Missing, file: missing.md}]", false, false},
		{"invalid-path", "chapters: [{id: x, title: X, file: ../../secret.md}]", true, true},
		{"parent-cycle", "chapters: [{id: x, title: X, file: x.md, parent: x}]", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "docs/help/index.yaml", tc.index)
			b := userhelp.Load(dir)
			if (len(b.Warnings) > 0) != tc.warn || b.Enabled != tc.enabled || b.Available() {
				t.Fatalf("unexpected book: %+v", b)
			}
		})
	}
	if b := userhelp.Load(t.TempDir()); b.Available() || len(b.Warnings) > 0 {
		t.Fatalf("missing book should be normal: %+v", b)
	}
}

func TestUnsafeContentAndLinks(t *testing.T) {
	dir := fixture(t)
	write(t, dir, "docs/help/list.md", "<script>alert(1)</script>\n\n[x](javascript:alert%281%29)\n\n![remote](https://host/x.png)\n\n![escape](../../secret.png)\n\n[missing](missing.md)\n\n[external](https://example.org/)\n")
	b := userhelp.Load(dir)
	if len(b.Warnings) < 3 {
		t.Fatalf("missing warnings: %+v", b.Warnings)
	}
	body, err := b.Render(b.Find("list"), func(id string) string { return id }, func(p string) string { return p })
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "javascript:", `src="https://`, "../../secret", `href="missing.md"`} {
		if strings.Contains(body, bad) {
			t.Fatalf("unsafe HTML %s: %s", bad, body)
		}
	}
	if !strings.Contains(body, `href="https://example.org/"`) {
		t.Fatal("external link lost")
	}
}

func TestSymlinkCannotReadOutsideProject(t *testing.T) {
	dir := fixture(t)
	outside := t.TempDir()
	write(t, outside, "private.md", "PRIVATE")
	if err := os.Remove(filepath.Join(dir, "docs/help/list.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(dir, "docs/help/list.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	b := userhelp.Load(dir)
	if b.Find("list") != nil || len(b.Warnings) == 0 {
		t.Fatalf("external symlink loaded: %+v", b)
	}
}
