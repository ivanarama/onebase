package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMain(m *testing.M) {
	if os.Getenv("PIPELINEHEALTH_TEST_GH_HELPER") == "1" {
		_, _ = io.WriteString(os.Stdout, os.Getenv("PIPELINEHEALTH_TEST_GH_RESPONSE"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type graphQLCall struct {
	query     string
	variables map[string]any
}

type scriptedGraphQLClient struct {
	t       *testing.T
	results []any
	errors  []error
	calls   []graphQLCall
}

func (client *scriptedGraphQLClient) Query(query string, variables map[string]any, destination any) error {
	client.t.Helper()
	index := len(client.calls)
	client.calls = append(client.calls, graphQLCall{query: query, variables: variables})
	if index < len(client.errors) && client.errors[index] != nil {
		return client.errors[index]
	}
	if index >= len(client.results) {
		client.t.Fatalf("unexpected GraphQL call %d", index+1)
	}
	data, err := json.Marshal(client.results[index])
	if err != nil {
		client.t.Fatalf("marshal scripted GraphQL response: %v", err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		client.t.Fatalf("decode scripted GraphQL response: %v", err)
	}
	return nil
}

func emptyConnection() map[string]any {
	return map[string]any{
		"totalCount": 0,
		"nodes":      []any{},
		"pageInfo":   map[string]any{"hasNextPage": false, "endCursor": nil},
	}
}

func connection(total int, nodes []any, next bool, cursor any) map[string]any {
	if nodes == nil {
		nodes = []any{}
	}
	return map[string]any{
		"totalCount": total,
		"nodes":      nodes,
		"pageInfo":   map[string]any{"hasNextPage": next, "endCursor": cursor},
	}
}

func gqlTestComment(id string) map[string]any {
	return map[string]any{
		"fullDatabaseId": id,
		"createdAt":      "2026-09-01T10:00:00Z",
		"updatedAt":      "2026-09-01T10:00:00Z",
		"author":         map[string]any{"login": "ivanarama"},
		"body":           "comment " + id,
	}
}

func gqlTestPullNode(nodeID string, number int, head string, labels []any, comments []any) map[string]any {
	return map[string]any{
		"id":          nodeID,
		"number":      number,
		"title":       "PR title",
		"body":        "Fixes #1",
		"url":         "https://example.test/pr",
		"createdAt":   "2026-09-01T00:00:00Z",
		"updatedAt":   "2026-09-02T00:00:00Z",
		"state":       "OPEN",
		"isDraft":     false,
		"headRefOid":  head,
		"baseRefName": "main",
		"labels":      connection(len(labels), labels, false, nil),
		"comments":    connection(len(comments), comments, false, nil),
		"commits": map[string]any{"nodes": []any{
			map[string]any{"commit": map[string]any{"id": "commit-" + nodeID, "oid": head}},
		}},
	}
}

func gqlTestIssueNode(nodeID string, number int, comments []any) map[string]any {
	return map[string]any{
		"id":        nodeID,
		"number":    number,
		"title":     "Issue title",
		"url":       "https://example.test/issue",
		"createdAt": "2026-09-01T00:00:00Z",
		"updatedAt": "2026-09-02T00:00:00Z",
		"state":     "OPEN",
		"labels":    connection(1, []any{map[string]any{"name": "approved"}}, false, nil),
		"comments":  connection(len(comments), comments, false, nil),
	}
}

func snapshotResult(pullTotal int, pulls []any, pullNext bool, pullCursor any, issueTotal int, issues []any, issueNext bool, issueCursor any) map[string]any {
	return map[string]any{"repository": map[string]any{
		"pullRequests": connection(pullTotal, pulls, pullNext, pullCursor),
		"issues":       connection(issueTotal, issues, issueNext, issueCursor),
	}}
}

func rawGraphQLClient(t *testing.T, data any) *ghPipelineGraphQLClient {
	t.Helper()
	response, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatalf("marshal raw GraphQL response: %v", err)
	}
	return &ghPipelineGraphQLClient{
		executable: "unused",
		run: func(_ context.Context, _ string, _ []byte, stdout, _ io.Writer) error {
			_, err := stdout.Write(response)
			return err
		},
	}
}

func TestGraphQLProductionIngressRejectsIncompleteSnapshots(t *testing.T) {
	for _, test := range []struct {
		name string
		data func() map[string]any
		want string
	}{
		{
			name: "empty pull request connection object",
			data: func() map[string]any {
				return map[string]any{"repository": map[string]any{
					"pullRequests": map[string]any{},
					"issues":       emptyConnection(),
				}}
			},
			want: `field "totalCount" is missing`,
		},
		{
			name: "empty issue connection object",
			data: func() map[string]any {
				return map[string]any{"repository": map[string]any{
					"pullRequests": emptyConnection(),
					"issues":       map[string]any{},
				}}
			},
			want: `field "totalCount" is missing`,
		},
		{
			name: "null labels connection",
			data: func() map[string]any {
				pull := gqlTestPullNode("pr-1", 10, headA, nil, nil)
				pull["labels"] = nil
				return snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
			},
			want: `field "labels" is null`,
		},
		{
			name: "null comments connection",
			data: func() map[string]any {
				pull := gqlTestPullNode("pr-1", 10, headA, nil, nil)
				pull["comments"] = nil
				return snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
			},
			want: `field "comments" is null`,
		},
		{
			name: "missing false boolean",
			data: func() map[string]any {
				pull := gqlTestPullNode("pr-1", 10, headA, nil, nil)
				delete(pull, "isDraft")
				return snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
			},
			want: `field "isDraft" is missing`,
		},
		{
			name: "missing page info member",
			data: func() map[string]any {
				pulls := connection(0, nil, false, nil)
				pulls["pageInfo"] = map[string]any{"hasNextPage": false}
				return map[string]any{"repository": map[string]any{
					"pullRequests": pulls,
					"issues":       emptyConnection(),
				}}
			},
			want: `field "endCursor" is missing`,
		},
		{
			name: "null nodes in empty connection",
			data: func() map[string]any {
				pulls := emptyConnection()
				pulls["nodes"] = nil
				return map[string]any{"repository": map[string]any{
					"pullRequests": pulls,
					"issues":       emptyConnection(),
				}}
			},
			want: `field "nodes" is null`,
		},
		{
			name: "missing nullable author key",
			data: func() map[string]any {
				comment := gqlTestComment("5702456239")
				delete(comment, "author")
				pull := gqlTestPullNode("pr-1", 10, headA, nil, []any{comment})
				return snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
			},
			want: `field "author" is missing`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := rawGraphQLClient(t, test.data())
			pulls, issues, err := loadPipelineInputsGraphQL(client, "ivanarama/onebase", "", "")
			if err == nil || pulls != nil || issues != nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("incomplete production response was accepted: pulls=%+v issues=%+v err=%v", pulls, issues, err)
			}
		})
	}
}

func TestGraphQLProductionIngressAcceptsCompleteEmptySnapshot(t *testing.T) {
	client := rawGraphQLClient(t, snapshotResult(0, nil, false, nil, 0, nil, false, nil))
	pulls, issues, err := loadPipelineInputsGraphQL(client, "ivanarama/onebase", "", "")
	if err != nil || len(pulls) != 0 || len(issues) != 0 {
		t.Fatalf("complete empty snapshot was rejected: pulls=%+v issues=%+v err=%v", pulls, issues, err)
	}
}

func TestGraphQLProductionIngressPreservesNullableAuthor(t *testing.T) {
	comment := gqlTestComment("5702456239")
	comment["author"] = nil
	pull := gqlTestPullNode("pr-1", 10, headA, nil, []any{comment})
	data := snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
	pullConnection := data["repository"].(map[string]any)["pullRequests"].(map[string]any)
	pullConnection["nodes"] = []any{pull}

	client := rawGraphQLClient(t, data)
	pulls, issues, err := loadPipelineInputsGraphQL(client, "ivanarama/onebase", "", "")
	if err != nil || len(pulls) != 1 || len(issues) != 0 || pulls[0].Comments[0].User.Login != "" {
		t.Fatalf("valid nullable fields were rejected: pulls=%+v issues=%+v err=%v", pulls, issues, err)
	}
}

func TestGraphQLCLIRejectsIncompleteConnections(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binaryName := "pipelinehealth"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	// Both the executable name and output path are fixed by the test.
	//nolint:gosec
	build := exec.Command("go", "build", "-o", binaryPath, "./tools/pipelinehealth")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pipelinehealth CLI: %v\n%s", err, output)
	}
	helperPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		data func() map[string]any
		want string
	}{
		{
			name: "null labels",
			data: func() map[string]any {
				pull := gqlTestPullNode("pr-1", 10, headA, nil, nil)
				pull["labels"] = nil
				return snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
			},
			want: `field "labels" is null`,
		},
		{
			name: "null comments",
			data: func() map[string]any {
				pull := gqlTestPullNode("pr-1", 10, headA, nil, nil)
				pull["comments"] = nil
				return snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
			},
			want: `field "comments" is null`,
		},
		{
			name: "empty outer connections",
			data: func() map[string]any {
				return map[string]any{"repository": map[string]any{
					"pullRequests": map[string]any{},
					"issues":       map[string]any{},
				}}
			},
			want: `field "totalCount" is missing`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := json.Marshal(map[string]any{"data": test.data()})
			if err != nil {
				t.Fatal(err)
			}
			// binaryPath is built by this test inside t.TempDir, not supplied by input.
			//nolint:gosec
			command := exec.Command(binaryPath, "-transport", "graphql", "-json")
			command.Dir = repositoryRoot
			command.Env = append(os.Environ(),
				"GH_EXE="+helperPath,
				"PIPELINEHEALTH_TEST_GH_HELPER=1",
				"PIPELINEHEALTH_TEST_GH_RESPONSE="+string(response),
			)
			output, err := command.Output()
			var exitError *exec.ExitError
			exitCode := -1
			var stderr []byte
			if errors.As(err, &exitError) {
				exitCode = exitError.ExitCode()
				stderr = exitError.Stderr
			}
			if exitCode != 2 || !strings.Contains(string(stderr), test.want) || len(output) != 0 {
				t.Fatalf("CLI accepted incomplete response: exit=%v stdout=%q stderr=%q", err, output, stderr)
			}
		})
	}
}

