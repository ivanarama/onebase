package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/userhelp"
)

func execDocs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newDocsCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	cmd.SetContext(t.Context())
	err := cmd.Execute()
	return out.String(), err
}

func TestDocsCLIInitExportAndOptionalCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	runInitInto(t, dir, "")
	b := userhelp.Load(dir)
	if !b.Available() || !b.AutoUpdate {
		t.Fatalf("init did not create documentation: %+v", b)
	}
	guide, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(guide), "auto_update: false") {
		t.Fatalf("init missing agent instructions: %v", err)
	}
	article := filepath.Join(dir, "docs/help/introduction.md")
	if err := os.WriteFile(article, []byte("CUSTOM"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := execDocs(t, "init", "--project", dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(article)
	if err != nil || string(raw) != "CUSTOM" {
		t.Fatal("docs init overwrote an existing article")
	}
	if out, err := execDocs(t, "publish", "--project", dir); err != nil || !strings.Contains(out, "Plan only") {
		t.Fatalf("dry run requires external credentials: %s %v", out, err)
	}
	if _, err := execDocs(t, "export", "--project", dir, "--output", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := execDocs(t, "export", "--project", dir, "--output", filepath.Join(dir, "export")); err == nil {
		t.Fatal("export can overwrite source files")
	}
	if err := os.WriteFile(filepath.Join(dir, "docs/help/index.yaml"), []byte("chapters: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := execDocs(t, "check", "--project", dir, "--json"); err != nil || !strings.Contains(out, "warnings") {
		t.Fatalf("invalid help became a blocker: %s %v", out, err)
	}
	if out, err := runCheckCmd(t, runCheck, dir, nil); err != nil || !strings.Contains(out, "OK: ошибок не найдено") {
		t.Fatalf("configuration check blocked on docs: %s %v", out, err)
	}
}

func TestDocsCLIExportRejectsCaseCollisionsBeforeWriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	runInitInto(t, dir, "")
	for name, body := range map[string]string{
		"index.yaml": "chapters:\n" +
			"  - {id: intro, title: Intro, file: introduction.md}\n" +
			"  - {id: Topic, title: Upper, file: upper.md}\n" +
			"  - {id: topic, title: Lower, file: lower.md}\n",
		"upper.md": "# Upper\n\nUpper chapter content.\n",
		"lower.md": "# Lower\n\nLower chapter content.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "docs", "help", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, existing := range []bool{false, true} {
		name := "new-output"
		if existing {
			name = "existing-output"
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "export")
			const original = "previous chapter content"
			if existing {
				if err := os.Mkdir(out, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(out, "chapter-Topic.html"), []byte(original), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := execDocs(t, "export", "--project", dir, "--output", out)
			if err == nil || !strings.Contains(err.Error(), `"Topic"`) || !strings.Contains(err.Error(), `"topic"`) {
				t.Fatalf("expected an error identifying both colliding chapter IDs, got %v", err)
			}
			if !existing {
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatalf("failed export created its output directory: %v", err)
				}
				return
			}
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 1 {
				t.Fatalf("failed export changed output entries: %v, %v", entries, err)
			}
			raw, err := os.ReadFile(filepath.Join(out, "chapter-Topic.html"))
			if err != nil || string(raw) != original {
				t.Fatalf("failed export overwrote an existing chapter: %q, %v", raw, err)
			}
		})
	}
}
