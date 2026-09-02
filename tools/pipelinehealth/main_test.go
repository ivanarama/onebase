package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	epoch = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func testPR(number int, head string, labels ...string) apiPull {
	item := apiPull{Number: number, Title: "PR", HTMLURL: "https://example.test/pr", State: "open"}
	item.Head.SHA = head
	item.Base.Ref = "main"
	for _, name := range labels {
		item.Labels = append(item.Labels, apiLabel{Name: name})
	}
	return item
}

func addComment(item apiPull, id int64, body string) apiPull {
	timestamp := fmt.Sprintf("2026-08-31T12:%02d:00Z", id%60)
	item.Comments = append(item.Comments, apiComment{
		ID: id, CreatedAt: timestamp, UpdatedAt: timestamp,
		User: apiUser{Login: "ivanarama"}, Body: body,
	})
	return item
}

func completion(head string, reviewID, claimID int64) string {
	return fmt.Sprintf("<!-- pp:head-reviewed %s review-comment=%d claim=%d epoch-sha256=%s -->", head, reviewID, claimID, epoch)
}

func syncIntent(from string, reviewID, claimID, completionID int64) string {
	return fmt.Sprintf("<!-- pp:base-sync-intent from=%s base=%s review-comment=%d claim=%d completion=%d ship-event=LE_test previous=none -->",
		from, headB, reviewID, claimID, completionID)
}

func syncDone(intentID int64, from, to string) string {
	return fmt.Sprintf("<!-- pp:base-sync-done intent=%d from=%s to=%s base=%s previous=none ship-event=LE_test -->",
		intentID, from, to, headB)
}

func hasFinding(result report, code string) bool {
	for _, item := range result.Findings {
		if item.Code == code {
			return true
		}
	}
	return false
}

func TestFreshPRSortsBeforeOlderNumberWithReviewHistory(t *testing.T) {
	old := addComment(testPR(10, headA), 30, completion(headB, 20, 25))
	fresh := testPR(99, headB)

	got := analyze([]apiPull{old, fresh}, "ivanarama")
	if len(got.ReviewCandidates) != 2 {
		t.Fatalf("review candidates: %+v", got.ReviewCandidates)
	}
	if got.ReviewCandidates[0].Number != 99 || got.ReviewCandidates[0].Depth != 0 {
		t.Fatalf("fresh PR was starved by an older number: %+v", got.ReviewCandidates)
	}
}

func TestEqualDepthUsesNumberAsDeterministicTieBreaker(t *testing.T) {
	got := analyze([]apiPull{testPR(20, headA), testPR(10, headB)}, "ivanarama")
	if got.ReviewCandidates[0].Number != 10 || got.ReviewCandidates[1].Number != 20 {
		t.Fatalf("unexpected tie-break: %+v", got.ReviewCandidates)
	}
}

func TestCompletionRetryForSameReviewIsNotDuplicateAudit(t *testing.T) {
	item := addComment(testPR(10, headA, "reviewed"), 30, completion(headA, 20, 25))
	item = addComment(item, 31, completion(headA, 20, 25))

	got := analyze([]apiPull{item}, "ivanarama")
	if hasFinding(got, "same_head_reviewed_twice") || reviewDepth(item.Comments, "ivanarama") != 1 {
		t.Fatalf("idempotent retry counted twice: %+v", got)
	}
}

func TestTwoDifferentCompletionsWithoutOverrideAreRed(t *testing.T) {
	item := addComment(testPR(10, headA, "reviewed"), 30, completion(headA, 20, 25))
	item = addComment(item, 40, completion(headA, 35, 36))

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "same_head_reviewed_twice") {
		t.Fatalf("duplicate audit was not diagnosed: %+v", got)
	}
}

func TestOverrideStartsAnotherReviewEpoch(t *testing.T) {
	item := addComment(testPR(10, headA, "reviewed"), 30, completion(headA, 20, 25))
	item = addComment(item, 31, "pp:review-again")

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 10 {
		t.Fatalf("human override did not return PR to REVIEW: %+v", got)
	}
}

func TestUnfinishedClaimIsVisibleImmediately(t *testing.T) {
	marker := fmt.Sprintf("<!-- pp:review-claim %s review-comment=20 epoch-sha256=%s -->", headA, epoch)
	item := addComment(testPR(10, headA), 25, marker)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "unfinished_review_transaction") {
		t.Fatalf("unfinished transaction was hidden: %+v", got)
	}
}

