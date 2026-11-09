package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

// A merge-shaped HEAD has an older source review, a negative current review,
// and a new, explicitly requested full current-HEAD review. There is no carry.
func fullCurrentReviewHistory(t *testing.T) map[string]any {
	t.Helper()
	snapshot := routingClone(t, routingGolden(t, "two-legacy-rounds"))
	snapshot["labels"] = []any{"reviewed", "ship"}
	edges := []any{}
	commit := func(id, head string) {
		edges = append(edges, map[string]any{"cursor": "C" + id, "node": map[string]any{
			"__typename": "PullRequestCommit", "id": id, "commit": map[string]any{"oid": head}}})
	}
	comment := func(id int, body string) {
		edges = append(edges, map[string]any{"cursor": fmt.Sprint("C", id), "node": map[string]any{
			"__typename": "IssueComment", "id": fmt.Sprint("IC_", id), "fullDatabaseId": fmt.Sprint(id),
			"createdAt": fmt.Sprintf("2026-10-29T00:%02d:00Z", id/10), "lastEditedAt": nil,
			"author": map[string]any{"login": "owner"}, "body": body}})
	}
	proof := func(id int, head, anchor, outcome string) {
		sum := sha256.Sum256([]byte("pp-review-epoch-v1\nhead=" + head + "\nanchor-node=" + anchor + "\n"))
		epoch := hex.EncodeToString(sum[:])
		comment(id, "Reviewed-SHA: "+head+"\nOutcome-Label: "+outcome+"\n<!-- pp:review pp:tail=0 -->")
		comment(id+1, fmt.Sprintf("<!-- pp:review-claim %s review-comment=%d epoch-sha256=%s -->", head, id, epoch))
		comment(id+2, fmt.Sprintf("<!-- pp:head-reviewed %s review-comment=%d claim=%d epoch-sha256=%s -->", head, id, id+1, epoch))
	}
	commit("SOURCE", headB)
	proof(100, headB, "SOURCE", "reviewed")
	commit("CURRENT", headA)
	proof(200, headA, "CURRENT", "changes-requested")
	comment(220, "Request a fresh full content review.\n\npp:review-again")
	proof(230, headA, "IC_220", "reviewed")
	snapshot["edges"] = edges
	return snapshot
}

func fullCurrentReviewCLI(t *testing.T, snapshot map[string]any, expectedCalls ...int) report {
	t.Helper()
	comments := []any{}
	for _, raw := range snapshot["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		if node["__typename"] != "IssueComment" {
			continue
		}
		updated := node["createdAt"]
		if node["lastEditedAt"] != nil {
			updated = node["lastEditedAt"]
		}
		comments = append(comments, map[string]any{"fullDatabaseId": node["fullDatabaseId"], "createdAt": node["createdAt"],
			"updatedAt": updated, "author": node["author"], "body": node["body"]})
	}
	labels := []any{}
	for _, name := range snapshot["labels"].([]any) {
		labels = append(labels, map[string]any{"name": name})
	}
	pull := gqlTestPullNode("PR_NODE_42", 42, headA, labels, comments)
	pull["headRefName"] = routingObject(snapshot["identity"])["head_ref"]
	pull["headRepository"] = map[string]any{"id": "R_BASE", "nameWithOwner": "owner/repo"}
	pull["baseRepository"] = pull["headRepository"]
	pull["baseRefOid"] = headB
	pull["isCrossRepository"] = false
	queue := snapshotResult(1, []any{pull}, false, nil, 0, nil, false, nil)
	repository := routingObject(queue["repository"])
	repository["id"], repository["nameWithOwner"] = "R_BASE", "owner/repo"
	timeline := routingPageData(t, snapshot, snapshot["edges"].([]any), false, "END")
	routingObject(routingObject(timeline["repository"])["pullRequest"])["labels"] = map[string]any{
		"nodes": labels, "pageInfo": map[string]any{"hasNextPage": false}}
	pages := []any{
		map[string]any{"data": queue},
		map[string]any{"data": map[string]any{"nodes": []any{map[string]any{
			"__typename": "Commit", "id": "commit-PR_NODE_42", "oid": headA,
			"parents": connection(2, []any{map[string]any{"oid": headB}, map[string]any{"oid": headC}}, false, nil)}}}},
		map[string]any{"data": map[string]any{"nodes": []any{map[string]any{
			"__typename": "PullRequest", "id": "PR_NODE_42", "headRefOid": headA}}}},
		map[string]any{"data": timeline}, map[string]any{"data": timeline},
	}
	data, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	sequence := filepath.Join(t.TempDir(), "pages.json")
	if err := os.WriteFile(sequence, data, 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Both executables and all responses are owned test fixtures.
	command := exec.Command(pipelinehealthCLI(t), "-transport", "graphql", "-repo", "owner/repo", "-owner", "owner", "-json")
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_SEQUENCE="+sequence)
	output, err := command.CombinedOutput()
	// -1 is used only for the explicit unresolved protocol-actor case: a
	// complete public read must refuse it, never turn it into ordinary work.
	if len(expectedCalls) == 1 && expectedCalls[0] == -1 {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(output), "actor") {
			t.Fatalf("unresolved actor did not fail the public read closed: %v\n%s", err, output)
		}
		return report{State: "read-refused"}
	}
	if exit, ok := err.(*exec.ExitError); err != nil && (!ok || exit.ExitCode() != 1 || !json.Valid(output)) {
		t.Fatalf("public CLI failed: %v\n%s", err, output)
	}
	var got report
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode public report: %v\n%s", err, output)
	}
	if len(expectedCalls) > 0 {
		counter, err := os.ReadFile(sequence + ".index")
		if err != nil || string(counter) != fmt.Sprint(expectedCalls[0]) {
			t.Fatalf("complete two-pass epoch was not read: calls=%q err=%v", counter, err)
		}
	}
	return got
}

