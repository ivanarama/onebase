package pipelinecontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type docsCIWorkflow struct {
	On map[string]struct {
		Paths       []string `yaml:"paths"`
		PathsIgnore []string `yaml:"paths-ignore"`
	} `yaml:"on"`
	Jobs map[string]docsCIJob `yaml:"jobs"`
}

type docsCIJob struct {
	Name     string
	Needs    []string
	If       string
	RunsOn   string `yaml:"runs-on"`
	Outputs  map[string]string
	Services map[string]struct{ Image string }
	Steps    []struct {
		ID   string
		Name string
		Uses string
		If   string
		Run  string
		Env  map[string]string
		With map[string]string
	}
}

func docsWorkflow(t *testing.T) docsCIWorkflow {
	t.Helper()
	var w docsCIWorkflow
	if err := yaml.Unmarshal([]byte(repositoryFile(t, ".github", "workflows", "ci.yml")), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// Required statuses must be real jobs even on docs-only PRs. In particular,
// bypassing steps must also bypass Windows allocation and the PG container.
func TestCIDocsRequiredStatusesAndFallback(t *testing.T) {
	w := docsWorkflow(t)
	for event, trigger := range w.On {
		if len(trigger.Paths)+len(trigger.PathsIgnore) != 0 {
			t.Fatalf("%s filters the entire workflow and can leave required statuses pending", event)
		}
	}
	for _, event := range []string{"pull_request", "push", "workflow_dispatch"} {
		if _, ok := w.On[event]; !ok {
			t.Errorf("missing trigger %s", event)
		}
	}
	docs := "needs.classify.result == 'success' && needs.classify.outputs.mode == 'docs'"
	full := "needs.classify.result != 'success' || needs.classify.outputs.mode != 'docs'"
	lightBuild := map[string]bool{
		"actions/checkout@v4": true, "actions/setup-go@v5": true,
		"Check for UTF-8 BOM in .go files": true, "Plan numbers are unique": true,
	}
	for _, id := range append(branchProtectionContexts(t), "smoke_matrix", "bench") {
		job, ok := w.Jobs[id]
		if !ok || (job.Name != "" && job.Name != id && id != "smoke_matrix") {
			t.Fatalf("missing/renamed status %s", id)
		}
		if id == "smoke" {
			if !slices.Contains(job.Needs, "smoke_matrix") || job.If != "always()" ||
				len(job.Steps) != 1 || !strings.Contains(job.Steps[0].Run, `"$MATRIX_RESULT" != "success"`) {
				t.Fatal("smoke must reject a failed/skipped matrix, including on docs-only PRs")
			}
			continue
		}
		wantIf := "${{ always() && !cancelled() }}"
		if id == "bench" {
			wantIf = "${{ always() && !cancelled() && github.event_name == 'pull_request' }}"
		}
		if job.If != wantIf || !slices.Contains(job.Needs, "classify") {
			t.Fatalf("%s can skip on classifier failure instead of falling back to full CI", id)
		}
		lightFound := false
		for _, step := range job.Steps {
			if step.Name == "Docs-only success" {
				lightFound = step.If == "${{ "+docs+" }}" && step.Run != ""
				continue
			}
			if id == "build" && (lightBuild[step.Name] || lightBuild[step.Uses]) {
				if step.If != "" {
					t.Fatalf("docs validation %s should still execute", step.Name+step.Uses)
				}
				continue
			}
			if step.If != "${{ "+full+" }}" &&
				step.If != "${{ failure() && ("+full+") }}" &&
				step.If != "${{ always() && ("+full+") }}" {
				t.Fatalf("%s/%s runs heavy work for docs-only or suppresses the error fallback: %s", id, step.Name+step.Uses, step.If)
			}
		}
		if !lightFound {
			t.Fatalf("%s lacks a success step for a proven docs-only diff", id)
		}
	}
	for _, id := range []string{"test-windows", "launcher-webview-build", "smoke_matrix"} {
		fallback := "'windows-latest'"
		if id == "smoke_matrix" {
			fallback = "matrix.os"
		}
		if w.Jobs[id].RunsOn != "${{ "+docs+" && 'ubuntu-latest' || "+fallback+" }}" {
			t.Errorf("%s allocates Windows for docs-only or loses the original code runner", id)
		}
	}
	if w.Jobs["postgres-integration"].Services["postgres"].Image != "${{ ("+full+") && 'postgres:16' || '' }}" {
		t.Fatal("docs-only must not start PostgreSQL; a failed classifier must start it")
	}
	classify := w.Jobs["classify"]
	if classify.Outputs["mode"] != "${{ steps.diff.outputs.mode }}" || len(classify.Steps) != 2 ||
		classify.Steps[0].Uses != "actions/checkout@v4" || classify.Steps[0].With["fetch-depth"] != "0" {
		t.Fatal("classifier must expose the actual diff result with exact commits available")
	}
	step := classify.Steps[1]
	if step.ID != "diff" || step.Env["BASE_SHA"] != "${{ github.event.pull_request.base.sha }}" ||
		step.Env["HEAD_SHA"] != "${{ github.event.pull_request.head.sha }}" || step.Env["EVENT_NAME"] != "${{ github.event_name }}" {
		t.Fatal("classification must use immutable event SHAs, not movable refs")
	}
}

// Execute the production workflow entry point against real commits. Tests do
// not call a private predicate or replace git for successful classifications.
func TestCIDocsClassificationThroughWorkflow(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable; the workflow harness runs in Linux CI")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the classifier runs on Ubuntu; Windows bash paths differ")
	}
	workflowRun := docsWorkflow(t).Jobs["classify"].Steps[1].Run
	for _, tc := range []struct {
		name, event, want string
		files             map[string]string
		remove            string
		badSHA, moveBase  bool
	}{
		{name: "plan", files: map[string]string{"Plans/200-plan.md": "plan"}, want: "docs"},
		{name: "docs", files: map[string]string{"docs/guide/page.md": "docs"}, want: "docs"},
		{name: "root_markdown", files: map[string]string{"README.md": "new"}, want: "docs"},
		{name: "mixed", files: map[string]string{"docs/help.md": "help", "main.go": "new code"}, want: "full"},
		{name: "nested_markdown", files: map[string]string{"internal/README.md": "embedded?"}, want: "full"},
		{name: "workflow", files: map[string]string{".github/workflows/other.yml": "workflow"}, want: "full"},
		{name: "empty", want: "full"},
		{name: "push", event: "push", files: map[string]string{"docs/a.md": "docs"}, want: "full"},
		{name: "manual", event: "workflow_dispatch", files: map[string]string{"docs/a.md": "docs"}, want: "full"},
		{name: "missing_commit", badSHA: true, files: map[string]string{"docs/a.md": "docs"}, want: "full"},
		{name: "force_pushed_base", moveBase: true, files: map[string]string{"docs/a.md": "docs"}, want: "full"},
		{name: "code_renamed_into_docs", remove: "main.go", files: map[string]string{"docs/code.md": "code"}, want: "full"},
		{name: "docs_renamed_into_code", remove: "README.md", files: map[string]string{"app.go": "readme"}, want: "full"},
		{name: "deleted_code", remove: "main.go", want: "full"},
		{name: "deleted_plan", remove: "Plans/1-old.md", want: "docs"},
		{name: "spaces_and_newlines", files: map[string]string{"docs/two words\npage.md": "docs"}, want: "docs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...) //nolint:gosec // fixed tool, test-owned arguments.
				cmd.Dir = dir
				cmd.Env = workflowHarnessEnv(map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull})
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			write := func(name, body string) {
				t.Helper()
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			git("init", "-b", "main")
			git("config", "user.name", "CI test")
			git("config", "user.email", "ci@example.invalid")
			write("main.go", "code")
			write("README.md", "readme")
			write("Plans/1-old.md", "old")
			write(".github/scripts/classify-ci.sh", repositoryFile(t, ".github", "scripts", "classify-ci.sh"))
			git("add", ".")
			git("commit", "-m", "base")
			base := git("rev-parse", "HEAD")
			if tc.remove != "" {
				if err := os.Remove(filepath.Join(dir, tc.remove)); err != nil {
					t.Fatal(err)
				}
			}
			for p, body := range tc.files {
				write(p, body)
			}
			git("add", "-A")
			git("commit", "--allow-empty", "-m", "head")
			head := git("rev-parse", "HEAD")
			if tc.moveBase {
				git("checkout", "--detach", base)
				write("main.go", "new base code")
				git("add", ".")
				git("commit", "-m", "changed base after head")
				base = git("rev-parse", "HEAD")
				git("checkout", "--detach", head)
			}
			if tc.badSHA {
				base = strings.Repeat("a", 40)
			}
			event := tc.event
			if event == "" {
				event = "pull_request"
			}
			output := filepath.Join(t.TempDir(), "output")
			cmd := exec.Command("bash", "-c", workflowRun) //nolint:gosec // production entry point in an isolated repository.
			cmd.Dir = dir
			cmd.Env = workflowHarnessEnv(map[string]string{"EVENT_NAME": event, "BASE_SHA": base, "HEAD_SHA": head, "GITHUB_OUTPUT": output})
			logs, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("classifier failed: %v\n%s", err, logs)
			}
			got, err := os.ReadFile(output)
			if err != nil || string(got) != "mode="+tc.want+"\n" {
				t.Fatalf("classification = %q (%v), want %s\n%s", got, err, tc.want, logs)
			}
		})
	}
}

