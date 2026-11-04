package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type identityGolden struct {
	Cases []struct {
		Name    string         `json:"name"`
		REST    map[string]any `json:"rest"`
		GraphQL map[string]any `json:"graphql"`
		SHA     string         `json:"identity_sha256"`
	} `json:"cases"`
}

func identityGoldens(t *testing.T) identityGolden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "pridentity", "testdata", "pr-identity-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result identityGolden
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func identityRESTPull(t *testing.T) map[string]any {
	t.Helper()
	p := identityGoldens(t).Cases[0].REST
	p["state"], p["title"], p["labels"], p["draft"] = "open", "Identity fixture", []any{}, false
	p["created_at"], p["updated_at"] = "2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z"
	return p
}

func identityFixtureCLI(t *testing.T, pulls []any, transport string, extra ...string) report {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pulls.json")
	data, err := json.Marshal(pulls)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-transport", transport, "-json", "-repo", "owner/repo", "-prs", path}
	args = append(args, extra...)
	//nolint:gosec // Fixed CLI and test-owned fixture; no shell or GitHub calls.
	cmd := exec.Command(pipelinehealthCLI(t), args...)
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI failed: %v\n%s", err, output)
	}
	var result report
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("CLI JSON: %v\n%s", err, output)
	}
	return result
}

func TestIdentityInventoryPublicGoldenAndOrder(t *testing.T) {
	for _, transport := range []string{"graphql", "rest"} {
		t.Run(transport, func(t *testing.T) {
			var pulls []any
			for _, c := range identityGoldens(t).Cases {
				p := c.REST
				p["state"], p["title"], p["labels"] = "open", c.Name, []any{}
				p["created_at"], p["updated_at"] = "2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z"
				// Separate invocations keep the common PR number from the paired fixture.
				r := identityFixtureCLI(t, []any{p}, transport, "-pr-identity", "-base-repository-id", "R_BASE")
				if len(r.ReviewCandidates) != 1 {
					t.Fatalf("valid identity excluded: %+v", r)
				}
				i := r.ReviewCandidates[0]
				if i.IdentitySHA256 != c.SHA || i.PRIdentity == nil || i.PRIdentity.HeadRef != "ветка/проверка" || i.BaseRefOID != strings.Repeat("b", 40) {
					t.Fatalf("paired identity mismatch: %+v", i)
				}
				if r.IdentityAuthority != "read-only" || r.IdentityV2Enabled == nil || *r.IdentityV2Enabled {
					t.Fatal("inventory granted authority")
				}
			}
			for _, n := range []int{53, 11, 47} {
				p := identityRESTPull(t)
				p["number"] = n
				p["node_id"] = fmt.Sprintf("PR_%d", n)
				pulls = append(pulls, p)
			}
			old := identityFixtureCLI(t, pulls, transport)
			next := identityFixtureCLI(t, pulls, transport, "-pr-identity", "-base-repository-id", "R_BASE")
			for i := range next.ReviewCandidates {
				next.ReviewCandidates[i].PRIdentity = nil
				next.ReviewCandidates[i].IdentitySHA256 = ""
				next.ReviewCandidates[i].BaseRefOID = ""
			}
			if !reflect.DeepEqual(old.ReviewCandidates, next.ReviewCandidates) {
				t.Fatal("identity changed candidate order or original fields")
			}
		})
	}
}

func TestIdentityInventoryPublicInvalidTargets(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"deleted fork":  func(p map[string]any) { p["head"].(map[string]any)["repo"] = nil },
		"non-main":      func(p map[string]any) { p["base"].(map[string]any)["ref"] = "release" },
		"missing SHA":   func(p map[string]any) { p["head"].(map[string]any)["sha"] = "" },
		"missing ref":   func(p map[string]any) { p["head"].(map[string]any)["ref"] = "" },
		"wrong base ID": func(p map[string]any) { p["base"].(map[string]any)["repo"].(map[string]any)["node_id"] = "R_OTHER" },
		"missing PR ID": func(p map[string]any) { delete(p, "node_id") },
		"draft":         func(p map[string]any) { p["draft"] = true },
		"newline ref":   func(p map[string]any) { p["head"].(map[string]any)["ref"] = "x\nhead-sha=z" },
	} {
		t.Run(name, func(t *testing.T) {
			for _, transport := range []string{"graphql", "rest"} {
				p := identityRESTPull(t)
				change(p)
				r := identityFixtureCLI(t, []any{p}, transport, "-pr-identity", "-base-repository-id", "R_BASE")
				if len(r.ReviewCandidates) != 0 || len(r.MergeExecutable) != 0 || len(r.FixCandidates) != 0 {
					t.Fatalf("invalid identity executable: %+v", r)
				}
				found := false
				for _, f := range r.Findings {
					found = found || f.Code == "invalid_pr_identity" && f.PR == 42
				}
				if !found {
					t.Fatalf("missing explicit finding: %+v", r.Findings)
				}
			}
		})
	}
}

