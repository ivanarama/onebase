package main

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Exercise the command entry point for both transports. REST uses a local TLS
// GitHub API and ghstub only for authentication; GraphQL uses ghstub responses.
func TestV1AbortPublicCLITransports(t *testing.T) {
	stub := buildGhStub(t)
	binary := filepath.Join(t.TempDir(), "pipelinehealth.exe")
	//nolint:gosec // G204: fixed package and test-owned executable path.
	build := exec.Command("go", "build", "-o", binary, "./tools/pipelinehealth")
	build.Dir = repoRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"rest", "graphql"} {
		t.Run(transport, func(t *testing.T) {
			for _, tc := range []struct {
				name                              string
				mutate                            func(*apiPull)
				owner                             string
				wantParents, wantDiagnostic, fail bool
			}{
				{name: "abort without ship or reviewed", wantParents: true, wantDiagnostic: true},
				{name: "configured trusted owner", owner: "trusted-bot", wantParents: true, wantDiagnostic: true},
				{name: "foreign author", mutate: func(p *apiPull) { p.Comments[2].User.Login = "someone" }},
				{name: "edited marker", mutate: func(p *apiPull) { p.Comments[2].UpdatedAt = "2026-09-02T00:00:00Z" }},
				{name: "another head", mutate: func(p *apiPull) { p.Comments[2].Body = syncV1Abort(30, headD) }},
				{name: "draft", mutate: func(p *apiPull) { p.Draft = true }},
				{name: "non-main", mutate: func(p *apiPull) { p.Base.Ref = "release" }},
				{name: "closed", mutate: func(p *apiPull) { p.State = "closed" }},
				{name: "no merge shape", wantParents: true},
				{name: "parents read failure", wantParents: true, fail: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					owner := tc.owner
					if owner == "" {
						owner = "ivanarama"
					}
					item := testPR(1464, headC)
					for _, c := range []struct {
						id   int64
						body string
					}{{20, completion(headA, 10, 15)}, {30, syncIntent(headA, 10, 15, 20)}, {35, syncV1Abort(30, headC)}} {
						stamp := "2026-09-01T00:00:00Z"
						item.Comments = append(item.Comments, apiComment{ID: c.id, CreatedAt: stamp, UpdatedAt: stamp, User: apiUser{Login: owner}, Body: c.body})
					}
					if tc.mutate != nil {
						tc.mutate(&item)
					}
					parents := []string{headA, headB}
					if tc.name == "no merge shape" {
						parents = parents[:1]
					}
					dataDir := t.TempDir()
					logPath := filepath.Join(dataDir, "requests.log")
					writeGhStubFixture(t, dataDir, "snapshot.json", abortGraphQLSnapshot(item))
					parentNodes := []any{}
					restParents := []map[string]string{}
					for _, p := range parents {
						parentNodes = append(parentNodes, map[string]any{"oid": p})
						restParents = append(restParents, map[string]string{"sha": p})
					}
					writeGhStubFixture(t, dataDir, "parents.json", map[string]any{"data": map[string]any{"nodes": []any{map[string]any{
						"__typename": "Commit", "id": "commit-pr-1", "oid": headC, "parents": connection(len(parents), parentNodes, false, nil),
					}}}})
					writeGhStubFixture(t, dataDir, "head.json", map[string]any{"data": map[string]any{"nodes": []any{map[string]any{
						"__typename": "PullRequest", "id": "pr-1", "headRefOid": headC,
					}}}})
					var mu sync.Mutex
					parentReads := 0
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch {
						case strings.Contains(r.URL.Path, "/pulls"):
							//nolint:errcheck // Test fixture response; command failure is asserted below.
							json.NewEncoder(w).Encode([]apiPull{item})
						case strings.Contains(r.URL.Path, "/comments"):
							//nolint:errcheck // Test fixture response.
							json.NewEncoder(w).Encode(item.Comments)
						case strings.Contains(r.URL.Path, "/commits/"):
							mu.Lock()
							parentReads++
							mu.Unlock()
							if tc.fail {
								http.Error(w, "injected commit read failure", http.StatusInternalServerError)
								return
							}
							//nolint:errcheck // Test fixture response.
							json.NewEncoder(w).Encode(map[string]any{"parents": restParents})
						default:
							http.Error(w, "unexpected endpoint", http.StatusNotFound)
						}
					}))
					defer server.Close()
					ca := filepath.Join(dataDir, "ca.pem")
					if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
						t.Fatal(err)
					}
					issues := filepath.Join(dataDir, "issues.json")
					if err := os.WriteFile(issues, []byte("[]"), 0o600); err != nil {
						t.Fatal(err)
					}
					exe := binary
					if transport == "rest" {
						exe = helper
					}
					//nolint:gosec // G204: executable built by this test or its own test binary.
					command := exec.Command(exe, "-transport", transport, "-json", "-issues", issues, "-owner", owner, "-cache-dir", filepath.Join(dataDir, "cache"), "-contract", filepath.Join(repoRoot(t), ".claude", "skills", "review-queue", "SKILL.md"))
					command.Dir = repoRoot(t)
					command.Env = append(os.Environ(), "GH_EXE="+stub, "GHSTUB_DATA="+dataDir, "GHSTUB_LOG="+logPath, "GHSTUB_FAIL_COMMIT=0", "GH_TOKEN=", "GITHUB_TOKEN=", "GH_ENTERPRISE_TOKEN=", "GITHUB_ENTERPRISE_TOKEN=", "GH_HOST="+strings.TrimPrefix(server.URL, "https://"), "PIPELINEHEALTH_TEST_REST_CA=")
					if tc.fail {
						command.Env = append(command.Env, "GHSTUB_FAIL_COMMIT=1")
					}
					if transport == "rest" {
						command.Env = append(command.Env, "PIPELINEHEALTH_TEST_REST_CA="+ca)
					}
					output, runErr := command.CombinedOutput()
					if transport == "graphql" && tc.name == "closed" {
						if runErr == nil || !strings.Contains(string(output), "invalid identity or state") {
							t.Fatalf("closed PR was accepted: %v %s", runErr, output)
						}
					} else if tc.fail {
						if runErr == nil || !strings.Contains(string(output), "injected commit read failure") {
							t.Fatalf("parent failure not propagated: %v %s", runErr, output)
						}
					} else {
						if runErr != nil {
							t.Fatalf("CLI failed: %v %s", runErr, output)
						}
						var got report
						if err := json.Unmarshal(output, &got); err != nil {
							t.Fatalf("output: %v %s", err, output)
						}
						if hasFinding(got, "base_sync_v1_abort_waiting_review") != tc.wantDiagnostic {
							t.Fatalf("abort diagnosis: %+v", got.Findings)
						}
						if got.IntegrationOwner != nil || len(got.MergeCandidates) != 0 {
							t.Fatalf("abort authorized integration/merge: %+v", got)
						}
						if tc.wantDiagnostic && (len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 1464 || got.ReviewCandidates[0].Stage != "review") {
							t.Fatalf("ordinary review lost: %+v", got.ReviewCandidates)
						}
					}
					mu.Lock()
					readParents := parentReads > 0
					mu.Unlock()
					if transport == "graphql" {
						requests, err := os.ReadFile(logPath)
						if err != nil {
							t.Fatal(err)
						}
						readParents = strings.Contains(string(requests), "parents.json")
						if tc.wantParents && !tc.fail && !strings.Contains(string(requests), "head.json") {
							t.Fatal("HEAD was not rechecked after loading parents")
						}
					}
					if readParents != tc.wantParents {
						t.Fatalf("parent read=%v, want=%v", readParents, tc.wantParents)
					}
				})
			}
		})
	}
}

func abortGraphQLSnapshot(item apiPull) map[string]any {
	comments := []any{}
	for _, c := range item.Comments {
		comments = append(comments, map[string]any{"fullDatabaseId": fmt.Sprint(c.ID), "createdAt": c.CreatedAt, "updatedAt": c.UpdatedAt, "author": map[string]any{"login": c.User.Login}, "body": c.Body})
	}
	pull := gqlTestPullNode("pr-1", item.Number, item.Head.SHA, nil, comments)
	pull["state"] = strings.ToUpper(item.State)
	pull["isDraft"] = item.Draft
	pull["baseRefName"] = item.Base.Ref
	return map[string]any{"data": snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)}
}