func TestGraphQLSnapshotMapsRESTSemanticsAndLargeDatabaseID(t *testing.T) {
	ship := gqlTestPullNode("pr-1", 10, headA,
		[]any{map[string]any{"name": "ship"}},
		[]any{gqlTestComment("5702456239")})
	ordinary := gqlTestPullNode("pr-2", 11, headB, nil, nil)
	withComment := gqlTestIssueNode("issue-1", 20, []any{gqlTestComment("5702456240")})
	withoutComments := gqlTestIssueNode("issue-2", 21, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		snapshotResult(2, []any{ship, ordinary}, false, nil, 2, []any{withComment, withoutComments}, false, nil),
		map[string]any{"nodes": []any{map[string]any{
			"__typename": "Commit",
			"id":         "commit-pr-1",
			"oid":        headA,
			"parents": connection(2, []any{
				map[string]any{"oid": headB}, map[string]any{"oid": headC},
			}, false, nil),
		}}},
		map[string]any{"nodes": []any{map[string]any{
			"__typename": "PullRequest", "id": "pr-1", "headRefOid": headA,
		}}},
	}}

	pulls, issues, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, true)
	if err != nil {
		t.Fatalf("load GraphQL snapshot: %v", err)
	}
	if len(pulls) != 2 || pulls[0].State != "open" || pulls[0].Head.SHA != headA || pulls[0].Base.Ref != "main" {
		t.Fatalf("pull mapping changed REST semantics: %+v", pulls)
	}
	if pulls[0].Comments[0].ID != 5702456239 {
		t.Fatalf("fullDatabaseId was truncated: %+v", pulls[0].Comments)
	}
	if len(pulls[0].HeadParents) != 2 || pulls[0].HeadParents[0] != headB || pulls[0].HeadParents[1] != headC {
		t.Fatalf("ship head parents were not loaded: %+v", pulls[0].HeadParents)
	}
	if pulls[1].HeadParents != nil {
		t.Fatalf("non-ship PR unexpectedly received head parents: %+v", pulls[1].HeadParents)
	}
	if len(issues) != 1 || issues[0].Number != 20 || issues[0].CommentCount != 1 || issues[0].Thread[0].ID != 5702456240 {
		t.Fatalf("issue mapping/filter changed REST semantics: %+v", issues)
	}
	if len(client.calls) != 3 || !strings.Contains(client.calls[1].query, "nodes(ids: $ids)") ||
		!strings.Contains(client.calls[2].query, "PipelineHealthPullHeads") {
		t.Fatalf("ship parents were not consolidated into one node query: %+v", client.calls)
	}
	ids, ok := client.calls[1].variables["ids"].([]string)
	if !ok || len(ids) != 1 || ids[0] != "commit-pr-1" {
		t.Fatalf("unexpected parent batch: %#v", client.calls[1].variables["ids"])
	}
}