func TestCompletedBaseSyncWithShipIsFirstReviewCandidate(t *testing.T) {
	ordinary := testPR(1, headA)
	carried := addComment(testPR(99, headB, "ship", "reviewed"), 30, syncIntent(headA, 10, 20, 25))
	carried = addComment(carried, 31, syncDone(30, headA, headB))

	got := analyze([]apiPull{ordinary, carried}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 99 ||
		got.ReviewCandidates[0].Stage != "integration-review" {
		t.Fatalf("base-sync review did not get priority: %+v", got.ReviewCandidates)
	}
	if !hasFinding(got, "base_sync_waiting_review") {
		t.Fatalf("base-sync wait is invisible: %+v", got)
	}
}

func TestBaseSyncIntentWithoutDoneIsMergeRecoveryNotReview(t *testing.T) {
	item := addComment(testPR(99, headA, "ship"), 30, syncIntent(headA, 10, 20, 25))

	got := analyze([]apiPull{testPR(1, headB), item}, "ivanarama")
	if len(got.ReviewCandidates) != 0 || !hasFinding(got, "base_sync_recovery") ||
		!hasFinding(got, "single_flight_barrier") {
		t.Fatalf("unfinished base-sync was not routed to MERGE recovery: %+v", got)
	}
}

func TestCompletedIntegrationReviewKeepsBarrierUntilMerge(t *testing.T) {
	owner := addComment(testPR(20, headB, "ship", "reviewed"), 30, syncIntent(headA, 10, 20, 25))
	owner = addComment(owner, 31, syncDone(30, headA, headB))
	owner = addComment(owner, 40, completion(headB, 35, 36))
	wouldBeNext := addComment(testPR(30, headB, "ship"), 41, completion(headA, 37, 38))

	got := analyze([]apiPull{wouldBeNext, owner}, "ivanarama")
	if len(got.ReviewCandidates) != 0 || !hasFinding(got, "base_sync_waiting_merge") ||
		!hasFinding(got, "single_flight_barrier") {
		t.Fatalf("reviewed owner released the barrier before merge: %+v", got)
	}
}

func TestLegacyReShipIsVisibleAsPriorityValidationCandidate(t *testing.T) {
	ordinary := testPR(1, headA)
	legacy := addComment(testPR(99, headB, "ship"), 30, completion(headA, 20, 25))

	got := analyze([]apiPull{ordinary, legacy}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 99 ||
		got.ReviewCandidates[0].Stage != "legacy-integration-review" {
		t.Fatalf("legacy re-ship validation did not get priority: %+v", got.ReviewCandidates)
	}
	if !hasFinding(got, "legacy_ship_waiting_review_validation") {
		t.Fatalf("legacy re-ship is invisible: %+v", got)
	}
}

func TestBaseAdvanceBetweenIntentAndDoneIsVisible(t *testing.T) {
	item := addComment(testPR(77, headB, "ship"), 30,
		fmt.Sprintf("<!-- pp:base-sync-intent from=%s base=%s review-comment=20 claim=25 completion=29 ship-event=LE_test previous=none -->", headA, headA))
	item = addComment(item, 31,
		fmt.Sprintf("<!-- pp:base-sync-done intent=30 from=%s to=%s base=%s previous=none ship-event=LE_test -->", headA, headB, headB))
	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "base_sync_base_advanced") {
		t.Fatalf("base advance between intent and done is invisible: %+v", got)
	}
}

func TestSingleFlightExposesOnlyFirstIntegrationReview(t *testing.T) {
	first := addComment(testPR(20, headB, "ship"), 30, completion(headA, 20, 25))
	second := addComment(testPR(30, headB, "ship"), 31, completion(headA, 21, 26))
	ordinary := testPR(1, headA)

	got := analyze([]apiPull{second, ordinary, first}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 20 {
		t.Fatalf("single-flight owner is not exclusive: %+v", got.ReviewCandidates)
	}
	if !hasFinding(got, "single_flight_barrier") {
		t.Fatalf("single-flight barrier is invisible: %+v", got)
	}
}

func TestSingleFlightOwnerUsesNumberInsteadOfReviewDepth(t *testing.T) {
	items := []candidate{
		{Number: 30, Depth: 0, Stage: "integration-review"},
		{Number: 20, Depth: 9, Stage: "legacy-integration-review"},
	}
	sortCandidates(items)
	if items[0].Number != 20 {
		t.Fatalf("single-flight owner must be the earliest PR number: %+v", items)
	}
}

func TestOrdinaryCandidatesUsePriorityBeforeReviewDepth(t *testing.T) {
	items := []candidate{
		{Number: 10, Depth: 0, Stage: "review", Priority: 2},
		{Number: 30, Depth: 8, Stage: "review", Priority: 0},
		{Number: 20, Depth: 1, Stage: "integration-review", Priority: 3},
	}
	sortCandidates(items)
	if items[0].Number != 20 || items[1].Number != 30 {
		t.Fatalf("safety must win, then queue priority: %+v", items)
	}
}

func TestQueuePriorityUsesManualLabelAndAging(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	priority, source := queuePriority(map[string]bool{"bug": true, "queue:p3": true}, "2026-09-02T00:00:00Z", now)
	if priority != 3 || !strings.HasPrefix(source, "manual:") {
		t.Fatalf("manual priority did not override classification: %d %s", priority, source)
	}
	priority, _ = queuePriority(map[string]bool{"enhancement": true}, "2026-08-19T00:00:00Z", now)
	if priority != 1 {
		t.Fatalf("aging did not prevent starvation: %d", priority)
	}
}

func TestShipWithoutReviewHistoryIsNotLegacyCandidate(t *testing.T) {
	got := analyze([]apiPull{testPR(99, headB, "ship")}, "ivanarama")
	if len(got.ReviewCandidates) != 0 || hasFinding(got, "legacy_ship_waiting_review_validation") {
		t.Fatalf("ordinary ship was treated as legacy reauthorization: %+v", got)
	}
}

func TestShipOnUnmarkedAuthorPushIsNotCarriedIntoReview(t *testing.T) {
	item := addComment(testPR(99, headB, "ship"), 30, syncIntent(headA, 10, 20, 25))
	item = addComment(item, 31, syncDone(30, headA, headA))

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 0 || hasFinding(got, "base_sync_waiting_review") {
		t.Fatalf("arbitrary new HEAD inherited ship: %+v", got)
	}
}

func testIssue(number int, comments ...apiComment) apiIssue {
	return apiIssue{Number: number, Title: "Issue", HTMLURL: "https://example.test/issue", State: "open", Thread: comments}
}

func issueComment(id int64, body string) apiComment {
	timestamp := fmt.Sprintf("2026-09-01T10:%02d:00Z", id%60)
	return apiComment{ID: id, CreatedAt: timestamp, UpdatedAt: timestamp,
		User: apiUser{Login: "ivanarama"}, Body: body}
}

func TestMojibakeInTriageVisibleTextIsRed(t *testing.T) {
	broken := issueComment(10, "**РўСЂРёР°Р¶.**\nРљРѕСЂРµРЅСЊ РЅР°Р№РґРµРЅ.\n<!-- pp:triage -->\npp-triage-route-v1")
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{testIssue(1281, broken)}, "ivanarama")
	result.finish()

	if result.State != "red" || !hasFinding(result, "triage_text_mojibake") {
		t.Fatalf("broken human-facing triage was not diagnosed: %+v", result)
	}
}

func TestDisplayRepairMarkerResolvesMojibakeFinding(t *testing.T) {
	broken := issueComment(10, "**РўСЂРёР°Р¶.**\nРљРѕСЂРµРЅСЊ РЅР°Р№РґРµРЅ.\n<!-- pp:triage -->")
	repair := issueComment(20, "Исправление опубликовано выше.\n<!-- pp:display-repair comment=10 -->")
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{testIssue(1281, broken, repair)}, "ivanarama")
	result.finish()

	if hasFinding(result, "triage_text_mojibake") {
		t.Fatalf("trusted repair marker did not resolve the finding: %+v", result)
	}
}

func TestCorrectRussianTriageIsNotMojibake(t *testing.T) {
	good := issueComment(10, "**Триаж.**\nКорень найден, решение проверено.\n<!-- pp:triage -->")
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{testIssue(1289, good)}, "ivanarama")

	if hasFinding(result, "triage_text_mojibake") {
		t.Fatalf("valid Russian was rejected: %+v", result)
	}
}
