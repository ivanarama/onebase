package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNextSlicePublicCLI(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	binary := filepath.Join(t.TempDir(), "pipelinehealth")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./tools/pipelinehealth") //nolint:gosec // fixed Go command, test-owned binary path
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(binary, args...) //nolint:gosec // binary was built above; every argument is a test fixture
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("public CLI: %v\n%s", err, output)
		}
		return output
	}
	out := run("-slice-vectors", "internal/nextslice/testdata/golden-v1.json")
	var checked struct {
		Vectors       int  `json:"vectors_passed"`
		Authoritative bool `json:"authoritative"`
		Mutations     bool `json:"mutations_enabled"`
	}
	if err := json.Unmarshal(out, &checked); err != nil {
		t.Fatal(err)
	}
	if checked.Vectors < 60 || checked.Authoritative || checked.Mutations {
		t.Fatalf("golden result: %s", out)
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "tools/pipelinehealth/testdata/next-slice", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var baselineIssues []apiIssue
	if err := json.Unmarshal(read("legacy-1274-issues.json"), &baselineIssues); err != nil {
		t.Fatal(err)
	}
	var baselinePulls []apiPull
	if err := json.Unmarshal(read("legacy-1374-prs.json"), &baselinePulls); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		alter func(*apiIssue, *apiPull)
		want  string
		human int
	}{
		{name: "exact historical orphan", want: "legacy-bootstrap-required", human: 1},
		{name: "active PR", alter: func(_ *apiIssue, p *apiPull) { p.State = "open" }, human: 0},
		{name: "closed without merge", alter: func(_ *apiIssue, p *apiPull) { p.SliceEvidence.Merged = false }, want: "human", human: 1},
		{name: "missing merge event", alter: func(_ *apiIssue, p *apiPull) { p.SliceEvidence.MergeEvents = 0 }, want: "human", human: 1},
		{name: "duplicate merge event", alter: func(_ *apiIssue, p *apiPull) { p.SliceEvidence.MergeEvents = 2 }, want: "human", human: 1},
		{name: "hold stops recovery", alter: func(i *apiIssue, _ *apiPull) { i.Labels = append(i.Labels, apiLabel{Name: "hold"}) }, want: "human", human: 1},
		{name: "incomplete evidence", alter: func(_ *apiIssue, p *apiPull) { p.SliceEvidence.Complete = false }, want: "human", human: 1},
		{name: "closed issue remains closed", alter: func(i *apiIssue, _ *apiPull) { i.State = "closed" }, human: 0},
		{name: "untrusted lookup hint", alter: func(i *apiIssue, _ *apiPull) {
			for n := range i.Thread {
				if strings.Contains(i.Thread[n].Body, "pp:in-work") {
					i.Thread[n].User.Login = "attacker"
				}
			}
		}, want: "human", human: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var issues []apiIssue
			var pulls []apiPull
			if err := json.Unmarshal(read("legacy-1274-issues.json"), &issues); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(read("legacy-1374-prs.json"), &pulls); err != nil {
				t.Fatal(err)
			}
			if tc.alter != nil {
				tc.alter(&issues[0], &pulls[0])
			}
			dir := t.TempDir()
			ip := filepath.Join(dir, "issues.json")
			pp := filepath.Join(dir, "prs.json")
			for path, value := range map[string]any{ip: issues, pp: pulls} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out := run("-prs", pp, "-issues", ip, "-plan-dir", filepath.Join(root, "Plans"), "-json")
			var got report
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.NextSliceHumanWaiting) != tc.human {
				t.Fatalf("human=%+v, want %d\n%s", got.NextSliceHumanWaiting, tc.human, out)
			}
			if tc.human > 0 && got.NextSliceHumanWaiting[0].Stage != tc.want {
				t.Fatalf("wrong state: %s", out)
			}
			for _, candidate := range got.FixCandidates {
				if candidate.Number == 1274 {
					t.Fatalf("bootstrap emitted FIX authority: %s", out)
				}
			}
			if tc.name == "exact historical orphan" && !strings.Contains(string(out), "legacy bootstrap required") {
				t.Fatalf("lost legacy diagnosis: %s", out)
			}
		})
	}
}

