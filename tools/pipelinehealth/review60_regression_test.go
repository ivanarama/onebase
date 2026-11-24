package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/ivantit66/onebase/internal/reviewrouting"
	"strings"
	"testing"
)

// Newly authored local review probes. Fixtures and subprocesses remain offline.
func TestNonceRetainedHeaderTypes(t *testing.T) {
	for _, field := range []string{"labelsComplete", "state", "isDraft", "labels", "baseRefName"} {
		t.Run(field, func(t *testing.T) {
			s := routingGolden(t, "retained-clean")
			review60RewritePublication(t, s, func(p map[string]any) {
				a := routingObject(p["audit"])
				h := routingObject(routingObject(a["history"])["header"])
				switch field {
				case "labelsComplete":
					h[field] = false
				case "state":
					h[field] = "INVALID_STATE"
				case "isDraft":
					h[field] = "false"
				case "labels":
					h[field] = map[string]any{"unexpected": true}
				case "baseRefName":
					h[field] = "other-base"
				}
				projection := map[string]any{}
				for k, v := range h {
					projection[k] = v
				}
				projection["edges"] = s["edges"].([]any)[:10]
				var err error
				a["snapshot_sha256"], err = reviewrouting.Digest(projection)
				if err != nil {
					t.Fatal(err)
				}
			})
			value, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-routing-inspect", "42")
			t.Logf("field=%s public error=%v depth=%v diagnostic=%v", field, err, value["verified_depth"], value["error"])
			if err == nil {
				t.Errorf("invalid/incomplete original header accepted by public inspection: %s", field)
			}
		})
	}
}

func TestNoncePartialCommentAuthorCannotEraseTypedHistory(t *testing.T) {
	for _, fault := range []string{"missing", "null", "wrong-type"} {
		t.Run(fault, func(t *testing.T) {
			s := routingGolden(t, "third-typed-round")
			// The independent ordinary queue read still has the complete comments.
			pr := routingPullFixture(t, s, 42)
			for _, raw := range s["edges"].([]any) {
				node := routingObject(routingObject(raw)["node"])
				if strings.HasPrefix(routingText(node["body"]), "<!-- pp:review-routing-v1 ") {
					switch fault {
					case "missing":
						delete(node, "author")
					case "null":
						node["author"] = nil
					case "wrong-type":
						node["author"] = "owner"
					}
				}
			}
			page := routingPageData(t, s, s["edges"].([]any), false, "END")
			client := &scriptedGraphQLClient{t: t, results: []any{page, page}}
			got, transportErr := stableRoutingSnapshot(client, "owner/repo", "R_BASE", 42)
			t.Logf("transport error=%v", transportErr)
			if transportErr != nil {
				return
			}
			pr["routing_snapshot"] = got
			v, err := routingCLI(t, []any{pr}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("content=%v contract=%v engineering=%v", v["content_review_candidates"], v["review_routing_contract"], v["engineering_waiting"])
			if len(v["content_review_candidates"].([]any)) != 0 {
				t.Error("partial comment actor erased typed history and re-elected ordinary REVIEW")
			}
		})
	}
}

func TestNonceRetainedIncompleteQueue(t *testing.T) {
	s := routingGolden(t, "retained-clean")
	review60RewritePublication(t, s, func(p map[string]any) {
		a := routingObject(p["audit"])
		h := routingObject(routingObject(a["history"])["header"])
		h["labelsComplete"] = false
		projection := map[string]any{}
		for k, v := range h {
			projection[k] = v
		}
		projection["edges"] = s["edges"].([]any)[:10]
		var err error
		a["snapshot_sha256"], err = reviewrouting.Digest(projection)
		if err != nil {
			t.Fatal(err)
		}
	})
	v, err := routingCLI(t, []any{routingPullFixture(t, s, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("contract=%v fixes=%v engineering=%v", v["review_routing_contract"], v["technical_fix_candidates"], v["engineering_waiting"])
	if len(v["technical_fix_candidates"].([]any)) != 0 {
		t.Error("incomplete retained label snapshot admitted technical source")
	}
}

func TestNoncePartialAuthorRefundsSpentBudget(t *testing.T) {
	s := routingGolden(t, "two-attempts-engineering-parking")
	before, err := reviewrouting.VerifiedDepth(s, "owner")
	if err != nil || len(before.Ledger.Attempts) != 2 {
		t.Fatalf("control: %v %v", before, err)
	}
	pr := routingPullFixture(t, s, 42)
	for _, raw := range s["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		if strings.HasPrefix(routingText(node["body"]), "<!-- pp:review-routing-v1 ") {
			delete(node, "author")
		}
	}
	after, err := reviewrouting.VerifiedDepth(s, "owner")
	t.Logf("verified depth before=%d after=%d spent before=%d after=%d error=%v", before.Depth, after.Depth, len(before.Ledger.Attempts), len(after.Ledger.Attempts), err)
	v, err := routingCLI(t, []any{pr}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ordinary review=%v engineering=%v", v["content_review_candidates"], v["engineering_waiting"])
	if len(v["content_review_candidates"].([]any)) != 0 {
		t.Error("two spent attempts and engineering parking disappeared after partial actor projection")
	}
}

// Construct malformed but correctly byte-hashed server data independently of
// the strict producer. A rejected schema must reach the public reader, rather
// than being refused by the test's own producer helper before that invocation.
func review60RewritePublication(t *testing.T, state map[string]any, mutate func(map[string]any)) {
	t.Helper()
	oldContext, newContext := "", ""
	for _, raw := range state["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		body := routingText(node["body"])
		if !strings.HasPrefix(body, "<!-- pp:review-routing-v1 ") {
			continue
		}
		payload, err := reviewrouting.Parse(body)
		if err != nil {
			t.Fatal(err)
		}
		if payload["kind"] == "publication" {
			oldContext, err = reviewrouting.Context(payload)
			if err != nil {
				t.Fatal(err)
			}
			mutate(payload)
			projected := map[string]any{}
			for key, value := range payload {
				if key != "owner" {
					projected[key] = value
				}
			}
			newContext, err = reviewrouting.Digest(projected)
			if err != nil {
				t.Fatal(err)
			}
		} else if payload["context_sha256"] == oldContext {
			payload["context_sha256"] = newContext
		}
		encoded, err := reviewrouting.Canonical(payload)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(encoded)
		node["body"] = fmt.Sprintf("<!-- pp:review-routing-v1 kind=%s sha256=%x -->\n%s", payload["kind"], hash, encoded)
	}
}