func TestFullCurrentReviewOverridesMergeShapePublicCLI(t *testing.T) {
	for _, historicalHandoff := range []bool{false, true} {
		snapshot := fullCurrentReviewHistory(t)
		if historicalHandoff {
			// A prior negative FIX handoff was superseded by the new explicit
			// review epoch; it must not be mistaken for a current carry handoff.
			node := routingObject(routingObject(snapshot["edges"].([]any)[5])["node"])
			node["body"] = "Historical <!-- pp:fix-handoff diagnostic -->\n" + node["body"].(string)
		}
		got := fullCurrentReviewCLI(t, snapshot, 5)
		if got.IntegrationOwner != nil || len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Stage != "merge" ||
			len(got.ReviewCandidates) != 0 || hasFinding(got, "legacy_source_review_missing") {
			t.Fatalf("explicit closed full current review was mistaken for legacy carry: %+v", got)
		}
	}
}

func TestUnprovedFullCurrentReviewKeepsLegacyLanePublicCLI(t *testing.T) {
	for _, fault := range []string{"no-override", "quoted", "fenced", "foreign", "edited", "deleted", "stale-before-head", "deleted-after-proof", "edited-source",
		"missing-review", "missing-claim", "missing-completion", "wrong-epoch", "losing-claim", "negative-outcome",
		"proof-before-override", "genuine-carry", "carry-intent", "carry-done", "carry-aborted", "typed-handoff", "new-fix-handoff"} {
		t.Run(fault, func(t *testing.T) {
			snapshot := fullCurrentReviewHistory(t)
			edges := snapshot["edges"].([]any)
			for index, raw := range edges {
				node := routingObject(routingObject(raw)["node"])
				id := node["fullDatabaseId"]
				if fault == "edited-source" && id == "100" {
					node["lastEditedAt"] = "2026-10-29T00:10:01Z"
				}
				if id == "220" {
					switch fault {
					case "no-override":
						node["body"] = "No new request."
					case "quoted":
						node["body"] = "> pp:review-again"
					case "fenced":
						node["body"] = "```\npp:review-again\n```"
					case "foreign":
						node["author"] = map[string]any{"login": "contributor"}
					case "edited":
						node["lastEditedAt"] = "2026-10-29T00:22:01Z"
					case "deleted":
						edges[index] = map[string]any{"cursor": "DELETED", "node": map[string]any{
							"id": "DELETED", "__typename": "CommentDeletedEvent", "createdAt": "2026-10-29T00:22:01Z"}}
					case "stale-before-head":
						edges = append(append(append([]any{}, edges[:4]...), raw), append(edges[4:index], edges[index+1:]...)...)
					case "proof-before-override":
						edges = append(append(append([]any{}, edges[:index]...), edges[index+1:]...), raw)
					case "carry-intent", "carry-done", "carry-aborted":
						marker := map[string]string{"carry-intent": "pp:base-sync-intent", "carry-done": "pp:base-sync-done",
							"carry-aborted": "pp:base-sync-v1-aborted"}[fault]
						node["body"] = "pp:review-again\n<!-- " + marker + " malformed -->"
					}
				}
				if fault == "missing-review" && id == "230" || fault == "missing-claim" && id == "231" || fault == "missing-completion" && id == "232" {
					node["body"] = "Unavailable proof part."
				}
				if fault == "wrong-epoch" && id == "232" {
					node["body"] = strings.ReplaceAll(node["body"].(string), "epoch-sha256=", "epoch-sha256=0")
				}
				if fault == "losing-claim" && id == "232" {
					node["body"] = strings.ReplaceAll(node["body"].(string), "claim=231", "claim=201")
				}
				if fault == "negative-outcome" && id == "230" {
					node["body"] = strings.ReplaceAll(node["body"].(string), "Outcome-Label: reviewed", "Outcome-Label: changes-requested")
				}
			}
			if fault == "new-fix-handoff" || fault == "typed-handoff" {
				body := "<!-- pp:fix-handoff unknown -->"
				if fault == "typed-handoff" {
					body = "<!-- pp:review-routing-v1 malformed -->"
				}
				edges = append(edges, map[string]any{"cursor": "C240", "node": map[string]any{
					"__typename": "IssueComment", "id": "IC_240", "fullDatabaseId": "240", "createdAt": "2026-10-29T00:24:00Z",
					"lastEditedAt": nil, "author": map[string]any{"login": "owner"}, "body": body}})
			}
			if fault == "deleted-after-proof" {
				edges = append(edges, map[string]any{"cursor": "DELETED", "node": map[string]any{
					"id": "DELETED", "__typename": "CommentDeletedEvent", "createdAt": "2026-10-29T00:24:00Z"}})
			}
			if fault == "genuine-carry" {
				comment := func(id int, body string) any {
					return map[string]any{"cursor": fmt.Sprint("C", id), "node": map[string]any{
						"__typename": "IssueComment", "id": fmt.Sprint("IC_", id), "fullDatabaseId": fmt.Sprint(id),
						"createdAt": fmt.Sprintf("2026-10-29T00:%02d:00Z", id/10), "lastEditedAt": nil,
						"author": map[string]any{"login": "owner"}, "body": body}}
				}
				intent := fmt.Sprintf("<!-- pp:base-sync-intent from=%s base=%s review-comment=100 claim=101 completion=102 ship-event=LE_source previous=none -->", headB, headC)
				done := fmt.Sprintf("<!-- pp:base-sync-done intent=150 from=%s to=%s base=%s previous=none ship-event=LE_source -->", headB, headA, headC)
				edges = append(append(append([]any{}, edges[:4]...), comment(150, intent), edges[4], comment(190, done)), edges[5:]...)
			}
			snapshot["edges"] = edges
			got := fullCurrentReviewCLI(t, snapshot)
			if fault == "genuine-carry" && (got.IntegrationOwner == nil || got.IntegrationOwner.Stage != "integration-merge-ready") {
				t.Fatalf("genuine carry lost its existing integration lane: %+v", got)
			}
			for _, candidate := range got.MergeExecutable {
				if candidate.Stage == "merge" {
					t.Fatalf("%s incorrectly authorized ordinary classification: %+v", fault, got)
				}
			}
		})
	}
}

