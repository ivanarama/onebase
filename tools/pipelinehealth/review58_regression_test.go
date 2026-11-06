package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

// Private review probes; synthetic peers only, no live transport.
func TestIndependentPageWatermarkDrift(t *testing.T) {
	s := routingGolden(t, "third-typed-round")
	values := s["edges"].([]any)
	first := routingPageData(t, s, values[:4], true, "PAGE1")
	last := routingPageData(t, s, values[4:], false, "PAGE2")
	changedFirst := routingClone(t, first)
	routingObject(routingObject(routingObject(changedFirst["repository"])["pullRequest"])["timelineItems"])["updatedAt"] = "2026-10-29T00:20:00Z"
	routingObject(routingObject(routingObject(last["repository"])["pullRequest"])["timelineItems"])["updatedAt"] = "2026-10-29T00:20:00Z"
	// A real monotonic change: pass 1 observes old -> new, pass 2 new -> new.
	client := &scriptedGraphQLClient{t: t, results: []any{first, last, changedFirst, last}}
	got, err := stableRoutingSnapshot(client, "owner/repo", "R_BASE", 42)
	if err == nil {
		t.Fatalf("accepted divergent first-page watermarks across passes; final=%v calls=%d", got["updatedAt"], len(client.calls))
	}
}

func independentRewritePublication(t *testing.T, s map[string]any, mutate func(map[string]any)) {
	t.Helper()
	oldContext, newContext := "", ""
	for _, raw := range s["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		body := routingText(node["body"])
		if !strings.HasPrefix(body, "<!-- pp:review-routing-v1 ") {
			continue
		}
		p, err := reviewrouting.Parse(body)
		if err != nil {
			t.Fatal(err)
		}
		if p["kind"] == "publication" {
			oldContext, err = reviewrouting.Context(p)
			if err != nil {
				t.Fatal(err)
			}
			mutate(p)
			newContext, err = reviewrouting.Context(p)
			if err != nil {
				t.Fatal(err)
			}
		} else if p["context_sha256"] == oldContext {
			p["context_sha256"] = newContext
		}
		rendered, err := reviewrouting.Render(p)
		if err != nil {
			t.Fatal(err)
		}
		node["body"] = rendered
	}
}

func TestIndependentPublicAuditBinding(t *testing.T) {
	s := routingGolden(t, "third-typed-round")
	independentRewritePublication(t, s, func(p map[string]any) { routingObject(p["audit"])["snapshot_sha256"] = strings.Repeat("f", 64) })
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-routing-inspect", "42")
	if err == nil {
		t.Fatalf("public inspection accepted invented audit prefix hash: depth=%v authority=%v", value["verified_depth"], value["authority"])
	}
}

func TestIndependentPublicPublicationAuthorBinding(t *testing.T) {
	s := routingGolden(t, "third-typed-round")
	independentRewritePublication(t, s, func(p map[string]any) { p["author"] = map[string]any{"id": "U_OTHER", "login": "other"} })
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-routing-inspect", "42")
	if err == nil {
		t.Fatalf("public inspection accepted publication for another PR author: depth=%v", value["verified_depth"])
	}
}

func TestIndependentPublicAuditQueueBinding(t *testing.T) {
	s := routingGolden(t, "third-typed-round")
	independentRewritePublication(t, s, func(p map[string]any) { routingObject(p["audit"])["snapshot_sha256"] = strings.Repeat("f", 64) })
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(value["technical_fix_candidates"].([]any)) > 0 {
		t.Fatalf("invented audit prefix admitted a technical source: %v", value["technical_fix_candidates"])
	}
}

func TestIndependentPublicTypedShipFence(t *testing.T) {
	s := routingGolden(t, "third-typed-round")
	independentRewritePublication(t, s, func(p map[string]any) {
		r := routingObject(p["report"])
		r["blocking"] = []any{}
		digest, err := reviewrouting.Digest(r)
		if err != nil {
			t.Fatal(err)
		}
		p["report_sha256"] = digest
		p["outcome"] = "reviewed"
	})
	s["labels"] = []any{"reviewed", "ship"}
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(value["merge_candidates"].([]any)) != 0 || value["integration_owner"] != nil || len(value["engineering_waiting"].([]any)) != 1 {
		t.Fatalf("typed ship escaped acceptance fence: %v", value)
	}
}