func TestCIDocsDiffErrorsFailClosedThroughWorkflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("classifier executes on Ubuntu")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	for _, tc := range []struct {
		name, diffCommand, base string
	}{
		{name: "failed_diff_with_partial_docs", diffCommand: `printf 'docs/a.md\0'; exit 1`},
		{name: "unterminated_name", diffCommand: `printf 'docs/a.md\0main.go'`},
		{name: "empty_name", diffCommand: `printf 'docs/a.md\0\0'`},
		{name: "empty_diff", diffCommand: `exit 0`},
		{name: "invalid_sha", base: "main; echo docs", diffCommand: `printf 'docs/a.md\0'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			scripts := filepath.Join(dir, ".github", "scripts")
			if err := os.MkdirAll(scripts, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(scripts, "classify-ci.sh"), []byte(repositoryFile(t, ".github", "scripts", "classify-ci.sh")), 0o600); err != nil {
				t.Fatal(err)
			}
			stub := "#!/usr/bin/env bash\ncase \"$1\" in\n cat-file) exit 0 ;;\n diff) " + tc.diffCommand + " ;;\n *) exit 99 ;;\nesac\n"
			if err := os.WriteFile(filepath.Join(dir, "git"), []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}
			base := tc.base
			if base == "" {
				base = strings.Repeat("a", 40)
			}
			output := filepath.Join(dir, "output")
			cmd := exec.Command(bash, "-c", docsWorkflow(t).Jobs["classify"].Steps[1].Run) //nolint:gosec // fixed production entry point, test-owned git stub.
			cmd.Dir = dir
			cmd.Env = workflowHarnessEnv(map[string]string{
				"PATH":       dir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"EVENT_NAME": "pull_request", "BASE_SHA": base, "HEAD_SHA": strings.Repeat("b", 40), "GITHUB_OUTPUT": output,
			})
			logs, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("classifier must fall back, not fail: %v\n%s", err, logs)
			}
			got, err := os.ReadFile(output)
			if err != nil || string(got) != "mode=full\n" {
				t.Fatalf("uncertain diff classified %q (%v), want full", got, err)
			}
		})
	}
}