func TestIdentityInventoryPublicGraphQLIngress(t *testing.T) {
	for _, invalid := range []string{"", "deleted fork", "missing cross flag", "wrong cross flag", "wrong root ID"} {
		t.Run("case "+invalid, func(t *testing.T) {
			g := identityGoldens(t).Cases[0]
			p := gqlTestPullNode("PR_NODE_42", 42, strings.Repeat("a", 40), nil, nil)
			for k, v := range g.GraphQL {
				p[k] = v
			}
			switch invalid {
			case "deleted fork":
				p["headRepository"] = nil
			case "missing cross flag":
				delete(p, "isCrossRepository")
			case "wrong cross flag":
				p["isCrossRepository"] = true
			}
			snapshot := snapshotResult(1, []any{p}, false, nil, 0, nil, false, nil)
			r := snapshot["repository"].(map[string]any)
			r["id"], r["nameWithOwner"] = "R_BASE", "owner/repo"
			if invalid == "wrong root ID" {
				r["id"] = "R_DIFFERENT"
			}
			data, err := json.Marshal(map[string]any{"data": snapshot})
			if err != nil {
				t.Fatal(err)
			}
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // Executes the owned CLI with an offline gh helper.
			cmd := exec.Command(pipelinehealthCLI(t), "-pr-identity", "-repo", "owner/repo", "-json")
			cmd.Dir = repoRoot(t)
			cmd.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_RESPONSE="+string(data))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("GraphQL CLI: %v\n%s", err, out)
			}
			var result report
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatal(err)
			}
			if invalid == "" {
				if len(result.ReviewCandidates) != 1 || result.ReviewCandidates[0].IdentitySHA256 != g.SHA {
					t.Fatalf("GraphQL drift: %s", out)
				}
			} else if len(result.ReviewCandidates) != 0 {
				t.Fatalf("invalid GraphQL identity accepted: %s", out)
			}
		})
	}
}

func TestIdentityInventoryPublicRejectsLossyUnicode(t *testing.T) {
	for _, ref := range []string{`"\ud800"`, string([]byte{'"', 0xff, '"'})} {
		p := identityRESTPull(t)
		data, err := json.Marshal([]any{p})
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(string(data), `"ref":"ветка/проверка"`, `"ref":`+ref, 1)
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // Fixed executable and test-owned malformed fixture.
		cmd := exec.Command(pipelinehealthCLI(t), "-pr-identity", "-prs", path, "-repo", "owner/repo", "-base-repository-id", "R_BASE", "-json")
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err == nil || strings.Contains(string(out), `"review_candidates"`) {
			t.Fatalf("lossy Unicode accepted: %v %s", err, out)
		}
	}
}

func TestIdentityInventoryPublicChangesBoundToDigest(t *testing.T) {
	original := identityFixtureCLI(t, []any{identityRESTPull(t)}, "graphql", "-pr-identity", "-base-repository-id", "R_BASE").ReviewCandidates[0]
	for _, change := range []string{"head ref", "head repository", "base oid", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			p := identityRESTPull(t)
			switch change {
			case "head ref":
				p["head"].(map[string]any)["ref"] = "renamed"
			case "head repository":
				p["head"].(map[string]any)["repo"] = map[string]any{"node_id": "R_FORK", "full_name": "contributor/repo"}
			case "base oid":
				p["base"].(map[string]any)["sha"] = strings.Repeat("c", 40)
			}
			pulls := []any{p}
			if change == "duplicate" {
				pulls = append(pulls, p)
			}
			r := identityFixtureCLI(t, pulls, "graphql", "-pr-identity", "-base-repository-id", "R_BASE")
			if change == "duplicate" {
				if len(r.ReviewCandidates) != 0 {
					t.Fatal("duplicate retained authority")
				}
				return
			}
			i := r.ReviewCandidates[0]
			if i.Head != original.Head {
				t.Fatal("control changed head SHA")
			}
			if (i.IdentitySHA256 == original.IdentitySHA256) != (change == "base oid") {
				t.Fatalf("wrong digest boundary: %+v", i)
			}
			if change == "base oid" && i.BaseRefOID != strings.Repeat("c", 40) {
				t.Fatal("base OID not reported")
			}
		})
	}
}

func TestIdentityInventoryPublicRESTIngress(t *testing.T) {
	p := identityRESTPull(t)
	var rootReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireTestAuthentication(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
			http.Error(w, "read only", 405)
			return
		}
		switch r.URL.Path {
		case "/repos/ivanarama/onebase":
			rootReads.Add(1)
			_, _ = w.Write([]byte(`{"node_id":"R_BASE","full_name":"ivanarama/onebase"}`))
		case "/repos/ivanarama/onebase/pulls":
			p["base"].(map[string]any)["repo"].(map[string]any)["full_name"] = "ivanarama/onebase"
			p["head"].(map[string]any)["repo"].(map[string]any)["full_name"] = "ivanarama/onebase"
			_ = json.NewEncoder(w).Encode([]any{p})
		case "/repos/ivanarama/onebase/issues", "/repos/ivanarama/onebase/issues/42/comments":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, 400)
		}
	}))
	defer server.Close()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // This test binary routes all REST calls to its own HTTP server.
	cmd := exec.Command(helper, "-test.run=^$")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "PIPELINEHEALTH_TEST_REST_CLI="+server.URL, "PIPELINEHEALTH_TEST_PR_IDENTITY=1", "GH_HOST=github.com", "GH_TOKEN=test-token", "PIPELINEHEALTH_CACHE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("REST CLI: %v\n%s", err, out)
	}
	var result report
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if rootReads.Load() != 1 || len(result.ReviewCandidates) != 1 || result.ReviewCandidates[0].PRIdentity == nil || result.ReviewCandidates[0].PRIdentity.BaseRepository != "ivanarama/onebase" {
		t.Fatalf("REST identity not bound to root metadata: %s", out)
	}
}
