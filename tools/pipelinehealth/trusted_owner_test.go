package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// Both TestMain paths must compose: routing to a TLS fixture retains its CA,
// while a fixture without that CA still fails certificate verification.
func TestRESTCLITLSCertificateAndTransport(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireTestAuthentication(w, r) {
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/ivanarama/onebase/pulls", "/repos/ivanarama/onebase/issues":
			_, _ = w.Write([]byte("[]"))
		default:
			http.Error(w, "unexpected repository path", http.StatusNotFound)
		}
	}))
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, trusted := range []bool{true, false} {
		name := "untrusted certificate"
		ca := ""
		if trusted {
			name, ca = "trusted certificate", caPath
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			//nolint:gosec // G204: current test executable and isolated fixture endpoint.
			command := exec.CommandContext(ctx, helper, "-test.run=^$")
			command.Dir = repoRoot(t)
			command.Env = append(os.Environ(), "PIPELINEHEALTH_TEST_REST_CLI="+server.URL,
				"PIPELINEHEALTH_TEST_REST_CA="+ca, "PIPELINEHEALTH_TEST_REST_OWNER=ivanarama",
				"GH_HOST=github.com", "GH_TOKEN=test-token", "PIPELINEHEALTH_CACHE_DIR="+t.TempDir())
			before := requests.Load()
			output, runErr := command.CombinedOutput()
			if !trusted {
				if runErr == nil || !strings.Contains(string(output), "x509:") || requests.Load() != before {
					t.Fatalf("TLS verification did not reject the untrusted fixture: %v\n%s", runErr, output)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("TLS CLI failed: %v\n%s", runErr, output)
			}
			var got report
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("CLI report: %v\n%s", err, output)
			}
			if got.Scope == "" || requests.Load()-before != 2 {
				t.Fatalf("public CLI did not load both queues: requests=%d report=%+v", requests.Load()-before, got)
			}
		})
	}
}