func TestFullCurrentReviewWithoutShipWaitsForHumanPublicCLI(t *testing.T) {
	snapshot := fullCurrentReviewHistory(t)
	snapshot["labels"] = []any{"reviewed"}
	// The old source review is not a parent proof. This does not invalidate a
	// fresh full current-HEAD content review or supply the missing human ship.
	for _, raw := range snapshot["edges"].([]any)[:4] {
		node := routingObject(routingObject(raw)["node"])
		if body, ok := node["body"].(string); ok {
			node["body"] = strings.ReplaceAll(body, headB, headD)
		}
	}
	got := fullCurrentReviewCLI(t, snapshot, 5)
	if len(got.ReviewedWaitingShip) != 1 || len(got.MergeExecutable) != 0 || len(got.HumanWaiting) != 0 || got.IntegrationOwner != nil {
		t.Fatalf("full current review invented ship or required a legacy parent proof: %+v", got)
	}
}

func TestRESTFullCurrentReviewRemainsConservativePublicCLI(t *testing.T) {
	snapshot := fullCurrentReviewHistory(t)
	pull := routingPullFixture(t, snapshot, 42)
	pull["head_parents"] = []string{headB, headC}
	// Neither a normalized offline snapshot nor a supplied private flag may
	// impersonate the live complete GraphQL reader in the ordinary REST path.
	pull["FullCurrentReview"] = true
	got, err := routingCLI(t, []any{pull})
	if exit, ok := err.(*exec.ExitError); err != nil && (!ok || exit.ExitCode() != 1) {
		t.Fatalf("REST CLI failed: %v %+v", err, got)
	}
	owner := routingObject(got["integration_owner"])
	if owner["stage"] != "legacy-integration-merge-ready" {
		t.Fatalf("REST fixture gained unproved ordinary classification: %+v", got)
	}
}

