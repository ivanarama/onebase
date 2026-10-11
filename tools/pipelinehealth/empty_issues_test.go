package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Redirect the REST CLI subprocess to the test server without changing the
// production client or contacting GitHub.
type restCLITestTransport struct {
	endpoint  *url.URL
	transport http.RoundTripper
}

func (transport restCLITestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	redirected := request.Clone(request.Context())
	redirected.URL.Scheme = transport.endpoint.Scheme
	redirected.URL.Host = transport.endpoint.Host
	return transport.transport.RoundTrip(redirected)
}

func requireEmptyIssueDiagnostics(t *testing.T, output []byte) {
	t.Helper()
	var result report
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode report: %v\n%s", err, output)
	}
	if result.State != "yellow" || result.IssuesChecked != 3 {
		t.Fatalf("unexpected report: %+v", result)
	}
	want := map[int]string{20: "issue_route_conflict", 21: "manual_route_conflict"}
	for _, item := range result.Findings {
		if expected, ok := want[item.Issue]; ok && item.Code == expected && item.Severity == "yellow" {
			delete(want, item.Issue)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing zero-comment diagnostics: %v; report=%+v", want, result)
	}
	if len(result.FixCandidates) != 1 || result.FixCandidates[0].Number != 22 {
		t.Fatalf("ordinary issue lost FIX eligibility: %+v", result.FixCandidates)
	}
}

func TestRESTCLIDiagnosesIssuesWithoutComments(t *testing.T) {
	var threadRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !requireTestAuthentication(response, request) {
			return
		}
		switch request.URL.Path {
		case "/repos/ivanarama/onebase/pulls":
			_, _ = io.WriteString(response, `[]`)
		case "/repos/ivanarama/onebase/issues":
			switch request.URL.Query().Get("page") {
			case "1":
				page := []apiIssue{
					{Number: 20, State: "open", Labels: []apiLabel{{Name: "ready-fix"}, {Name: "needs-decision"}}},
					{Number: 21, State: "open", Labels: []apiLabel{{Name: "manual"}, {Name: "approved"}}},
				}
				for number := 100; number < 198; number++ {
					page = append(page, apiIssue{Number: number, State: "open", CommentCount: 1, PullRequest: map[string]any{}, Labels: []apiLabel{{Name: "manual"}, {Name: "approved"}}})
				}
				_ = json.NewEncoder(response).Encode(page)
			case "2":
				_, _ = io.WriteString(response, `[{"number":22,"state":"open","comments":1,"labels":[{"name":"ready-fix"}]}]`)
			default:
				http.Error(response, "unexpected page", http.StatusBadRequest)
			}
		case "/repos/ivanarama/onebase/issues/22/comments":
			threadRequests.Add(1)
			_, _ = io.WriteString(response, `[{"id":1,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z","user":{"login":"ivanarama"},"body":"Plan\n<!-- pp:triage -->"}]`)
		default:
			http.Error(response, "unexpected request: "+request.URL.Path, http.StatusBadRequest)
		}
	}))
	defer server.Close()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // executable is this test binary; endpoint and cache are test-owned.
	command := exec.Command(helper, "-test.run=^$")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command.Dir = root
	command.Env = append(os.Environ(), "PIPELINEHEALTH_TEST_REST_CLI="+server.URL, "GH_HOST=github.com", "GH_TOKEN=test-token", "PIPELINEHEALTH_CACHE_DIR="+t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("REST CLI failed: %v\n%s", err, output)
	}
	requireEmptyIssueDiagnostics(t, output)
	if threadRequests.Load() != 1 {
		t.Fatalf("ordinary thread requests = %d, want 1", threadRequests.Load())
	}
}

func TestGraphQLCLIDiagnosesIssuesWithoutComments(t *testing.T) {
	one := gqlTestIssueNode("one", 20, nil)
	one["labels"] = connection(2, []any{map[string]any{"name": "ready-fix"}, map[string]any{"name": "needs-decision"}}, false, nil)
	two := gqlTestIssueNode("two", 21, nil)
	two["labels"] = connection(2, []any{map[string]any{"name": "manual"}, map[string]any{"name": "approved"}}, false, nil)
	comment := gqlTestComment("1")
	comment["body"] = "Plan\n<!-- pp:triage -->"
	ordinary := gqlTestIssueNode("three", 22, []any{comment})
	ordinary["labels"] = connection(1, []any{map[string]any{"name": "ready-fix"}}, false, nil)
	pages := []any{
		map[string]any{"data": snapshotResult(0, nil, false, nil, 3, []any{one, two}, true, "next")},
		map[string]any{"data": map[string]any{"repository": map[string]any{"issues": connection(3, []any{ordinary}, false, nil)}}},
	}
	data, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	sequence := filepath.Join(directory, "pages.json")
	if err := os.WriteFile(sequence, data, 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // executable is Go; paths are controlled by this test.
	command := exec.Command("go", "run", "./tools/pipelinehealth", "-transport", "graphql", "-json")
	command.Dir = root
	command.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_SEQUENCE="+sequence)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("GraphQL CLI failed: %v\n%s", err, output)
	}
	requireEmptyIssueDiagnostics(t, output)
	counter, err := os.ReadFile(sequence + ".index")
	if err != nil || string(counter) != "2" {
		t.Fatalf("unexpected GraphQL requests: %q err=%v", counter, err)
	}
}
