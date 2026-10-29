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