func TestGraphQLSnapshotPaginatesOuterConnections(t *testing.T) {
	first := gqlTestPullNode("pr-1", 10, headA, nil, nil)
	second := gqlTestPullNode("pr-2", 11, headB, nil, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(2, []any{first}, true, "pull-page-1"),
		}},
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(2, []any{second}, false, nil),
		}},
	}}

	pulls, issues, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err != nil {
		t.Fatalf("load paginated snapshot: %v", err)
	}
	if len(pulls) != 2 || len(issues) != 0 {
		t.Fatalf("outer pagination lost nodes: pulls=%+v issues=%+v", pulls, issues)
	}
	if got := client.calls[1].variables["pullCursor"]; got == nil || *(got.(*string)) != "pull-page-1" {
		t.Fatalf("second page used wrong cursor: %#v", got)
	}
	if client.calls[1].variables["includeIssues"] != false {
		t.Fatalf("completed/skipped issue connection was queried again")
	}
}

func TestGraphQLSnapshotPaginatesAllLabelsAndComments(t *testing.T) {
	pull := gqlTestPullNode("pr-1", 10, headA,
		[]any{map[string]any{"name": "first"}},
		[]any{gqlTestComment("5702456239")})
	pull["labels"] = connection(2, []any{map[string]any{"name": "first"}}, true, "labels-1")
	pull["comments"] = connection(2, []any{gqlTestComment("5702456239")}, true, "comments-1")
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(1, []any{pull}, false, nil),
		}},
		map[string]any{"node": map[string]any{
			"__typename": "PullRequest",
			"labels":     connection(2, []any{map[string]any{"name": "second"}}, false, nil),
			"comments":   connection(2, []any{gqlTestComment("5702456240")}, false, nil),
		}},
	}}

	pulls, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err != nil {
		t.Fatalf("load nested pages: %v", err)
	}
	if len(pulls[0].Labels) != 2 || len(pulls[0].Comments) != 2 || pulls[0].Comments[1].ID != 5702456240 {
		t.Fatalf("nested pagination lost data: %+v", pulls[0])
	}
	if client.calls[1].variables["includeLabels"] != true || client.calls[1].variables["includeComments"] != true {
		t.Fatalf("continuation query did not request both open connections: %#v", client.calls[1].variables)
	}
}