func TestIndependentUnfinishedPublicationAuthorQueue(t *testing.T) {
	s := routingGolden(t, "unfinished-valid")
	independentRewritePublication(t, s, func(p map[string]any) { p["author"] = map[string]any{"id": "U_OTHER", "login": "other"} })
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(value["content_review_candidates"].([]any)) > 0 {
		t.Fatalf("ordinary REVIEW elected to recover publication bound to another author: %v", value["content_review_candidates"])
	}
}

func TestIndependentUnfinishedPublicationIdentityQueue(t *testing.T) {
	s := routingGolden(t, "unfinished-valid")
	independentRewritePublication(t, s, func(p map[string]any) {
		i := routingObject(p["identity"])
		i["base_repository"] = "other/repository"
		i["head_repository"] = "other/repository"
	})
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(value["content_review_candidates"].([]any)) > 0 {
		t.Fatalf("ordinary REVIEW elected to recover publication whose complete identity differs from PR: %v", value["content_review_candidates"])
	}
}

func TestIndependentMissingEditStatusTransport(t *testing.T) {
	s := routingGolden(t, "third-typed-round")
	for _, raw := range s["edges"].([]any) {
		n := routingObject(routingObject(raw)["node"])
		if n["__typename"] == "IssueComment" {
			delete(n, "lastEditedAt")
		}
	}
	page := routingPageData(t, s, s["edges"].([]any), false, "END")
	client := &scriptedGraphQLClient{t: t, results: []any{page, page}}
	got, err := stableRoutingSnapshot(client, "owner/repo", "R_BASE", 42)
	if err != nil {
		return
	}
	verified, err := reviewrouting.VerifiedDepth(got, "owner")
	if err == nil {
		t.Fatalf("incomplete transport with no edit-status fields was accepted as verified depth %d", verified.Depth)
	}
}

func TestIndependentFlagOffKeepsLegacyJSON(t *testing.T) {
	s := routingGolden(t, "two-legacy-rounds")
	value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-contract", routingSkillFixture(t, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"review_routing_contract", "fix_execution_contract", "technical_fix_candidates", "technical_fix_settlement_candidates", "engineering_waiting", "technical_waiting"} {
		if _, found := value[field]; found {
			t.Fatalf("flag-OFF added %s", field)
		}
	}
	if len(value["review_candidates"].([]any)) != 1 {
		t.Fatalf("ordinary queue changed: %v", value["review_candidates"])
	}
}

func TestIndependentSyntheticPublicInspectionTransport(t *testing.T) {
	for _, fault := range []string{"control", "audit", "author", "missing-edit-status"} {
		t.Run(fault, func(t *testing.T) {
			s := routingGolden(t, "third-typed-round")
			switch fault {
			case "audit":
				independentRewritePublication(t, s, func(p map[string]any) { routingObject(p["audit"])["snapshot_sha256"] = strings.Repeat("f", 64) })
			case "author":
				independentRewritePublication(t, s, func(p map[string]any) { p["author"] = map[string]any{"id": "U_OTHER", "login": "other"} })
			case "missing-edit-status":
				for _, raw := range s["edges"].([]any) {
					delete(routingObject(routingObject(raw)["node"]), "lastEditedAt")
				}
			}
			page := routingPageData(t, s, s["edges"].([]any), false, "END")
			pages := []any{
				map[string]any{"data": map[string]any{"repository": map[string]any{"id": "R_BASE", "nameWithOwner": "owner/repo"}}},
				map[string]any{"data": page}, map[string]any{"data": page},
			}
			data, err := json.Marshal(pages)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "synthetic-pages.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // Fixed public CLI compiled into this test process's private directory.
			cmd := exec.Command(pipelinehealthCLI(t), "-json", "-repo", "owner/repo", "-owner", "owner", "-routing-inspect", "42")
			preparePipelineGraphQLCommand(cmd)
			cmd.Env = append(os.Environ(), "GH_EXE="+executable, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_SEQUENCE="+path)
			output, runErr := cmd.Output()
			value := map[string]any{}
			if err := json.Unmarshal(output, &value); err != nil {
				t.Fatalf("CLI JSON=%s err=%v process=%v", output, err, runErr)
			}
			t.Logf("fault=%s depth=%v authority=%v error=%v", fault, value["verified_depth"], value["authority"], value["error"])
			if fault == "control" {
				if runErr != nil || value["verified_depth"] != float64(3) || value["mutation_authorized"] != false {
					t.Fatal("synthetic positive control failed")
				}
				return
			}
			if runErr == nil {
				t.Fatalf("public inspection's actual transport accepted %s as authenticated verified history", fault)
			}
		})
	}
}
