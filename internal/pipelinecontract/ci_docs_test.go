package pipelinecontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type docsCIWorkflow struct {
	Env map[string]string
	On  map[string]struct {
		Paths       []string `yaml:"paths"`
		PathsIgnore []string `yaml:"paths-ignore"`
		Types       []string `yaml:"types"`
	} `yaml:"on"`
	Jobs map[string]docsCIJob `yaml:"jobs"`
}

type docsCIJob struct {
	Env      map[string]string
	Name     string
	Needs    []string
	If       string
	RunsOn   string `yaml:"runs-on"`
	Outputs  map[string]string
	Services map[string]struct{ Image string }
	Strategy struct {
		FailFast *bool `yaml:"fail-fast"`
		Matrix   struct{ Shard []string }
	}
	Steps []struct {
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
		"Require every race shard": true, // A small result gate, not a test/workload bypass.
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
		if id == "build" {
			// Unlike the workers, the required aggregate must also reject cancellation.
			wantIf = "${{ always() }}"
		}
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
			if id == "build" && step.Name == "Check commit closing references" {
				if step.If != "${{ github.event_name == 'pull_request' }}" {
					t.Fatal("closing-reference gate must run for every PR, including docs-only")
				}
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
	if classify.Outputs["mode"] != "${{ steps.diff.outputs.mode }}" || len(classify.Steps) != 4 ||
		classify.Steps[0].Uses != "actions/checkout@v4" || classify.Steps[0].With["fetch-depth"] != "0" {
		t.Fatal("classifier must expose the actual diff result with exact commits available")
	}
	step := classify.Steps[1]
	if step.ID != "diff" || step.Env["BASE_SHA"] != "${{ github.event.pull_request.base.sha }}" ||
		step.Env["HEAD_SHA"] != "${{ github.event.pull_request.head.sha }}" || step.Env["EVENT_NAME"] != "${{ github.event_name }}" {
		t.Fatal("classification must use immutable event SHAs, not movable refs")
	}
}

func TestCIShardNodeRuntimeCoupled(t *testing.T) {
	w := docsWorkflow(t)
	exact := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	envRef := regexp.MustCompile(`^\$\{\{ env\.([A-Za-z_][A-Za-z0-9_]*) \}\}$`)
	producerVersion := ""
	for _, id := range []string{"unit-plan", "unit-race", "build"} {
		job := w.Jobs[id]
		version, condition, consumers := "", "", 0
		for _, step := range job.Steps {
			if step.Uses == "actions/setup-node@v4" {
				version, condition = step.With["node-version"], step.If
				if match := envRef.FindStringSubmatch(version); match != nil {
					version = w.Env[match[1]]
					for _, env := range []map[string]string{job.Env, step.Env} {
						if value, ok := env[match[1]]; ok {
							version = value
						}
					}
				}
				if !exact.MatchString(version) {
					t.Fatalf("%s requires an exact Node patch version, got %q", id, version)
				}
			}
			mode := map[string]string{"unit-plan": "plan", "unit-race": "run", "build": "merge"}[id]
			if !strings.Contains(step.Run, "test-coverage-shards.py "+mode) {
				continue
			}
			consumers++
			if version == "" || condition != "" && condition != step.If {
				t.Fatalf("%s can fingerprint Node before installing its runtime", id)
			}
			if id == "unit-plan" {
				producerVersion = version
			} else if version != producerVersion {
				t.Fatalf("%s uses Node %q, producer uses %q", id, version, producerVersion)
			}
		}
		if consumers != 1 {
			t.Fatalf("%s: expected one real plan consumer, got %d", id, consumers)
		}
	}
}

func TestCIShardDAGKeepsRequiredBuildFailClosed(t *testing.T) {
	w := docsWorkflow(t)
	full := "needs.classify.result != 'success' || needs.classify.outputs.mode != 'docs'"
	plan := w.Jobs["unit-plan"]
	race := w.Jobs["unit-race"]
	build := w.Jobs["build"]
	if plan.If != "${{ always() && !cancelled() && ("+full+") }}" ||
		!slices.Equal(plan.Needs, []string{"classify"}) ||
		plan.Outputs["plan-sha256"] != "${{ steps.plan.outputs.sha256 }}" {
		t.Fatal("planning must run on classifier failure and export its independent plan SHA")
	}
	if race.If != "${{ always() && !cancelled() && needs.unit-plan.result == 'success' && ("+full+") }}" ||
		!slices.Equal(race.Needs, []string{"classify", "unit-plan"}) ||
		race.Strategy.FailFast == nil || *race.Strategy.FailFast ||
		!slices.Equal(race.Strategy.Matrix.Shard, []string{"other", "ui-1", "ui-2", "ui-3", "ui-4", "launcher-1", "launcher-2"}) {
		t.Fatal("all seven original shards must run without fail-fast or docs/failure bypass")
	}
	if build.If != "${{ always() }}" || !slices.Equal(build.Needs, []string{"classify", "unit-plan", "unit-race"}) {
		t.Fatal("required build must observe failed, missing, skipped and cancelled shard jobs")
	}
	gateFound, mergeFound, runFound := false, false, false
	for _, step := range build.Steps {
		if step.Name == "Require every race shard" {
			gateFound = step.If == "" &&
				step.Env["DOCS"] == "${{ needs.classify.result == 'success' && needs.classify.outputs.mode == 'docs' }}" &&
				step.Env["PLAN_RESULT"] == "${{ needs.unit-plan.result }}" && step.Env["RACE_RESULT"] == "${{ needs.unit-race.result }}" &&
				strings.Contains(step.Run, `test "$PLAN_RESULT" = success && test "$RACE_RESULT" = success`) &&
				strings.Contains(step.Run, `test "$PLAN_RESULT" = skipped && test "$RACE_RESULT" = skipped`)
		}
		if step.Name == "Validate all journals and merge atomic coverage" {
			mergeFound = step.If == "${{ "+full+" }}" && step.Env["PLAN_SHA"] == "${{ needs.unit-plan.outputs.plan-sha256 }}" &&
				strings.Contains(step.Run, `--plan-sha256 "$PLAN_SHA"`) && strings.Contains(step.Run, "test-coverage-shards.py merge")
		}
	}
	for _, step := range race.Steps {
		if step.Name == "Run exact race shard" {
			runFound = step.Env["PLAN_SHA"] == "${{ needs.unit-plan.outputs.plan-sha256 }}" &&
				strings.Contains(step.Run, `--plan-sha256 "$PLAN_SHA"`) && strings.Contains(step.Run, "test-coverage-shards.py run")
		}
	}
	if !gateFound || !mergeFound || !runFound {
		t.Fatal("required build lost its explicit result gate, independent plan hash or full journal/profile validation")
	}
}

func TestCICommitClosingGate(t *testing.T) {
	w := docsWorkflow(t)
	for _, event := range []string{"opened", "synchronize", "reopened", "edited"} {
		if !slices.Contains(w.On["pull_request"].Types, event) {
			t.Errorf("closing-reference gate must recheck pull_request %s", event)
		}
	}
	var found, completeHistory bool
	for _, step := range w.Jobs["build"].Steps {
		if step.Uses == "actions/checkout@v4" {
			completeHistory = step.With["fetch-depth"] == "0"
		}
		if step.Name == "Check commit closing references" {
			found = step.If == "${{ github.event_name == 'pull_request' }}" &&
				step.Run == `go run ./tools/prclosecheck -event "$GITHUB_EVENT_PATH" -checkout-merge "$GITHUB_SHA"` && completeHistory
		}
	}
	if !found {
		t.Fatal("required build must check event data through prclosecheck with complete history")
	}
}

func TestReadyMetadataKeepsFullCIProofAndTrustedCodeBoundary(t *testing.T) {
	w := docsWorkflow(t)
	if slices.Contains(w.On["pull_request"].Types, "ready_for_review") {
		t.Fatal("Ready must validate existing full evidence before requesting another full run")
	}
	var metadata docsCIWorkflow
	if err := yaml.Unmarshal([]byte(repositoryFile(t, ".github", "workflows", "ci-metadata.yml")), &metadata); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(metadata.On["pull_request_target"].Types, []string{"ready_for_review"}) {
		t.Fatal("metadata trigger changed")
	}
	steps := metadata.Jobs["metadata"].Steps
	if len(steps) != 5 || steps[0].Uses != "actions/checkout@v4" ||
		steps[0].With["ref"] != "${{ github.event.pull_request.base.sha }}" ||
		steps[0].With["persist-credentials"] != "false" || steps[0].With["fetch-depth"] != "0" {
		t.Fatal("privileged event must only execute the immutable trusted base")
	}
	if steps[2].Env["HEAD_SHA"] != "${{ github.event.pull_request.head.sha }}" ||
		!strings.Contains(steps[2].Run, `test "$(git rev-parse FETCH_HEAD)" = "$HEAD_SHA"`) ||
		steps[3].Run != `go run ./tools/prclosecheck -event "$GITHUB_EVENT_PATH"` ||
		steps[4].Run != `python3 .github/scripts/ci-metadata.py --event "$GITHUB_EVENT_PATH"` {
		t.Fatal("Ready lost its actual metadata check or conservative full-CI fallback")
	}
	classify := w.Jobs["classify"].Steps
	if classify[2].Run != "python3 .github/scripts/ci-inputs.py" || classify[2].Env["CI_MODE"] != "${{ steps.diff.outputs.mode }}" ||
		classify[3].Uses != "actions/upload-artifact@v4" || classify[3].With["path"] != "ci-inputs.json" {
		t.Fatal("full CI must retain immutable input evidence for its actual attempt")
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
