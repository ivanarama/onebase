package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Regression #2426: a complete prior current-HEAD proof exists, the first
// parent was not reviewed, and the owner requested a new full content review.
// Selecting REVIEW is not a review result or permission to merge.
func TestRequestedFullCurrentReviewWithoutParentProofGetsContentLanePublicCLI(t *testing.T) {
	for _, ship := range []bool{false, true} {
		snapshot := fullCurrentReviewHistory(t)
		labels := []any{"reviewed"}
		if ship {
			labels = append(labels, "ship")
		}
		snapshot["labels"] = labels
		edges := snapshot["edges"].([]any)
		for _, raw := range edges[:4] {
			node := routingObject(routingObject(raw)["node"])
			if body, ok := node["body"].(string); ok {
				node["body"] = strings.ReplaceAll(body, headB, headD)
			}
		}
		kept := []any{}
		for _, raw := range edges {
			node := routingObject(routingObject(raw)["node"])
			id := node["fullDatabaseId"]
			if id != "230" && id != "231" && id != "232" {
				kept = append(kept, raw)
			}
		}
		snapshot["edges"] = kept
		got := fullCurrentReviewCLI(t, snapshot)
		if len(got.ContentReviewCandidates) != 1 || got.ContentReviewCandidates[0].Number != 42 ||
			len(got.MergeExecutable) != 0 || len(got.ReviewedWaitingShip) != 0 ||
			len(got.HumanWaiting) != 0 || got.IntegrationOwner != nil {
			t.Fatalf("explicit full-review request did not enter the content lane: %+v", got)
		}
	}
}

func pendingCurrentReviewHistory(t *testing.T) map[string]any {
	t.Helper()
	snapshot := fullCurrentReviewHistory(t)
	snapshot["labels"] = []any{"reviewed"}
	kept := []any{}
	for _, raw := range snapshot["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		id := node["fullDatabaseId"]
		if id == "230" || id == "231" || id == "232" {
			continue
		}
		if id == "200" {
			node["body"] = strings.ReplaceAll(node["body"].(string), "Outcome-Label: changes-requested", "Outcome-Label: reviewed")
		}
		kept = append(kept, raw)
	}
	snapshot["edges"] = kept
	return snapshot
}

func reentryFixtureCLI(t *testing.T, snapshot map[string]any, change func(map[string]any)) map[string]any {
	t.Helper()
	pull := routingPullFixture(t, snapshot, 42)
	pull["head_parents"] = []string{headB, headC}
	if change != nil {
		change(pull)
	}
	got, err := routingCLI(t, []any{pull})
	if exit, ok := err.(*exec.ExitError); err != nil && (!ok || exit.ExitCode() != 1) {
		t.Fatalf("public fixture CLI failed: %v %+v", err, got)
	}
	return got
}

func TestReviewReentryKeepsStopLabelsAndCurrentHeadPublicCLI(t *testing.T) {
	for _, fault := range []string{"hold", "manual", "draft", "closed", "other-base", "changed-head", "needs-decision-without-override"} {
		t.Run(fault, func(t *testing.T) {
			snapshot := pendingCurrentReviewHistory(t)
			if fault == "needs-decision-without-override" {
				edges := []any{}
				for _, raw := range snapshot["edges"].([]any) {
					if routingObject(routingObject(raw)["node"])["fullDatabaseId"] != "220" {
						edges = append(edges, raw)
					}
				}
				snapshot["edges"] = edges
			}
			got := reentryFixtureCLI(t, snapshot, func(pull map[string]any) {
				switch fault {
				case "hold", "manual":
					pull["labels"] = []any{map[string]any{"name": "reviewed"}, map[string]any{"name": fault}}
				case "needs-decision-without-override":
					pull["labels"] = []any{map[string]any{"name": "reviewed"}, map[string]any{"name": "needs-decision"}}
				case "draft":
					pull["draft"] = true
				case "closed":
					pull["state"] = "closed"
				case "other-base":
					pull["base"] = map[string]any{"ref": "other"}
				case "changed-head":
					pull["head"] = map[string]any{"sha": headD}
					pull["labels"] = []any{map[string]any{"name": "reviewed"}, map[string]any{"name": "needs-decision"}}
				}
			})
			for _, field := range []string{"review_dispatch_candidates", "content_review_candidates", "merge_candidates", "merge_executable", "reviewed_waiting_ship"} {
				if len(got[field].([]any)) != 0 {
					t.Fatalf("%s reopened stopped or different HEAD through %s: %+v", fault, field, got)
				}
			}
		})
	}
}

