package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

// #2110: an override consumed on the old HEAD was resurrected after an author
// merge, so the coordinator repeatedly dispatched work the mutation gate refused.
func TestReviewAgainRequiresCurrentHeadProofPublicCLI(t *testing.T) {
	for _, tc := range []struct {
		name         string
		oldCompleted bool
		currentProof bool
		wantReview   bool
	}{
		{name: "consumed override on old head", oldCompleted: true},
		{name: "old override without current head proof"},
		{name: "current head override", oldCompleted: true, currentProof: true, wantReview: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pr := withMergeHead(testPR(2110, headC, "ship", "reviewed", "needs-decision"))
			pr = addComment(pr, 20, completion(headA, 10, 15))
			pr = addComment(pr, 31, "pp:review-again")
			if tc.oldCompleted {
				pr = addComment(pr, 40, completion(headA, 35, 36))
			}
			if tc.currentProof {
				pr = addComment(pr, 41, completion(headC, 38, 39))
				pr = addComment(pr, 42, "pp:review-again")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !requireTestAuthentication(w, r) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/ivanarama/onebase/pulls":
					_ = json.NewEncoder(w).Encode([]apiPull{pr})
				case "/repos/ivanarama/onebase/issues":
					_ = json.NewEncoder(w).Encode([]apiIssue{})
				case "/repos/ivanarama/onebase/issues/2110/comments":
					_ = json.NewEncoder(w).Encode(pr.Comments)
				case "/repos/ivanarama/onebase/commits/" + headC:
					_ = json.NewEncoder(w).Encode(map[string]any{"parents": []any{
						map[string]string{"sha": headA}, map[string]string{"sha": headB},
					}})
				default:
					http.Error(w, "unexpected request", http.StatusNotFound)
				}
			}))
			defer server.Close()
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // test-owned executable and local API fixture.
			command := exec.Command(helper, "-test.run=^$")
			command.Dir = repoRoot(t)
			command.Env = append(os.Environ(), "PIPELINEHEALTH_TEST_REST_CLI="+server.URL,
				"GH_HOST=github.com", "GH_TOKEN=test-token", "PIPELINEHEALTH_CACHE_DIR="+t.TempDir())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("public REST CLI: %v\n%s", err, output)
			}
			var got report
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.MergeCandidates) != 0 || got.IntegrationOwner != nil {
				t.Fatalf("override granted integration ownership or merge: %+v", got)
			}
			if tc.wantReview {
				if len(got.ContentReviewCandidates) != 1 || len(got.HumanWaiting) != 0 {
					t.Fatalf("fresh current-head override lost REVIEW: %+v", got)
				}
			} else if len(got.HumanWaiting) != 1 || len(got.ReviewCandidates) != 0 || len(got.ContentReviewCandidates) != 0 {
				t.Fatalf("old override unparked a different HEAD: %+v", got)
			}
		})
	}
}
