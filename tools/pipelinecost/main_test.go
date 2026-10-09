package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Invoke the executable's real main, including flags, file/API collection,
// validation and exit status. Fake gh is another executable, not a mock of
// summarize: live tests exercise the production subprocess boundary.
func TestMain(m *testing.M) {
	if os.Getenv("PIPELINECOST_FAKE_GH") != "" && len(os.Args) > 1 && os.Args[1] == "api" {
		//nolint:gosec // G703: the parent test supplies a fixture directory from t.TempDir.
		data, err := os.ReadFile(filepath.Join(os.Getenv("PIPELINECOST_FAKE_GH"), "responses.json"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var responses map[string]json.RawMessage
		if err := json.Unmarshal(data, &responses); err != nil {
			os.Exit(1)
		}
		for endpoint, body := range responses {
			if endpoint == os.Args[2] || endpoint == "search" && strings.HasPrefix(os.Args[2], "search/issues?") {
				fmt.Println(string(body))
				os.Exit(0)
			}
		}
		fmt.Fprintln(os.Stderr, "unexpected endpoint:", os.Args[2])
		os.Exit(1)
	}
	if os.Getenv("PIPELINECOST_TEST_CLI") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"pipelinecost"}, os.Args[i+1:]...)
				main()
				os.Exit(0)
			}
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func cli(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	// G204: this test binary and fixture-owned arguments, without a shell.
	cmd := exec.Command(os.Args[0], append([]string{"--"}, args...)...) //nolint:gosec
	// The race detector otherwise sleeps a second in every CLI/gh subprocess.
	// Keep detection enabled; omit only its exit delay in these short-lived helpers.
	cmd.Env = append(os.Environ(), "PIPELINECOST_TEST_CLI=1", "GORACE=atexit_sleep_ms=0")
	return cmd.CombinedOutput()
}

func samplePull(n int, names ...string) pull {
	p := pull{Number: n, URL: fmt.Sprintf("https://github.com/ivanarama/onebase/pull/%d", n), CreatedAt: time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC), MergedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), ChangedFiles: len(names), Additions: 10, Deletions: 2}
	p.Base.Ref = "main"
	for _, name := range names {
		p.Files = append(p.Files, file{Name: name})
	}
	return p
}
func sampleSnapshot() snapshot {
	return snapshot{Version: 1, Repository: "ivanarama/onebase", Month: "2026-09", CollectedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Pulls: []pull{}}
}
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func offline(t *testing.T, s snapshot) ([]byte, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	writeJSON(t, path, s)
	return cli(t, "-month", "2026-09", "-input", path, "-json")
}
func readReport(t *testing.T, data []byte, err error) report {
	t.Helper()
	if err != nil {
		t.Fatalf("CLI: %v\n%s", err, data)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return r
}

func TestCLIMonthlyReport(t *testing.T) {
	s := sampleSnapshot()
	// Exactly 25% among four non-plan PR; plans never dilute that signal.
	s.Pulls = []pull{samplePull(5, "Plans/200-example.md"), samplePull(4, "internal/ui/handler.go"), samplePull(3, "docs/README.md"), samplePull(2, "internal/query/query.go"), samplePull(1, "tools/pipelinehealth/main.go")}
	s.Pulls[0].MergedAt = time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	data, err := offline(t, s)
	r := readReport(t, data, err)
	if r.Total != 5 || r.NonPlan != 4 || r.Pipeline != 1 || r.SharePercent == nil || *r.SharePercent != 25 || !r.Discuss || r.Buckets["plan"].Count != 1 {
		t.Fatalf("report: %+v", r)
	}
	if r.Items[0].Number != 1 || r.Items[0].LeadHours != 1 || r.Buckets["other"].Additions != 30 {
		t.Fatalf("items/buckets: %+v", r)
	}
}

func TestCLIScopeAndNoData(t *testing.T) {
	s := sampleSnapshot()
	labelled := samplePull(2, "CLAUDE.md")
	labelled.Labels = append(labelled.Labels, struct {
		Name string `json:"name"`
	}{Name: "area:pipeline"})
	renamed := samplePull(4, "archive/old-tool.go")
	renamed.Files[0].Previous = "tools/pipelinehealth/main.go"
	s.Pulls = []pull{samplePull(1, ".agents/skills/fix-approved/SKILL.md", "internal/ui/x.go"), labelled, samplePull(3, ".claude/skills/unrelated/SKILL.md"), renamed}
	data, err := offline(t, s)
	r := readReport(t, data, err)
	if r.Buckets["mixed"].Count != 2 || r.Buckets["pipeline"].Count != 1 || r.Items[1].Basis != "area:pipeline" {
		t.Fatalf("scope: %+v", r)
	}
	for _, pulls := range [][]pull{{}, {samplePull(1, "Plans/README.md")}} {
		s.Pulls = pulls
		data, err := offline(t, s)
		r := readReport(t, data, err)
		if r.SharePercent != nil || r.Discuss {
			t.Fatalf("zero denominator: %+v", r)
		}
	}
}

func TestCLIRejectsIncompleteSnapshot(t *testing.T) {
	cases := []struct {
		name   string
		change func(*snapshot)
	}{
		{"duplicate", func(s *snapshot) { s.Pulls = append(s.Pulls, s.Pulls[0]) }},
		{"other base", func(s *snapshot) { s.Pulls[0].Base.Ref = "release" }},
		{"next month", func(s *snapshot) { s.Pulls[0].MergedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }},
		{"previous month", func(s *snapshot) { s.Pulls[0].MergedAt = time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC) }},
		{"missing files", func(s *snapshot) { s.Pulls[0].ChangedFiles++ }},
		{"duplicate files", func(s *snapshot) {
			s.Pulls[0].ChangedFiles++
			s.Pulls[0].Files = append(s.Pulls[0].Files, s.Pulls[0].Files[0])
		}},
		{"missing created", func(s *snapshot) { s.Pulls[0].CreatedAt = time.Time{} }},
		{"future creation", func(s *snapshot) { s.Pulls[0].CreatedAt = s.Pulls[0].MergedAt.Add(time.Hour) }},
		{"missing pulls", func(s *snapshot) { s.Pulls = nil }},
		{"wrong month", func(s *snapshot) { s.Month = "2026-08" }},
		{"current snapshot", func(s *snapshot) { s.CollectedAt = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sampleSnapshot()
			s.Pulls = []pull{samplePull(1, "internal/ui/x.go")}
			tc.change(&s)
			data, err := offline(t, s)
			if err == nil {
				t.Fatalf("accepted invalid snapshot: %s", data)
			}
			if strings.Contains(string(data), `"discussion_signal"`) {
				t.Fatalf("partial report: %s", data)
			}
		})
	}
}

