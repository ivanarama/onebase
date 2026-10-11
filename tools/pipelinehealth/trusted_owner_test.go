package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRESTCLILoadsReviewedMergeParentsForTrustedOwner(t *testing.T) {
	for _, owner := range []string{"ivanarama", "reviewerbot"} {
		t.Run(owner, func(t *testing.T) {
			pr := addComment(testPR(1321, headB, "reviewed"), 30, completion(headC, 20, 25))
			pr = addComment(pr, 40, completion(headB, 35, 36))
			for index := range pr.Comments {
				pr.Comments[index].User.Login = owner
			}
			var parentReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if !requireTestAuthentication(response, request) {
					return
				}
				response.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/repos/ivanarama/onebase/pulls":
					_ = json.NewEncoder(response).Encode([]apiPull{pr})
				case "/repos/ivanarama/onebase/issues":
					_ = json.NewEncoder(response).Encode([]apiIssue{})
				case "/repos/ivanarama/onebase/issues/1321/comments":
					_ = json.NewEncoder(response).Encode(pr.Comments)
				case "/repos/ivanarama/onebase/commits/" + headB:
					parentReads.Add(1)
					_ = json.NewEncoder(response).Encode(map[string]any{"parents": []any{
						map[string]any{"sha": headA}, map[string]any{"sha": headD},
					}})
				default:
					http.Error(response, "unexpected repository path", http.StatusNotFound)
				}
			}))
			defer server.Close()
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			root, err := filepath.Abs(filepath.Join("..", ".."))
			if err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // executable and endpoint are supplied by this isolated test.
			command := exec.Command(helper, "-test.run=^$")
			command.Dir = root
			command.Env = append(os.Environ(), "PIPELINEHEALTH_TEST_REST_CLI="+server.URL,
				"PIPELINEHEALTH_TEST_REST_OWNER="+owner, "GH_HOST=github.com", "GH_TOKEN=test-token",
				"PIPELINEHEALTH_CACHE_DIR="+t.TempDir())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("REST CLI failed: %v\n%s", err, output)
			}
			var got report
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.ReviewedWaitingShip) != 0 || len(got.HumanWaiting) != 1 ||
				got.HumanWaiting[0].Number != 1321 || got.HumanWaiting[0].Stage != "legacy-source-proof-missing" ||
				!hasFinding(got, "legacy_source_review_missing") {
				t.Fatalf("REST CLI asked for ship without source proof: %+v", got)
			}
			if parentReads.Load() != 1 {
				t.Fatalf("parent requests = %d, want 1", parentReads.Load())
			}
		})
	}
}
