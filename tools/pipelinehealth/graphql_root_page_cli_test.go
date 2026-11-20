package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The public command must request bounded root pages and still diagnose the
// final PR's completed review after both outer and nested cursor pagination.
func TestGraphQLCLIBoundedRootPagesKeepCompleteQueue(t *testing.T) {
	for _, counts := range []struct{ pulls, issues int }{{20, 20}, {21, 41}, {41, 21}} {
		t.Run(fmt.Sprintf("pulls=%d/issues=%d", counts.pulls, counts.issues), func(t *testing.T) {
			fixture := newRootPageFixture(t, counts.pulls, counts.issues, "")
			output, err := rootPageCLI(t, fixture.server.URL)
			if err != nil {
				t.Fatalf("bounded public snapshot failed: %v\n%s", err, output)
			}
			var got report
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatal(err)
			}
			if got.Checked != counts.pulls || got.IssuesChecked != counts.issues {
				t.Fatalf("outer pages lost queue members: pulls=%d/%d issues=%d/%d", got.Checked, counts.pulls, got.IssuesChecked, counts.issues)
			}
			if len(got.ReviewedWaitingShip) != 1 || got.ReviewedWaitingShip[0].Number != 1000+counts.pulls {
				t.Fatalf("last PR's paginated review was lost: %+v", got.ReviewedWaitingShip)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			wantPages := max((counts.pulls+19)/20, (counts.issues+19)/20)
			if fixture.roots != wantPages || fixture.nested != 1 {
				t.Fatalf("snapshot did not traverse every cursor exactly once: root=%d/%d nested=%d/1", fixture.roots, wantPages, fixture.nested)
			}
		})
	}
}

func TestGraphQLCLIBoundedRootPagesStillRejectQueueDrift(t *testing.T) {
	for _, test := range []struct {
		mode  string
		want  string
		reads int
	}{
		{"duplicate", "duplicate node id", 2},
		{"count-change", "totalCount changed", 6},
	} {
		t.Run(test.mode, func(t *testing.T) {
			fixture := newRootPageFixture(t, 41, 21, test.mode)
			output, err := rootPageCLI(t, fixture.server.URL)
			if err == nil || !strings.Contains(string(output), test.want) {
				t.Fatalf("incomplete/changing snapshot was not refused: %v\n%s", err, output)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.roots != test.reads || fixture.nested != 0 {
				t.Fatalf("invalid read escaped its existing retry boundary: root=%d/%d nested=%d", fixture.roots, test.reads, fixture.nested)
			}
		})
	}
}

type rootPageFixture struct {
	t             *testing.T
	server        *httptest.Server
	mu            sync.Mutex
	roots, nested int
	pulls, issues int
	mode          string
}

func newRootPageFixture(t *testing.T, pulls, issues int, mode string) *rootPageFixture {
	t.Helper()
	f := &rootPageFixture{t: t, pulls: pulls, issues: issues, mode: mode}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

var rootPageLimit = regexp.MustCompile(`(?s)(pullRequests|issues)\(\s*states:\s*OPEN\s*first:\s*([0-9]+)`)

func (f *rootPageFixture) serve(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string
		Variables map[string]any
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.t.Error(err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var data any
	switch {
	case strings.Contains(request.Query, "query PipelineHealthSnapshot"):
		f.roots++
		limits := map[string]int{}
		for _, match := range rootPageLimit.FindAllStringSubmatch(request.Query, -1) {
			n, _ := strconv.Atoi(match[2])
			limits[match[1]] = n
		}
		if len(limits) != 2 || limits["pullRequests"] < 1 || limits["issues"] < 1 || limits["pullRequests"] > 20 || limits["issues"] > 20 {
			f.t.Errorf("root read exceeds the fixture gateway's safe page size: %v", limits)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "root page is too large"}}})
			return
		}
		repo := map[string]any{}
		for _, kind := range []string{"pull", "issue"} {
			include := request.Variables["includePulls"]
			connectionName, total := "pullRequests", f.pulls
			if kind == "issue" {
				include, connectionName, total = request.Variables["includeIssues"], "issues", f.issues
			}
			if include != true {
				continue
			}
			start := 0
			if cursor := request.Variables[kind+"Cursor"]; cursor != nil {
				text, ok := cursor.(string)
				prefix := kind + ":"
				if !ok || !strings.HasPrefix(text, prefix) {
					f.t.Errorf("invalid %s continuation cursor: %v", kind, cursor)
					return
				}
				var err error
				start, err = strconv.Atoi(strings.TrimPrefix(text, prefix))
				if err != nil || start <= 0 || start >= total {
					f.t.Errorf("out-of-range %s cursor: %v", kind, cursor)
					return
				}
			}
			end := min(start+limits[connectionName], total)
			nodes := []any{}
			for index := start; index < end; index++ {
				if kind == "pull" {
					pull := gqlTestPullNode(fmt.Sprintf("PR_%d", index), 1001+index, headA, nil, nil)
					if index == total-1 {
						first := gqlTestComment("900001")
						first["body"] = reviewConclusion(headA, "reviewed")
						pull["labels"] = connection(1, []any{map[string]any{"name": "reviewed"}}, false, nil)
						pull["comments"] = connection(3, []any{first}, true, "last-pr-comments")
					}
					if f.mode == "duplicate" && index == 20 {
						pull["id"] = "PR_0"
					}
					nodes = append(nodes, pull)
				} else {
					nodes = append(nodes, gqlTestIssueNode(fmt.Sprintf("ISSUE_%d", index), 2001+index, nil))
				}
			}
			count := total
			if f.mode == "count-change" && kind == "pull" && start > 0 {
				count++
			}
			var cursor any
			if end < total {
				cursor = fmt.Sprintf("%s:%d", kind, end)
			}
			repo[connectionName] = connection(count, nodes, end < total, cursor)
		}
		data = map[string]any{"repository": repo}
	case strings.Contains(request.Query, "query PipelineHealthNodePage"):
		f.nested++
		if request.Variables["id"] != fmt.Sprintf("PR_%d", f.pulls-1) || request.Variables["commentCursor"] != "last-pr-comments" || request.Variables["includeComments"] != true || request.Variables["includeLabels"] != false {
			f.t.Errorf("wrong nested continuation: %+v", request.Variables)
			return
		}
		claim, done := gqlTestComment("900002"), gqlTestComment("900003")
		claim["body"], done["body"] = claimFor(headA, 900001, epoch), completion(headA, 900001, 900002)
		data = map[string]any{"node": map[string]any{"__typename": "PullRequest", "comments": connection(3, []any{claim, done}, false, nil)}}
	default:
		f.t.Errorf("unexpected public GraphQL operation: %s", request.Query)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func rootPageCLI(t *testing.T, endpoint string) ([]byte, error) {
	t.Helper()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	binary := pipelinehealthCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	//nolint:gosec // Fixed CLI and test-owned executable, loopback server and paths.
	command := exec.CommandContext(ctx, binary, "-transport", "graphql", "-json", "-contract", filepath.Join(root, ".claude", "skills", "review-queue", "SKILL.md"))
	command.Dir = root
	command.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_ROUTING_SERVER="+endpoint)
	return command.CombinedOutput()
}