func fakeGH(t *testing.T, responses map[string]any) {
	t.Helper()
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "responses.json"), responses)
	//nolint:gosec // G703: the running test executable is copied as fake gh, no user input.
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	//nolint:gosec // G703: fixed gh filename within the test-owned temporary directory.
	if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PIPELINECOST_FAKE_GH", dir)
}

func TestCLILiveCollectionAndReplay(t *testing.T) {
	p := samplePull(1, "tools/pipelinehealth/main.go")
	files := p.Files
	p.Files = nil
	fakeGH(t, map[string]any{"search": map[string]any{"total_count": 1, "incomplete_results": false, "items": []map[string]int{{"number": 1}}}, "repos/ivanarama/onebase/pulls/1": p, "repos/ivanarama/onebase/pulls/1/files?per_page=100&page=1": files})
	path := filepath.Join(t.TempDir(), "snapshot.json")
	first, err := cli(t, "-month", "2026-09", "-snapshot", path, "-json")
	r := readReport(t, first, err)
	if r.Pipeline != 1 || !r.Discuss {
		t.Fatalf("live: %+v", r)
	}
	second, err := cli(t, "-month", "2026-09", "-input", path, "-json")
	if err != nil || string(first) != string(second) {
		t.Fatalf("replay differs: %v\n%s\n%s", err, first, second)
	}
}

func TestCLILiveRejectsTruncation(t *testing.T) {
	for _, result := range []map[string]any{
		{"total_count": 1, "incomplete_results": true, "items": []any{}},
		{"total_count": 1001, "incomplete_results": false, "items": []any{}},
		{"total_count": 2, "incomplete_results": false, "items": []map[string]int{{"number": 1}}},
		{"items": []any{}},
	} {
		t.Run(fmt.Sprint(result), func(t *testing.T) {
			fakeGH(t, map[string]any{"search": result})
			data, err := cli(t, "-month", "2026-09", "-json")
			if err == nil {
				t.Fatalf("accepted incomplete search: %s", data)
			}
		})
	}
}

func TestCLIPaginatesSearchAndFiles(t *testing.T) {
	query := url.QueryEscape("repo:ivanarama/onebase is:pr is:merged base:main merged:2026-09-01..2026-09-30")
	responses := map[string]any{}
	first := []map[string]int{}
	for n := 1; n <= 101; n++ {
		p := samplePull(n, "internal/ui/x.go")
		if n == 101 {
			p.ChangedFiles = 101
			p.Files = nil
			for i := 0; i < 100; i++ {
				p.Files = append(p.Files, file{Name: fmt.Sprintf("internal/ui/%d.go", i)})
			}
			responses[fmt.Sprintf("repos/ivanarama/onebase/pulls/%d/files?per_page=100&page=1", n)] = p.Files
			responses[fmt.Sprintf("repos/ivanarama/onebase/pulls/%d/files?per_page=100&page=2", n)] = []file{{Name: "tools/pipelinehealth/main.go"}}
		} else {
			first = append(first, map[string]int{"number": n})
			responses[fmt.Sprintf("repos/ivanarama/onebase/pulls/%d/files?per_page=100&page=1", n)] = p.Files
		}
		p.Files = nil
		responses[fmt.Sprintf("repos/ivanarama/onebase/pulls/%d", n)] = p
	}
	responses["search/issues?q="+query+"&per_page=100&page=1&sort=created&order=asc"] = map[string]any{"total_count": 101, "incomplete_results": false, "items": first}
	responses["search/issues?q="+query+"&per_page=100&page=2&sort=created&order=asc"] = map[string]any{"total_count": 101, "incomplete_results": false, "items": []map[string]int{{"number": 101}}}
	fakeGH(t, responses)
	data, err := cli(t, "-month", "2026-09", "-json")
	r := readReport(t, data, err)
	if r.Total != 101 || r.Buckets["mixed"].Count != 1 || r.Discuss {
		t.Fatalf("pagination: %+v", r)
	}
}