func TestGraphQLSnapshotRejectsDuplicateNestedNodes(t *testing.T) {
	for _, test := range []struct {
		name     string
		labels   []any
		comments []any
		want     string
	}{
		{
			name: "labels",
			labels: []any{
				map[string]any{"name": "ship"}, map[string]any{"name": "ship"},
			},
			want: "duplicate label",
		},
		{
			name:     "comments",
			comments: []any{gqlTestComment("5702456239"), gqlTestComment("5702456239")},
			want:     "duplicate fullDatabaseId",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pull := gqlTestPullNode("pr-1", 10, headA, test.labels, test.comments)
			client := &scriptedGraphQLClient{t: t, results: []any{
				map[string]any{"repository": map[string]any{
					"pullRequests": connection(1, []any{pull}, false, nil),
				}},
			}}
			_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("duplicate %s did not fail closed: %v", test.name, err)
			}
		})
	}
}

func TestGraphQLSnapshotRejectsPaginationWithoutCursor(t *testing.T) {
	pull := gqlTestPullNode("pr-1", 10, headA, nil, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(2, []any{pull}, true, nil),
		}},
	}}
	_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err == nil || !strings.Contains(err.Error(), "without endCursor") {
		t.Fatalf("missing pagination cursor did not fail closed: %v", err)
	}
}