func fullReviewWireExample(t *testing.T) string {
	t.Helper()
	records, _, err := reviewrouting.ReadRecords(routingGolden(t, "third-typed-round"), "owner")
	if err != nil || len(records) == 0 {
		t.Fatalf("genuine shared wire fixture unavailable: %v", err)
	}
	wire, err := reviewrouting.Render(records[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestFullCurrentReviewRoutingMentionsStayOrdinaryPublicCLI(t *testing.T) {
	wire := fullReviewWireExample(t)
	for name, body := range map[string]string{
		"prose":           "This explanation mentions the pp:review-routing- namespace.",
		"inline":          "The review quotes `" + wire + "` without creating a record.",
		"fenced":          "```json\n" + wire + "\n```",
		"unclosed-fence":  "~~~json\n" + wire,
		"quoted":          "> " + wire,
		"review-consumer": "<!-- pp:review-routing-v1-consumer stage=review -->",
		"fix-consumer":    "<!-- pp:review-routing-v1-consumer stage=fix -->",
		"merge-consumer":  "<!-- pp:review-routing-v1-consumer stage=merge -->",
		"tail-consumer":   "<!-- pp:review-routing-v1-consumer stage=tail -->",
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := fullCurrentReviewHistory(t)
			routingMentionComment(snapshot, body)
			got := fullCurrentReviewCLI(t, snapshot, 5)
			if got.IntegrationOwner != nil || len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Stage != "merge" {
				t.Fatalf("ordinary mention revived the legacy integration lane: %+v", got)
			}
		})
	}
}

func TestFullCurrentReviewActualRoutingRecordsStayClosedPublicCLI(t *testing.T) {
	wire := fullReviewWireExample(t)
	for _, fault := range []string{"genuine", "unknown-version", "malformed", "leading-space", "leading-BOM", "consumer-unknown-stage", "consumer-extra-attribute", "edited", "deleted", "unresolved-actor"} {
		t.Run(fault, func(t *testing.T) {
			body := wire
			switch fault {
			case "unknown-version":
				body = strings.Replace(wire, "routing-v1", "routing-v2", 1)
			case "malformed":
				body = "<!-- pp:review-routing-v1 malformed -->"
			case "leading-space":
				body = " " + wire
			case "leading-BOM":
				body = "\ufeff" + wire
			case "consumer-unknown-stage":
				body = "<!-- pp:review-routing-v1-consumer stage=unknown -->"
			case "consumer-extra-attribute":
				body = "<!-- pp:review-routing-v1-consumer stage=review extra=yes -->"
			}
			snapshot := fullCurrentReviewHistory(t)
			routingMentionComment(snapshot, body)
			edges := snapshot["edges"].([]any)
			node := routingObject(routingObject(edges[len(edges)-1])["node"])
			switch fault {
			case "edited":
				node["lastEditedAt"] = "2026-10-29T00:25:00Z"
			case "deleted":
				snapshot["edges"] = append(edges, map[string]any{"cursor": "DELETED", "node": map[string]any{
					"id": "DELETED", "__typename": "CommentDeletedEvent", "createdAt": "2026-10-29T00:25:00Z"}})
			case "unresolved-actor":
				node["author"] = nil
			}
			var got report
			if fault == "unresolved-actor" {
				got = fullCurrentReviewCLI(t, snapshot, -1)
			} else {
				got = fullCurrentReviewCLI(t, snapshot)
			}
			if len(got.MergeExecutable) != 0 {
				t.Fatalf("actual/unknown record acquired ordinary execution: %+v", got)
			}
		})
	}
}