func TestReviewReentryCannotUseQuotedForeignOrEditedOverridePublicCLI(t *testing.T) {
	for _, fault := range []string{"quoted", "fenced", "inline", "foreign", "edited", "missing", "before-current-proof"} {
		t.Run(fault, func(t *testing.T) {
			snapshot := pendingCurrentReviewHistory(t)
			for _, raw := range snapshot["edges"].([]any) {
				node := routingObject(routingObject(raw)["node"])
				id := node["fullDatabaseId"]
				if id == "100" || id == "101" || id == "102" {
					node["body"] = strings.ReplaceAll(node["body"].(string), headB, headD)
				}
				if id != "220" {
					continue
				}
				switch fault {
				case "quoted":
					node["body"] = "> pp:review-again"
				case "fenced":
					node["body"] = "```\npp:review-again\n```"
				case "inline":
					node["body"] = "The instruction is `pp:review-again`."
				case "foreign":
					node["author"] = map[string]any{"login": "contributor"}
				case "edited":
					node["lastEditedAt"] = "2026-10-29T00:22:01Z"
				case "missing":
					node["body"] = "No new request."
				case "before-current-proof":
					node["fullDatabaseId"], node["createdAt"] = "190", "2026-10-29T00:19:00Z"
				}
			}
			got := fullCurrentReviewCLI(t, snapshot)
			if len(got.ContentReviewCandidates) != 0 || len(got.MergeExecutable) != 0 || len(got.HumanWaiting) != 1 || got.IntegrationOwner != nil {
				t.Fatalf("%s override reopened an unproved lineage: %+v", fault, got)
			}
		})
	}
}

func TestReviewReentryRejectsIncompleteParentProofPublicCLI(t *testing.T) {
	for _, fault := range []string{"missing-review", "missing-claim", "edited-review", "edited-claim", "foreign-review", "wrong-review-head", "wrong-claim-head", "wrong-claim-epoch"} {
		t.Run(fault, func(t *testing.T) {
			snapshot := pendingCurrentReviewHistory(t)
			kept := []any{}
			for _, raw := range snapshot["edges"].([]any) {
				node := routingObject(routingObject(raw)["node"])
				id := node["fullDatabaseId"]
				if id == "220" || fault == "missing-review" && id == "100" || fault == "missing-claim" && id == "101" {
					continue
				}
				if id == "100" {
					switch fault {
					case "edited-review":
						node["lastEditedAt"] = "2026-10-29T00:10:01Z"
					case "foreign-review":
						node["author"] = map[string]any{"login": "contributor"}
					case "wrong-review-head":
						node["body"] = strings.ReplaceAll(node["body"].(string), headB, headD)
					}
				}
				if id == "101" {
					switch fault {
					case "edited-claim":
						node["lastEditedAt"] = "2026-10-29T00:10:01Z"
					case "wrong-claim-head":
						node["body"] = strings.ReplaceAll(node["body"].(string), headB, headD)
					case "wrong-claim-epoch":
						node["body"] = strings.ReplaceAll(node["body"].(string), "epoch-sha256=", "epoch-sha256=0")
					}
				}
				kept = append(kept, raw)
			}
			snapshot["edges"] = kept
			got := fullCurrentReviewCLI(t, snapshot)
			if len(got.ReviewedWaitingShip) != 0 || len(got.MergeExecutable) != 0 || len(got.HumanWaiting) != 1 || got.IntegrationOwner != nil {
				t.Fatalf("%s source proof was accepted for ship: %+v", fault, got)
			}
		})
	}
}

func TestReviewReentryTrustedOverrideCanUnparkNeedsDecisionPublicCLI(t *testing.T) {
	snapshot := pendingCurrentReviewHistory(t)
	snapshot["labels"] = []any{"reviewed", "needs-decision"}
	got := fullCurrentReviewCLI(t, snapshot)
	if len(got.ContentReviewCandidates) != 1 || len(got.MergeExecutable) != 0 || got.IntegrationOwner != nil {
		t.Fatalf("trusted new full-review request did not unpark the review: %+v", got)
	}
}

// Regression #2352: a negative committed review of the first parent is not
// positive source proof, even if the merge HEAD has a later reviewed result.
func TestNegativeParentReviewNeverInvitesShipOrOwnsMergePublicCLI(t *testing.T) {
	for _, ship := range []bool{false, true} {
		snapshot := fullCurrentReviewHistory(t)
		labels := []any{"reviewed"}
		if ship {
			labels = append(labels, "ship")
		}
		snapshot["labels"] = labels
		edges := snapshot["edges"].([]any)
		kept := []any{}
		for _, raw := range edges {
			node := routingObject(routingObject(raw)["node"])
			id := node["fullDatabaseId"]
			if id == "220" || id == "230" || id == "231" || id == "232" {
				continue
			}
			if id == "100" {
				node["body"] = strings.ReplaceAll(node["body"].(string), "Outcome-Label: reviewed", "Outcome-Label: changes-requested")
			}
			if id == "200" {
				node["body"] = strings.ReplaceAll(node["body"].(string), "Outcome-Label: changes-requested", "Outcome-Label: reviewed")
			}
			kept = append(kept, raw)
		}
		snapshot["edges"] = kept
		got := fullCurrentReviewCLI(t, snapshot)
		if len(got.ReviewedWaitingShip) != 0 || len(got.MergeExecutable) != 0 ||
			len(got.HumanWaiting) != 1 || got.HumanWaiting[0].Stage != "legacy-source-proof-missing" ||
			got.IntegrationOwner != nil {
			t.Fatalf("negative source review acquired ship or merge route: %+v", got)
		}
	}
}