func TestGraphQLSnapshotRejectsEmptyAdvancingPages(t *testing.T) {
	t.Run("outer", func(t *testing.T) {
		client := &scriptedGraphQLClient{t: t, results: []any{
			map[string]any{"repository": map[string]any{
				"pullRequests": connection(1, nil, true, "next"),
			}},
		}}
		_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
		if err == nil || !strings.Contains(err.Error(), "empty advancing page") {
			t.Fatalf("empty outer page did not fail closed: %v", err)
		}
	})

	t.Run("nested", func(t *testing.T) {
		pull := gqlTestPullNode("pr-1", 10, headA, []any{map[string]any{"name": "first"}}, nil)
		pull["labels"] = connection(2, []any{map[string]any{"name": "first"}}, true, "labels-1")
		client := &scriptedGraphQLClient{t: t, results: []any{
			map[string]any{"repository": map[string]any{
				"pullRequests": connection(1, []any{pull}, false, nil),
			}},
			map[string]any{"node": map[string]any{
				"__typename": "PullRequest",
				"labels":     connection(2, nil, false, nil),
			}},
		}}
		_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
		if err == nil || !strings.Contains(err.Error(), "empty advancing page") {
			t.Fatalf("empty nested page did not fail closed: %v", err)
		}
	})
}

func TestGraphQLSnapshotRejectsAccumulatedNodesAboveTotalCount(t *testing.T) {
	first := gqlTestPullNode("pr-1", 10, headA, nil, nil)
	second := gqlTestPullNode("pr-2", 11, headB, nil, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(1, []any{first, second}, false, nil),
		}},
	}}
	_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err == nil || !strings.Contains(err.Error(), "exceeds totalCount") {
		t.Fatalf("overfull connection did not fail closed: %v", err)
	}
}

func TestGraphQLSnapshotRejectsRepeatedCursor(t *testing.T) {
	first := gqlTestPullNode("pr-1", 10, headA, nil, nil)
	second := gqlTestPullNode("pr-2", 11, headB, nil, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(3, []any{first}, true, "same-cursor"),
		}},
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(3, []any{second}, true, "same-cursor"),
		}},
	}}
	_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err == nil || !strings.Contains(err.Error(), "repeated cursor") {
		t.Fatalf("cursor loop did not fail closed: %v", err)
	}
}

func TestGraphQLSnapshotRejectsConnectionCountChange(t *testing.T) {
	first := gqlTestPullNode("pr-1", 10, headA, nil, nil)
	second := gqlTestPullNode("pr-2", 11, headB, nil, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(2, []any{first}, true, "page-1"),
		}},
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(3, []any{second}, false, nil),
		}},
	}}
	_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err == nil || !strings.Contains(err.Error(), "totalCount changed") {
		t.Fatalf("moving queue snapshot did not fail closed: %v", err)
	}
}

func TestGraphQLSnapshotRejectsMalformedOrOverflowDatabaseID(t *testing.T) {
	for _, id := range []string{"not-a-number", "9223372036854775808"} {
		t.Run(id, func(t *testing.T) {
			pull := gqlTestPullNode("pr-1", 10, headA, nil, []any{gqlTestComment(id)})
			client := &scriptedGraphQLClient{t: t, results: []any{
				map[string]any{"repository": map[string]any{
					"pullRequests": connection(1, []any{pull}, false, nil),
				}},
			}}
			_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
			if err == nil || !strings.Contains(err.Error(), "invalid fullDatabaseId") {
				t.Fatalf("invalid database id did not fail closed: %v", err)
			}
		})
	}
}

