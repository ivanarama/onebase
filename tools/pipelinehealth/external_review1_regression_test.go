package main

import (
	"testing"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

// Reproduce canonical public review #2452 / 6555284057 through the real CLI.
func TestPublicTypedReviewedIgnoresQuotedReviewAgain(t *testing.T) {
	for _, test := range []struct {
		name, body string
		override   bool
	}{
		{"plain", "pp:review-again", true},
		{"backticks", "Example:\n```text\npp:review-again\n```", false},
		{"tildes", "Example:\n~~~text\npp:review-again\n~~~", false},
		{"unclosed", "Example:\n````text\npp:review-again\n```", false},
		{"real-after-example", "```text\npp:review-again\n```\npp:review-again", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := routingGolden(t, "retained-clean")
			review60RewritePublication(t, state, func(payload map[string]any) {
				report := routingObject(payload["report"])
				report["blocking"] = []any{}
				var err error
				payload["report_sha256"], err = reviewrouting.Digest(report)
				if err != nil {
					t.Fatal(err)
				}
				payload["outcome"] = "reviewed"
			})
			control, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
			if err != nil || len(control["reviewed_waiting_ship"].([]any)) != 1 {
				t.Fatalf("positive reviewed control: %v %v", control, err)
			}
			state["edges"] = append(state["edges"].([]any), map[string]any{"cursor": "EXAMPLE", "node": map[string]any{
				"__typename": "IssueComment", "id": "IC_EXAMPLE", "fullDatabaseId": "99999", "createdAt": "2026-10-30T00:00:00Z",
				"lastEditedAt": nil, "author": map[string]any{"login": "owner"}, "body": test.body}})
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
			if err != nil {
				t.Fatal(err)
			}
			if (len(value["content_review_candidates"].([]any)) == 1) != test.override || (len(value["reviewed_waiting_ship"].([]any)) == 1) == test.override {
				t.Fatalf("quoted command changed the epoch: %v", value)
			}
		})
	}
}

func TestPublicUnfinishedPublicationCountersMustMatchHistory(t *testing.T) {
	for _, field := range []string{"valid", "depth", "budget_used"} {
		t.Run(field, func(t *testing.T) {
			state := routingGolden(t, "unfinished-valid")
			if field != "valid" {
				review60RewritePublication(t, state, func(payload map[string]any) {
					if field == "depth" {
						payload[field] = int64(3)
					} else {
						payload[field] = int64(1)
					}
				})
			}
			pulls := []any{routingPullFixture(t, state, 42)}
			_, inspectErr := routingCLI(t, pulls, "-routing-inspect", "42")
			if (inspectErr == nil) != (field == "valid") {
				t.Fatalf("unfinished %s inspection error: %v", field, inspectErr)
			}
			value, err := routingCLI(t, pulls, "-technical-handoff", "-contract", routingSkillFixture(t, true))
			if err != nil {
				t.Fatal(err)
			}
			if (len(value["content_review_candidates"].([]any)) == 1) != (field == "valid") || (len(value["engineering_waiting"].([]any)) == 1) == (field == "valid") {
				t.Fatalf("unfinished %s acquired an invalid recovery route: %v", field, value)
			}
		})
	}
}

func TestPublicLegacyHistoryKeepsFencedExampleBetweenReviewAndClaim(t *testing.T) {
	state := routingGolden(t, "two-legacy-rounds")
	edges := state["edges"].([]any)
	for index, raw := range edges {
		node := routingObject(routingObject(raw)["node"])
		if _, ok := node["body"]; !ok {
			continue
		}
		example := map[string]any{"cursor": "EXAMPLE", "node": map[string]any{
			"__typename": "IssueComment", "id": "IC_EXAMPLE", "fullDatabaseId": "99999", "createdAt": "2026-10-29T00:00:00Z",
			"lastEditedAt": nil, "author": map[string]any{"login": "owner"}, "body": "Example:\n```text\npp:review-again\n```"}}
		updated := append([]any{}, edges[:index+1]...)
		updated = append(updated, example)
		state["edges"] = append(updated, edges[index+1:]...)
		break
	}
	value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-routing-inspect", "42")
	if err != nil || value["verified_depth"] != float64(2) {
		t.Fatalf("fenced example destroyed historical proof: %v %v", value, err)
	}
}