func TestNextSliceReopenedPublicCLI(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	binary := filepath.Join(t.TempDir(), "pipelinehealth")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./tools/pipelinehealth") //nolint:gosec // fixed Go command and test-owned output
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build public CLI: %v\n%s", err, output)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan := gqlTestComment("10")
	plan["body"] = "<!-- pp:plan-ready issue=1274 pr=1329 path=Plans/159-undefined-and-typed-empty-values.md -->"
	active := gqlTestComment("11")
	active["body"] = "Взято в работу: #1374. <!-- pp:in-work -->"
	issue := gqlTestIssueNode("issue-1274", 1274, []any{plan, active})
	issue["labels"] = connection(2, []any{map[string]any{"name": "approved"}, map[string]any{"name": "in-work"}}, false, nil)
	initial := map[string]any{"data": snapshotResult(0, nil, false, nil, 1, []any{issue}, false, nil)}
	// The targeted query deliberately has no draft/base/labels/review fields.
	// It observes that the old closed PR reopened, not a complete Ready PR.
	reopened := map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"number": 1374, "title": "slice", "body": "Part of #1274", "url": "https://example.test/1374", "state": "OPEN", "headRefOid": headA, "merged": false, "mergeCommit": nil,
		"comments": emptyConnection(), "timelineItems": emptyConnection(),
	}}}}
	for _, tc := range []struct {
		name     string
		draft    bool
		base     string
		labels   []any
		history  bool
		reviewed bool
		churn    bool
	}{
		{name: "reopened draft", draft: true, base: "main"},
		{name: "reopened hold", base: "main", labels: []any{map[string]any{"name": "hold"}}},
		{name: "reopened other base", base: "release"},
		{name: "ready retains prior review depth", base: "main", history: true},
		{name: "reviewed stays waiting for ship", base: "main", reviewed: true, labels: []any{map[string]any{"name": "reviewed"}}},
		{name: "repeated reopen fails closed", churn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := []any{initial, reopened}
			wantRequests := "3"
			if tc.churn {
				pages = []any{initial, reopened, initial, reopened, initial, reopened}
				wantRequests = "6"
			} else {
				var comments []any
				if tc.history || tc.reviewed {
					comment := gqlTestComment("30")
					previousHead := headB
					if tc.reviewed {
						previousHead = headA
					}
					comment["body"] = completion(previousHead, 20, 25)
					comments = []any{comment}
				}
				full := gqlTestPullNode("pr-1374", 1374, headA, tc.labels, comments)
				full["body"], full["isDraft"], full["baseRefName"] = "Part of #1274", tc.draft, tc.base
				pages = append(pages, map[string]any{"data": snapshotResult(1, []any{full}, false, nil, 1, []any{issue}, false, nil)})
			}
			data, err := json.Marshal(pages)
			if err != nil {
				t.Fatal(err)
			}
			sequence := filepath.Join(t.TempDir(), "pages.json")
			if err := os.WriteFile(sequence, data, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, "-transport", "graphql", "-repo", "ivanarama/onebase", "-owner", "ivanarama", "-json") //nolint:gosec // fixed CLI and test-owned executable
			command.Dir = root
			command.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_SEQUENCE="+sequence, "GH_TOKEN=", "GITHUB_TOKEN=", "GH_ENTERPRISE_TOKEN=", "GITHUB_ENTERPRISE_TOKEN=")
			output, err := command.CombinedOutput()
			if tc.churn {
				if err == nil || !strings.Contains(string(output), "reopened") || json.Valid(output) {
					t.Fatalf("repeated churn returned a report instead of failing closed: err=%v output=%s", err, output)
				}
			} else {
				if err != nil {
					t.Fatalf("public CLI failed after complete refresh: %v\n%s", err, output)
				}
				var got report
				if err := json.Unmarshal(output, &got); err != nil {
					t.Fatal(err)
				}
				if got.Checked != 1 || got.IssuesChecked != 1 {
					t.Fatalf("complete refreshed snapshot was not used: %s", output)
				}
				if tc.history {
					for _, queue := range [][]candidate{got.ContentReviewCandidates, got.ReviewDispatchCandidates} {
						if len(queue) != 1 || queue[0].Number != 1374 || queue[0].Head != headA || queue[0].Depth != 1 {
							t.Fatalf("reopened Ready PR lost its normal identity or review history: %s", output)
						}
					}
				} else if len(got.ContentReviewCandidates) != 0 || len(got.ReviewDispatchCandidates) != 0 || len(got.ReviewCandidates) != 0 {
					t.Fatalf("incomplete OPEN observation bypassed draft/base/hold/review route: %s", output)
				}
				if tc.reviewed && (len(got.ReviewedWaitingShip) != 1 || got.ReviewedWaitingShip[0].Number != 1374) {
					t.Fatalf("current committed review was lost: %s", output)
				}
			}
			counter, err := os.ReadFile(sequence + ".index")
			if err != nil || string(counter) != wantRequests {
				t.Fatalf("full snapshot refresh/bounded retry not reached: requests=%q want=%s err=%v", counter, wantRequests, err)
			}
		})
	}
}