func TestGraphQLSnapshotRejectsShipHeadMismatch(t *testing.T) {
	pull := gqlTestPullNode("pr-1", 10, headA, []any{map[string]any{"name": "ship"}}, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(1, []any{pull}, false, nil),
		}},
		map[string]any{"nodes": []any{map[string]any{
			"__typename": "Commit", "id": "commit-pr-1", "oid": headB,
			"parents": emptyConnection(),
		}}},
	}}
	_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err == nil || !strings.Contains(err.Error(), "changed oid") {
		t.Fatalf("head/commit race did not fail closed: %v", err)
	}
}

func TestGraphQLSnapshotRejectsPushRaceAfterParentLookup(t *testing.T) {
	pull := gqlTestPullNode("pr-1", 10, headA, []any{map[string]any{"name": "ship"}}, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(1, []any{pull}, false, nil),
		}},
		map[string]any{"nodes": []any{map[string]any{
			"__typename": "Commit", "id": "commit-pr-1", "oid": headA,
			"parents": connection(2, []any{
				map[string]any{"oid": headB}, map[string]any{"oid": headC},
			}, false, nil),
		}}},
		map[string]any{"nodes": []any{map[string]any{
			"__typename": "PullRequest", "id": "pr-1", "headRefOid": headB,
		}}},
	}}
	_, _, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, false)
	if err == nil || !strings.Contains(err.Error(), "headRefOid changed") {
		t.Fatalf("push between snapshot and parent lookup was accepted: %v", err)
	}
}

func TestGraphQLSnapshotRejectsClientErrorWithoutPartialResult(t *testing.T) {
	client := &scriptedGraphQLClient{
		t:       t,
		results: []any{nil},
		errors:  []error{errors.New("GraphQL response errors: field failed")},
	}
	pulls, issues, err := loadGraphQLSnapshot(client, "ivanarama/onebase", true, true)
	if err == nil || pulls != nil || issues != nil {
		t.Fatalf("partial GraphQL failure was accepted: pulls=%+v issues=%+v err=%v", pulls, issues, err)
	}
}

func TestDecodeGraphQLResponseRejectsPartialDataWithErrors(t *testing.T) {
	response := []byte(`{"data":{"repository":{"pullRequests":{"totalCount":1}}},"errors":[{"message":"comments failed"}]}`)
	var destination map[string]any
	err := decodePipelineGraphQLResponse(response, &destination)
	if err == nil || !strings.Contains(err.Error(), "comments failed") {
		t.Fatalf("partial GraphQL response was accepted: destination=%+v err=%v", destination, err)
	}
	if destination != nil {
		t.Fatalf("partial data escaped into destination: %+v", destination)
	}
}

func TestGraphQLErrorDetailIsBoundedAndMarked(t *testing.T) {
	response, err := json.Marshal(map[string]any{
		"data":   map[string]any{"repository": map[string]any{}},
		"errors": []any{map[string]any{"message": strings.Repeat("я", maxPipelineGraphQLDetailBytes)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var destination map[string]any
	err = decodePipelineGraphQLResponse(response, &destination)
	if err == nil || len(err.Error()) > maxPipelineGraphQLDetailBytes || !strings.HasSuffix(err.Error(), "[truncated]") {
		t.Fatalf("GraphQL error detail was not bounded: len=%d err=%v", len(err.Error()), err)
	}
	if !utf8.ValidString(err.Error()) {
		t.Fatalf("GraphQL error truncation split UTF-8: %q", err.Error())
	}
}

func TestGraphQLQueryAndPageBoundsFailClosed(t *testing.T) {
	t.Run("queries", func(t *testing.T) {
		delegate := &scriptedGraphQLClient{t: t}
		client := &limitedPipelineGraphQLClient{delegate: delegate, used: maxPipelineGraphQLQueries}
		err := client.Query("query { viewer { login } }", nil, &map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "exceeded") || len(delegate.calls) != 0 {
			t.Fatalf("query bound did not fail before transport: calls=%d err=%v", len(delegate.calls), err)
		}
	})

	t.Run("pages", func(t *testing.T) {
		seen := make(map[string]bool, maxPipelineGraphQLPages)
		for index := 0; index < maxPipelineGraphQLPages; index++ {
			seen[strconv.Itoa(index)] = true
		}
		cursor := "new-cursor"
		_, _, err := advanceGQLCursor("test", gqlPageInfo{HasNextPage: true, EndCursor: &cursor}, seen)
		if err == nil || !strings.Contains(err.Error(), "exceeded") {
			t.Fatalf("page bound did not fail closed: %v", err)
		}
	})
}

func TestBoundedGraphQLCaptureStopsRetainingAfterLimit(t *testing.T) {
	capture := newBoundedCapture(4)
	if written, err := capture.Write([]byte("abcdef")); err != nil || written != 6 {
		t.Fatalf("bounded capture must consume process output: written=%d err=%v", written, err)
	}
	if !capture.truncated || capture.String() != "abcd" {
		t.Fatalf("bounded capture did not enforce limit: truncated=%v data=%q", capture.truncated, capture.String())
	}
	if written, err := capture.Write([]byte("gh")); err != nil || written != 2 || capture.String() != "abcd" {
		t.Fatalf("bounded capture retained bytes after limit: written=%d err=%v data=%q", written, err, capture.String())
	}
}

func TestGraphQLCommandUsesOperationalTimeout(t *testing.T) {
	client := &ghPipelineGraphQLClient{
		executable: "unused",
		timeout:    time.Millisecond,
		run: func(ctx context.Context, _ string, _ []byte, _, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	var destination map[string]any
	err := client.Query("query { viewer { login } }", nil, &destination)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("GraphQL command timeout was not enforced: %v", err)
	}
}

func TestGraphQLFixtureMatrixKeepsPullFixtureOffline(t *testing.T) {
	directory := t.TempDir()
	pullPath := filepath.Join(directory, "pulls.json")
	data, err := json.Marshal([]apiPull{testPR(42, headA)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pullPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	client := &scriptedGraphQLClient{t: t}
	pulls, issues, err := loadPipelineInputsGraphQL(client, "ivanarama/onebase", pullPath, "")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if len(pulls) != 1 || pulls[0].Number != 42 || len(issues) != 0 || len(client.calls) != 0 {
		t.Fatalf("pull fixture unexpectedly touched GitHub: pulls=%+v issues=%+v calls=%d", pulls, issues, len(client.calls))
	}
}

func TestGraphQLIssueFixtureStillLoadsLivePullsOnly(t *testing.T) {
	directory := t.TempDir()
	issuePath := filepath.Join(directory, "issues.json")
	data, err := json.Marshal([]apiIssue{testIssue(43)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issuePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	pull := gqlTestPullNode("pr-1", 42, headA, nil, nil)
	client := &scriptedGraphQLClient{t: t, results: []any{
		map[string]any{"repository": map[string]any{
			"pullRequests": connection(1, []any{pull}, false, nil),
		}},
	}}

	pulls, issues, err := loadPipelineInputsGraphQL(client, "ivanarama/onebase", "", issuePath)
	if err != nil {
		t.Fatalf("load mixed fixture/live inputs: %v", err)
	}
	if len(pulls) != 1 || len(issues) != 1 || issues[0].Number != 43 {
		t.Fatalf("mixed fixture matrix changed: pulls=%+v issues=%+v", pulls, issues)
	}
	if len(client.calls) != 1 || client.calls[0].variables["includeIssues"] != false {
		t.Fatalf("issue fixture still triggered a live issue query: %+v", client.calls)
	}
}

func TestSplitGitHubRepositoryRejectsAmbiguousNames(t *testing.T) {
	for _, repo := range []string{"", "onebase", "/onebase", "ivanarama/", "a/b/c"} {
		if _, _, err := splitGitHubRepository(repo); err == nil {
			t.Fatalf("invalid repository %q was accepted", repo)
		}
	}
}
