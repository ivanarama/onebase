package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC = "cccccccccccccccccccccccccccccccccccccccc"
	epoch = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func testPR(number int, head string, labels ...string) apiPull {
	item := apiPull{Number: number, Title: "PR", HTMLURL: "https://example.test/pr", UpdatedAt: "2026-09-01T00:00:00Z", State: "open"}
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

// withMergeHead gives the pull request the two-parent head commit every
// base-sync leaves behind. Comment history alone never proves that shape.
func withMergeHead(item apiPull) apiPull {
	item.HeadParents = []string{headA, headB}
	return item
}

func withAdmission(item apiPull, repository, ref, mergeable, mergeState, base string, maintainer bool, checks ...apiCheckContext) apiPull {
	item.Head.Ref = ref
	item.Head.Repo = &struct {
		FullName string `json:"full_name"`
	}{FullName: repository}
	item.MaintainerCanModify = maintainer
	item.AdmissionKnown = true
	item.Mergeable = mergeable
	item.MergeStateStatus = mergeState
	item.Base.OID = base
	item.CheckContexts = append(item.CheckContexts, checks...)
	return item
}

func successfulRequiredChecks() []apiCheckContext {
	names := []string{"build", "lint", "postgres-integration", "vuln", "smoke", "e2e", "test-windows", "launcher-webview-build"}
	checks := make([]apiCheckContext, 0, len(names))
	for _, name := range names {
		checks = append(checks, apiCheckContext{Name: name, Status: "COMPLETED", Conclusion: "SUCCESS"})
	}
	return checks
}

func prepIntent(from, base string) string {
	return prepIntentWithIdentity(from, base, testPreReviewIdentity("ivanarama/onebase", "feature/multiline", false))
}

func prepDone(intentID int64, from, to, base string) string {
	return prepDoneWithIdentity(intentID, from, to, base, testPreReviewIdentity("ivanarama/onebase", "feature/multiline", false))
}

func prepIntentWithIdentity(from, base, identity string) string {
	return fmt.Sprintf("<!-- pp:pre-review-sync-intent from=%s base=%s identity-sha256=%s -->", from, base, identity)
}

func prepDoneWithIdentity(intentID int64, from, to, base, identity string) string {
	return fmt.Sprintf("<!-- pp:pre-review-sync-done intent=%d from=%s to=%s base=%s identity-sha256=%s -->", intentID, from, to, base, identity)
}

func testPreReviewIdentity(repository, ref string, maintainer bool) string {
	record := fmt.Sprintf("pp-pre-review-sync-identity-v1\nhead-repository=%s\nhead-ref=%s\nmaintainer-can-modify=%t\n", repository, ref, maintainer)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(record)))
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

func TestConflictingHeadWithoutRequiredChecksRoutesToPreReviewSync(t *testing.T) {
	item := withAdmission(testPR(1394, headA), "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewSyncCandidates) != 1 || got.PreReviewSyncCandidates[0].Number != 1394 ||
		got.PreReviewSyncCandidates[0].Stage != "pre-review-sync" {
		t.Fatalf("conflict did not enter pre-review sync: %+v", got)
	}
	if len(got.FixCandidates) != 1 || len(got.ContentReviewCandidates) != 0 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("deadlocked PR leaked to REVIEW or left FIX: %+v", got)
	}
}

func TestPostFixConflictingHeadCanEnterPreReviewSync(t *testing.T) {
	item := addComment(testPR(1553, headB), 20, completion(headA, 10, 15))
	item = withAdmission(item, "ivanarama/onebase", "fix/1553", "CONFLICTING", "DIRTY", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewSyncCandidates) != 1 || got.PreReviewSyncCandidates[0].Number != 1553 {
		t.Fatalf("post-FIX head was not prepared before review: %+v", got)
	}
}

func TestReviewTransactionOwnsHeadBeforePreReviewSync(t *testing.T) {
	claim := fmt.Sprintf("<!-- pp:review-claim %s review-comment=20 epoch-sha256=%s -->", headA, epoch)
	item := addComment(testPR(1394, headA), 25, claim)
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewSyncCandidates) != 0 || !hasFinding(got, "unfinished_review_transaction") {
		t.Fatalf("pre-review sync stole an active REVIEW transaction: %+v", got)
	}
}

func TestReviewOverrideOwnsHeadBeforePreReviewSync(t *testing.T) {
	item := addComment(testPR(1394, headA), 25, "pp:review-again")
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewSyncCandidates) != 0 || len(got.ReviewCandidates) != 1 {
		t.Fatalf("pre-review sync stole an explicit REVIEW override: %+v", got)
	}
}

func TestPreReviewSyncRequiresForkMaintainerPermission(t *testing.T) {
	blocked := withAdmission(testPR(1408, headA), "contributor/onebase", "feature/owners", "CONFLICTING", "DIRTY", headB, false)
	allowed := withAdmission(testPR(1409, headB), "contributor/onebase", "feature/search", "CONFLICTING", "DIRTY", headC, true)

	got := analyze([]apiPull{blocked, allowed}, "ivanarama")
	if len(got.PreReviewSyncCandidates) != 1 || got.PreReviewSyncCandidates[0].Number != 1409 ||
		!hasFinding(got, "pre_review_sync_source_blocked") {
		t.Fatalf("fork identity/permission gate failed: %+v", got)
	}
}

func TestCompletedPreReviewSyncWaitsForCIWithoutRepeating(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewWaitingCI) != 1 || len(got.PreReviewSyncCandidates) != 0 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("pending CI repeated sync or started REVIEW: %+v", got)
	}
}

func TestCompletedPreReviewSyncWaitsForPendingRequiredRerun(t *testing.T) {
	checks := successfulRequiredChecks()
	checks[0] = apiCheckContext{Name: "build", Status: "COMPLETED", Conclusion: "FAILURE"}
	checks = append(checks, apiCheckContext{Name: "build", Status: "IN_PROGRESS"})
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, checks...)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewWaitingCI) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("pending required rerun started REVIEW too early: %+v", got)
	}
}

func TestCompletedPreReviewSyncWaitsForPendingRerunAfterEarlierSuccess(t *testing.T) {
	checks := successfulRequiredChecks()
	checks = append(checks, apiCheckContext{Name: "build", Status: "IN_PROGRESS"})
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, checks...)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewWaitingCI) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("pending rerun after earlier success started validation too early: %+v", got)
	}
}

func TestMissingPostSyncCIEventuallyNeedsAttention(t *testing.T) {
	identity := testPreReviewIdentity("ivanarama/onebase", "feature/multiline", false)
	intentAt := time.Now().UTC().Add(-61 * time.Minute).Truncate(time.Second)
	doneAt := intentAt.Add(time.Minute)
	item := testPR(1394, headB)
	item.Comments = []apiComment{
		{ID: 20, CreatedAt: intentAt.Format(time.RFC3339), UpdatedAt: intentAt.Format(time.RFC3339), User: apiUser{Login: "ivanarama"}, Body: prepIntentWithIdentity(headA, headC, identity)},
		{ID: 21, CreatedAt: doneAt.Format(time.RFC3339), UpdatedAt: doneAt.Format(time.RFC3339), User: apiUser{Login: "ivanarama"}, Body: prepDoneWithIdentity(20, headA, headB, headC, identity)},
	}
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_ci_needs_attention") || len(got.PreReviewWaitingCI) != 1 ||
		len(got.HumanWaiting) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("missing fork CI stayed silently hidden forever: %+v", got)
	}
}

func TestPartiallyMissingPostSyncCIEventuallyNeedsAttention(t *testing.T) {
	identity := testPreReviewIdentity("ivanarama/onebase", "feature/multiline", false)
	intentAt := time.Now().UTC().Add(-61 * time.Minute).Truncate(time.Second)
	doneAt := intentAt.Add(time.Minute)
	item := testPR(1394, headB)
	item.Comments = []apiComment{
		{ID: 20, CreatedAt: intentAt.Format(time.RFC3339), UpdatedAt: intentAt.Format(time.RFC3339), User: apiUser{Login: "ivanarama"}, Body: prepIntentWithIdentity(headA, headC, identity)},
		{ID: 21, CreatedAt: doneAt.Format(time.RFC3339), UpdatedAt: doneAt.Format(time.RFC3339), User: apiUser{Login: "ivanarama"}, Body: prepDoneWithIdentity(20, headA, headB, headC, identity)},
	}
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false,
		apiCheckContext{Name: "build", Status: "COMPLETED", Conclusion: "SUCCESS"})

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_ci_needs_attention") || len(got.PreReviewWaitingCI) != 1 ||
		len(got.HumanWaiting) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("partially missing required CI stayed silently hidden forever: %+v", got)
	}
}

func TestCompletedPreReviewSyncReturnsExactHeadToFullReviewAfterCI(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 1394 ||
		got.ReviewCandidates[0].Stage != "pre-review-validation" || len(got.PreReviewSyncCandidates) != 0 {
		t.Fatalf("synced head did not return to full content REVIEW: %+v", got)
	}
	proof := got.ReviewCandidates[0].PreReviewSyncValidation
	if proof == nil || proof.IntentCommentID != 20 || proof.DoneCommentID != 21 ||
		proof.From != headA || proof.To != headB || proof.Base != headC ||
		proof.IdentitySHA256 != testPreReviewIdentity("ivanarama/onebase", "feature/multiline", false) ||
		proof.IntentCreatedAt == "" || proof.DoneCreatedAt == "" {
		t.Fatalf("validation target lost exact handoff identity: %+v", proof)
	}
	encoded, err := json.Marshal(got.ReviewCandidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(wire["pre_review_sync"], &metadata); err != nil {
		t.Fatal(err)
	}
	expectedKeys := []string{"intent_comment_id", "done_comment_id", "from", "to", "base", "identity_sha256", "intent_created_at", "done_created_at"}
	if len(metadata) != len(expectedKeys) {
		t.Fatalf("pre_review_sync metadata keys: %s", wire["pre_review_sync"])
	}
	for _, key := range expectedKeys {
		if _, ok := metadata[key]; !ok {
			t.Fatalf("pre_review_sync metadata is missing %q: %s", key, wire["pre_review_sync"])
		}
	}
	for _, key := range []string{"intent_comment_id", "done_comment_id"} {
		var id int64
		if err := json.Unmarshal(metadata[key], &id); err != nil || id <= 0 {
			t.Fatalf("%s is not a positive int64: %s (%v)", key, metadata[key], err)
		}
	}
	hexPattern := regexp.MustCompile(`^[0-9a-f]+$`)
	for key, length := range map[string]int{"from": 40, "to": 40, "base": 40, "identity_sha256": 64} {
		var value string
		if err := json.Unmarshal(metadata[key], &value); err != nil || len(value) != length || !hexPattern.MatchString(value) {
			t.Fatalf("%s has invalid lowercase hex: %s (%v)", key, metadata[key], err)
		}
	}
	var intentAtText, doneAtText string
	if err := json.Unmarshal(metadata["intent_created_at"], &intentAtText); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metadata["done_created_at"], &doneAtText); err != nil {
		t.Fatal(err)
	}
	intentAt, intentErr := time.Parse(time.RFC3339, intentAtText)
	doneAt, doneErr := time.Parse(time.RFC3339, doneAtText)
	if intentErr != nil || doneErr != nil || !doneAt.After(intentAt) || got.ReviewCandidates[0].Head != proof.To {
		t.Fatalf("invalid timestamp/head wire invariants: candidate=%+v intentErr=%v doneErr=%v", got.ReviewCandidates[0], intentErr, doneErr)
	}
}

func TestCompletedPreReviewSyncWithLateStopDoesNotEnterValidation(t *testing.T) {
	item := addComment(testPR(1394, headB, "needs-decision"), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_validation_blocked") || len(got.HumanWaiting) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("late human stop entered pre-review validation: %+v", got)
	}
}

func TestBlockedPreReviewSyncCannotBeClosedByStaleWorkerDone(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_handoff_recovery") || len(got.HumanWaiting) != 0 ||
		len(got.ReviewCandidates) != 0 || len(got.FixCandidates) != 1 {
		t.Fatalf("stale worker done bypassed durable human handoff: %+v", got)
	}
}

func TestExactResumeAllowsBlockedPreReviewSyncDone(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headB)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, resumeMarker)
	item = addComment(item, 23, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Stage != "pre-review-validation" ||
		hasFinding(got, "pre_review_sync_recovery_blocked") {
		t.Fatalf("exact human resume did not authorize matching done: %+v", got)
	}
}

func TestResumeBeforeExpectedMergeAllowsBlockedPreReviewSyncDone(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headA)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, resumeMarker)
	item = addComment(item, 23, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Stage != "pre-review-validation" ||
		hasFinding(got, "pre_review_sync_recovery_blocked") {
		t.Fatalf("resume immediately before the expected merge was lost: %+v", got)
	}
}

func TestResumeAfterDoneCannotRetroactivelyAuthorizePreReviewSync(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headB)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, prepDone(20, headA, headB, headC))
	item = addComment(item, 23, resumeMarker)
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_handoff_recovery") || len(got.ReviewCandidates) != 0 || len(got.FixCandidates) != 1 {
		t.Fatalf("resume after done retroactively authorized stale work: %+v", got)
	}
}

func TestNewBlockAfterResumeRejectsPreReviewSyncDone(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headB)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, resumeMarker)
	item = addComment(item, 23, blockedMarker)
	item = addComment(item, 24, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_handoff_recovery") || len(got.ReviewCandidates) != 0 || len(got.FixCandidates) != 1 {
		t.Fatalf("new block after resume was ignored by done validation: %+v", got)
	}
}

func TestUnmatchedResumeCannotAuthorizePreReviewSyncDone(t *testing.T) {
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headB)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, resumeMarker)
	item = addComment(item, 22, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_malformed") || len(got.HumanWaiting) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("unmatched resume authorized pre-review validation: %+v", got)
	}
}

func TestCompletedPreReviewSyncWithOrphanReviewClaimDoesNotEnterValidation(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item = addComment(item, 22, fmt.Sprintf("<!-- pp:review-claim %s review-comment=19 epoch-sha256=%s -->", headB, epoch))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_validation_blocked") || len(got.HumanWaiting) != 1 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("orphan review transaction entered pre-review validation: %+v", got)
	}
}

func TestUnknownConflictAdmissionDoesNotFallThroughToContentReview(t *testing.T) {
	item := withAdmission(testPR(1394, headA), "ivanarama/onebase", "feature/multiline", "UNKNOWN", "UNKNOWN", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_admission_pending") || len(got.ReviewCandidates) != 0 ||
		len(got.ContentReviewCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("unknown mergeability escaped into executable work: %+v", got)
	}
}

func TestMalformedPreReviewDoneFailsClosed(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, "dddddddddddddddddddddddddddddddddddddddd"}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_malformed") || len(got.ReviewCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("malformed done was treated as executable work: %+v", got)
	}
}

func TestPreReviewSyncForeignHeadAfterIntentFailsClosed(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item.HeadParents = []string{"dddddddddddddddddddddddddddddddddddddddd"}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_orphaned") || len(got.HumanWaiting) != 1 ||
		len(got.PreReviewSyncCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("foreign push bypassed the earliest durable intent: %+v", got)
	}
}

func TestForeignForkChangeQuarantinesOnlyThatPull(t *testing.T) {
	blocked := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	blocked.HeadParents = []string{"dddddddddddddddddddddddddddddddddddddddd"}
	blocked = withAdmission(blocked, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headC, false)
	ordinary := testPR(1500, headC)

	got := analyze([]apiPull{blocked, ordinary}, "ivanarama")
	got.finish()
	if got.State == "red" || len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 1500 {
		t.Fatalf("one changed fork globally denied service to other PRs: %+v", got)
	}
}

func TestAdmissionRaceQuarantinesOnlyChangedPull(t *testing.T) {
	changed := testPR(1394, headA)
	stable := testPR(1500, headB)
	aliases := map[string]int{"pr0": 0, "pr1": 1}
	response := map[string]json.RawMessage{
		"pr0": json.RawMessage(fmt.Sprintf(`{"number":1394,"headRefOid":"%s"}`, headC)),
		"pr1": json.RawMessage(fmt.Sprintf(`{"number":1500,"headRefOid":"%s","baseRefOid":"%s","mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","commits":{"nodes":[{"commit":{"oid":"%s","statusCheckRollup":null}}]}}`, headB, headC, headB)),
	}
	prs := []apiPull{changed, stable}

	applyPullAdmissionBatch(prs, aliases, response)
	if prs[0].AdmissionError == "" || prs[0].AdmissionKnown || !prs[1].AdmissionKnown || prs[1].AdmissionError != "" {
		t.Fatalf("per-PR admission quarantine failed: %+v", prs)
	}
	got := analyze(prs, "ivanarama")
	got.finish()
	if got.State == "red" || len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 1500 {
		t.Fatalf("admission race globally denied service: %+v", got)
	}
}

func TestPreReviewSyncDoneMustReferenceEarliestExactIntent(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepIntent(headA, headC))
	item = addComment(item, 22, prepDone(21, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_malformed") || len(got.ReviewCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("later duplicate intent displaced the earliest intent: %+v", got)
	}
}

func TestCurrentDoneAndOpenIntentForSameTransitionAreAmbiguous(t *testing.T) {
	otherIdentity := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	item := addComment(testPR(1394, headB), 20, prepIntentWithIdentity(headA, headC, otherIdentity))
	item = addComment(item, 21, prepIntent(headA, headC))
	item = addComment(item, 22, prepDone(21, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_malformed") || len(got.ReviewCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("ambiguous identity intents elected the wrong recovery winner: %+v", got)
	}
}

func TestCompletedHopCanRecoverNextIntentFromCurrentHead(t *testing.T) {
	nextBase := "dddddddddddddddddddddddddddddddddddddddd"
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item = addComment(item, 22, prepIntent(headB, nextBase))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", nextBase, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.FixCandidates) != 1 || got.FixCandidates[0].Stage != "pre-review-sync-recovery" ||
		hasFinding(got, "pre_review_sync_malformed") {
		t.Fatalf("valid next-hop recovery was mistaken for ambiguous history: %+v", got)
	}
}

func TestPreReviewSyncDoneTimestampMustFollowIntent(t *testing.T) {
	item := testPR(1394, headB)
	item.Comments = []apiComment{
		{ID: 20, CreatedAt: "2026-09-01T00:00:01Z", UpdatedAt: "2026-09-01T00:00:01Z", User: apiUser{Login: "ivanarama"}, Body: prepIntent(headA, headC)},
		{ID: 21, CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z", User: apiUser{Login: "ivanarama"}, Body: prepDone(20, headA, headB, headC)},
	}
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_malformed") || len(got.ReviewCandidates) != 0 {
		t.Fatalf("non-monotonic intent/done timestamps entered REVIEW: %+v", got)
	}
}

func TestCompletedPreReviewSyncWithOldShipAndReviewDepthStillReturnsToFullReview(t *testing.T) {
	item := addComment(testPR(1394, headB, "ship"), 10, completion(headA, 5, 7))
	item = addComment(item, 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Stage != "pre-review-validation" ||
		got.IntegrationOwner != nil || len(got.MergeCandidates) != 0 {
		t.Fatalf("old ship/review proof escaped full post-sync review: %+v", got)
	}
}

func TestReviewedPreReviewSyncHeadStaysInOrdinaryMergeLane(t *testing.T) {
	item := addComment(testPR(1394, headB, "ship", "reviewed"), 10, completion(headA, 5, 7))
	item = addComment(item, 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item = addComment(item, 30, completion(headB, 25, 29))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	got.finish()
	if got.IntegrationOwner != nil || len(got.MergeCandidates) != 1 ||
		got.MergeCandidates[0].Stage != "merge" || len(got.MergeExecutable) != 1 ||
		got.MergeExecutable[0].Number != 1394 {
		t.Fatalf("reviewed pre-review-sync head seized the integration lane: %+v", got)
	}
	if hasFinding(got, "legacy_ship_waiting_merge") {
		t.Fatalf("pre-review-sync lineage was mistaken for legacy base-sync: %+v", got.Findings)
	}
}

func TestReviewCompletedBeforePreReviewSyncDoneCannotAuthorizeMerge(t *testing.T) {
	item := addComment(testPR(1394, headB, "ship", "reviewed"), 20, prepIntent(headA, headC))
	item = addComment(item, 21, completion(headB, 18, 19))
	item = addComment(item, 22, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "MERGEABLE", "CLEAN", headC, false, successfulRequiredChecks()...)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_validation_out_of_order") || len(got.HumanWaiting) != 1 ||
		len(got.MergeCandidates) != 0 || got.IntegrationOwner != nil {
		t.Fatalf("review before matching done authorized a pre-review-sync merge: %+v", got)
	}
}

func TestAdvancedBaseCanStartNextPreReviewSyncHop(t *testing.T) {
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", "dddddddddddddddddddddddddddddddddddddddd", false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.PreReviewSyncCandidates) != 1 || got.PreReviewSyncCandidates[0].Stage != "pre-review-sync" {
		t.Fatalf("advanced base left a new conflict deadlocked: %+v", got)
	}
}

func TestCompletedPreReviewSyncCannotCycleBackToFrom(t *testing.T) {
	item := addComment(testPR(1394, headA), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_cycle") || len(got.HumanWaiting) != 1 ||
		len(got.FixCandidates) != 0 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("rollback to a completed hop from started an automatic cycle: %+v", got)
	}
}

func TestCompletedPreReviewSyncChainCannotRollBackToIntermediateFrom(t *testing.T) {
	headD := "dddddddddddddddddddddddddddddddddddddddd"
	headE := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, prepDone(20, headA, headB, headC))
	item = addComment(item, 22, prepIntent(headB, headD))
	item = addComment(item, 23, prepDone(22, headB, headE, headD))
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headD, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_cycle") || len(got.HumanWaiting) != 1 ||
		len(got.FixCandidates) != 0 || len(got.ReviewCandidates) != 0 {
		t.Fatalf("rollback to an intermediate completed-hop from escaped cycle quarantine: %+v", got)
	}
}

func TestOpenPreReviewIntentIsRecoveryAndWinsFixQueue(t *testing.T) {
	recovery := addComment(testPR(1394, headA), 20, prepIntent(headA, headB))
	recovery = withAdmission(recovery, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)
	rework := testPR(1200, headB, "changes-requested")

	got := analyze([]apiPull{rework, recovery}, "ivanarama")
	if len(got.FixCandidates) != 2 || got.FixCandidates[0].Number != 1394 ||
		got.FixCandidates[0].Stage != "pre-review-sync-recovery" || got.FixCandidates[1].Stage != "fix-review" {
		t.Fatalf("pre-review recovery did not precede ordinary rework: %+v", got.FixCandidates)
	}
}

func TestOpenPreReviewIntentStillOwnsRecoveryAfterMainAdvances(t *testing.T) {
	item := addComment(testPR(1394, headA), 20, prepIntent(headA, headB))
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.FixCandidates) != 1 || got.FixCandidates[0].Stage != "pre-review-sync-recovery" {
		t.Fatalf("advanced main stranded durable recovery for its exact old base: %+v", got)
	}
}

func TestOpenPreReviewIntentWithLateHumanStopIsNotExecutable(t *testing.T) {
	item := addComment(testPR(1394, headA, "needs-decision"), 20, prepIntent(headA, headB))
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_recovery_blocked") || len(got.HumanWaiting) != 1 ||
		len(got.PreReviewSyncCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("human stop remained an executable recovery and can starve FIX: %+v", got)
	}
}

func TestPermanentlyBlockedRecoveryDoesNotStarveFixQueue(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	blocked := addComment(testPR(1394, headA, "needs-decision"), 20, prepIntent(headA, headB))
	blocked = addComment(blocked, 21, blockedMarker)
	blocked = withAdmission(blocked, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)
	rework := testPR(1500, headB, "changes-requested")

	got := analyze([]apiPull{blocked, rework}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_recovery_blocked") || len(got.HumanWaiting) != 1 ||
		len(got.FixCandidates) != 1 || got.FixCandidates[0].Number != 1500 {
		t.Fatalf("permanently invalid recovery kept starving the FIX queue: %+v", got)
	}
}

func TestBlockedIntentCannotBeReenabledByHeadTransition(t *testing.T) {
	tests := []struct {
		name       string
		markerHead string
		current    string
		parents    []string
	}{
		{name: "marker on from then merge appears", markerHead: headA, current: headB, parents: []string{headA, headC}},
		{name: "marker on merge then branch returns to from", markerHead: headB, current: headA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", tt.markerHead)
			item := addComment(testPR(1394, tt.current, "needs-decision"), 20, prepIntent(headA, headC))
			item = addComment(item, 21, marker)
			item.HeadParents = tt.parents
			item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headC, false)

			got := analyze([]apiPull{item}, "ivanarama")
			if !hasFinding(got, "pre_review_sync_recovery_blocked") || len(got.HumanWaiting) != 1 || len(got.FixCandidates) != 0 {
				t.Fatalf("head transition bypassed durable blocked intent: %+v", got)
			}
		})
	}
}

func TestExactHumanResumeReenablesSameIntent(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headA)
	item := addComment(testPR(1394, headA, "needs-decision"), 20, prepIntent(headA, headB))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, resumeMarker)
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.FixCandidates) != 1 || got.FixCandidates[0].Stage != "pre-review-sync-recovery" || len(got.HumanWaiting) != 0 {
		t.Fatalf("exact human resume did not restore the same durable intent: %+v", got)
	}
}

func TestResumeBeforeExpectedMergeAllowsOpenRecoveryAfterPush(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headA)
	item := addComment(testPR(1394, headB), 20, prepIntent(headA, headC))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, resumeMarker)
	item.HeadParents = []string{headA, headC}
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headC, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.FixCandidates) != 1 || got.FixCandidates[0].Stage != "pre-review-sync-recovery" ||
		hasFinding(got, "pre_review_sync_recovery_blocked") {
		t.Fatalf("valid resume before an exact merge did not survive a crash before done: %+v", got)
	}
}

func TestNewBlockAfterResumeStopsRecoveryAgain(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headA)
	item := addComment(testPR(1394, headA, "needs-decision"), 20, prepIntent(headA, headB))
	item = addComment(item, 21, blockedMarker)
	item = addComment(item, 22, resumeMarker)
	item = addComment(item, 23, blockedMarker)
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_recovery_blocked") || len(got.HumanWaiting) != 1 || len(got.FixCandidates) != 0 {
		t.Fatalf("later stable event did not block a resumed recovery: %+v", got)
	}
}

func TestResumeAbsorbsEarlierReviewEventButNotLaterOne(t *testing.T) {
	claim := func(id int64) string {
		return fmt.Sprintf("<!-- pp:review-claim %s review-comment=%d epoch-sha256=%s -->", headA, id-1, epoch)
	}
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	resumeMarker := fmt.Sprintf("<!-- pp:pre-review-sync-resume intent=20 head=%s -->", headA)
	base := addComment(testPR(1394, headA, "needs-decision"), 20, prepIntent(headA, headB))
	base = addComment(base, 21, claim(21))
	base = addComment(base, 22, blockedMarker)
	base = addComment(base, 23, resumeMarker)
	base = withAdmission(base, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{base}, "ivanarama")
	if len(got.FixCandidates) != 1 || got.FixCandidates[0].Stage != "pre-review-sync-recovery" {
		t.Fatalf("resume did not absorb an earlier review event: %+v", got)
	}

	withLateClaim := addComment(base, 24, claim(24))
	got = analyze([]apiPull{withLateClaim}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_recovery_blocked") || len(got.FixCandidates) != 0 || len(got.HumanWaiting) != 1 {
		t.Fatalf("review event after resume did not block recovery again: %+v", got)
	}
}

func TestBlockedMarkerWithoutNeedsDecisionRecoversOnlyHandoff(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=post-intent-event -->", headA)
	item := addComment(testPR(1394, headA), 20, prepIntent(headA, headB))
	item = addComment(item, 21, blockedMarker)
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_handoff_recovery") || len(got.FixCandidates) != 1 ||
		got.FixCandidates[0].Stage != "pre-review-sync-recovery" || len(got.HumanWaiting) != 0 {
		t.Fatalf("crash between blocked marker and label was not recoverable: %+v", got)
	}

	item.Labels = append(item.Labels, apiLabel{Name: "needs-decision"})
	got = analyze([]apiPull{item}, "ivanarama")
	if len(got.FixCandidates) != 0 || len(got.HumanWaiting) != 1 || !hasFinding(got, "pre_review_sync_recovery_blocked") {
		t.Fatalf("completed blocked handoff did not release FIX: %+v", got)
	}
}

func TestStablePushDeniedMarkerReleasesRecoveryAfterHandoff(t *testing.T) {
	blockedMarker := fmt.Sprintf("<!-- pp:pre-review-sync-recovery-blocked intent=20 head=%s reason=push-denied -->", headA)
	item := addComment(testPR(1394, headA), 20, prepIntent(headA, headB))
	item = addComment(item, 21, blockedMarker)
	item = withAdmission(item, "contributor/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, true)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_handoff_recovery") || len(got.FixCandidates) != 1 {
		t.Fatalf("push-denied handoff could not recover its missing label: %+v", got)
	}

	item.Labels = append(item.Labels, apiLabel{Name: "needs-decision"})
	got = analyze([]apiPull{item}, "ivanarama")
	if len(got.FixCandidates) != 0 || len(got.HumanWaiting) != 1 || !hasFinding(got, "pre_review_sync_recovery_blocked") {
		t.Fatalf("completed push-denied handoff kept starving FIX: %+v", got)
	}
}

func TestOpenPreReviewIntentCannotOverlapReviewTransaction(t *testing.T) {
	item := addComment(testPR(1394, headA), 20, prepIntent(headA, headB))
	item = addComment(item, 21, fmt.Sprintf("<!-- pp:review-claim %s review-comment=19 epoch-sha256=%s -->", headA, epoch))
	item = withAdmission(item, "ivanarama/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_recovery_blocked") || len(got.FixCandidates) != 0 {
		t.Fatalf("overlapping REVIEW event remained executable FIX recovery: %+v", got)
	}
}

func TestPreReviewSyncForkPermissionChangeFailsClosed(t *testing.T) {
	identity := testPreReviewIdentity("contributor/onebase", "feature/multiline", true)
	item := addComment(testPR(1394, headA), 20, prepIntentWithIdentity(headA, headB, identity))
	item = withAdmission(item, "contributor/onebase", "feature/multiline", "CONFLICTING", "DIRTY", headB, false)

	got := analyze([]apiPull{item}, "ivanarama")
	if !hasFinding(got, "pre_review_sync_identity_changed") || len(got.HumanWaiting) != 1 ||
		len(got.PreReviewSyncCandidates) != 0 || len(got.FixCandidates) != 0 {
		t.Fatalf("changed fork permission retained automatic recovery: %+v", got)
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
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 1 ||
		got.IntegrationOwner == nil || got.IntegrationOwner.Number != 99 ||
		!hasFinding(got, "base_sync_recovery") ||
		!hasFinding(got, "single_flight_barrier") {
		t.Fatalf("MERGE recovery incorrectly blocked content review: %+v", got)
	}
}

func TestOneParentSuccessorWithoutShipReleasesBrokenBaseSyncOwner(t *testing.T) {
	// Live recovery for a malformed historical handoff: an old open intent may
	// describe from or its two-parent merge, but it must not capture an ordinary
	// one-parent successor after route labels are removed. The old comment stays
	// as audit history while the new exact HEAD returns to full content REVIEW.
	item := testPR(1443, headC)
	item.HeadParents = []string{headB}
	item = addComment(item, 30, syncIntent(headA, 10, 20, 25))

	got := analyze([]apiPull{item}, "ivanarama")
	if got.IntegrationOwner != nil || hasFinding(got, "base_sync_recovery") {
		t.Fatalf("historical broken intent kept single-flight ownership: %+v", got)
	}
	if len(got.ContentReviewCandidates) != 1 || got.ContentReviewCandidates[0].Number != 1443 ||
		got.ContentReviewCandidates[0].Head != headC || got.ContentReviewCandidates[0].Stage != "review" {
		t.Fatalf("one-parent recovery head did not return to content review: %+v", got)
	}
}

func TestHistoricalUnfinishedIntentDoesNotOverrideCurrentCompletedSync(t *testing.T) {
	item := testPR(1323, headC, "ship", "reviewed")
	item.HeadParents = []string{headB, headA}
	item = addComment(item, 20, syncIntent(headA, 10, 11, 12))
	item = addComment(item, 21, syncIntent(headA, 10, 11, 12))
	item = addComment(item, 30, syncIntent(headB, 13, 14, 15))
	item = addComment(item, 31, syncDone(30, headB, headC))

	got := analyze([]apiPull{item}, "ivanarama")
	if got.IntegrationOwner == nil || got.IntegrationOwner.Number != 1323 ||
		got.IntegrationOwner.Stage != "integration-review" ||
		len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 1323 ||
		hasFinding(got, "base_sync_recovery") ||
		!hasFinding(got, "base_sync_waiting_review") {
		t.Fatalf("old intents from superseded heads blocked current integration review: %+v", got)
	}
}

func TestNewIntentFromCurrentCompletedHeadStillRequiresRecovery(t *testing.T) {
	item := testPR(99, headC, "ship", "reviewed")
	item.HeadParents = []string{headB, headA}
	item = addComment(item, 30, syncIntent(headB, 10, 20, 25))
	item = addComment(item, 31, syncDone(30, headB, headC))
	item = addComment(item, 40, syncIntent(headC, 32, 33, 34))

	got := analyze([]apiPull{item}, "ivanarama")
	if got.IntegrationOwner == nil || got.IntegrationOwner.Stage != "integration-merge-recovery" ||
		!hasFinding(got, "base_sync_recovery") {
		t.Fatalf("open intent from the current head was not recoverable: %+v", got)
	}
}

func TestMultiHopRecoveryKeepsOriginalSingleFlightOwnership(t *testing.T) {
	owner := testPR(1218, headC, "ship", "reviewed")
	owner.HeadParents = []string{headB, headA}
	owner = addComment(owner, 10,
		fmt.Sprintf("<!-- pp:base-sync-intent from=%s base=%s review-comment=1 claim=2 completion=3 ship-event=LE_test previous=none -->", headA, headB))
	owner = addComment(owner, 11,
		fmt.Sprintf("<!-- pp:base-sync-done intent=10 from=%s to=%s base=%s previous=none ship-event=LE_test -->", headA, headB, headB))
	owner = addComment(owner, 50,
		fmt.Sprintf("<!-- pp:base-sync-intent from=%s base=%s review-comment=4 claim=5 completion=6 ship-event=LE_test previous=11 -->", headB, headA))

	later := testPR(1220, headB, "ship", "reviewed")
	later.HeadParents = []string{headA, headB}
	later = addComment(later, 20, syncIntent(headA, 7, 8, 9))
	later = addComment(later, 21, syncDone(20, headA, headB))

	got := analyze([]apiPull{later, owner}, "ivanarama")
	if got.IntegrationOwner == nil || got.IntegrationOwner.Number != 1218 ||
		got.IntegrationOwner.Stage != "integration-merge-recovery" ||
		len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Number != 1218 {
		t.Fatalf("multi-hop owner was overtaken by a later chain: %+v", got)
	}
}

func TestCompletedIntegrationReviewKeepsBarrierUntilMerge(t *testing.T) {
	owner := addComment(testPR(20, headB, "ship", "reviewed"), 30, syncIntent(headA, 10, 20, 25))
	owner = addComment(owner, 31, syncDone(30, headA, headB))
	owner = addComment(owner, 40, completion(headB, 35, 36))
	wouldBeNext := withMergeHead(addComment(testPR(30, headB, "ship"), 41, completion(headA, 37, 38)))
	ordinary := testPR(40, headA)

	got := analyze([]apiPull{wouldBeNext, ordinary, owner}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 40 ||
		got.IntegrationOwner == nil || got.IntegrationOwner.Number != 20 ||
		len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Number != 20 ||
		!hasFinding(got, "base_sync_waiting_merge") ||
		!hasFinding(got, "single_flight_barrier") {
		t.Fatalf("merge-ready owner incorrectly blocked content review: %+v", got)
	}
}

func TestLegacyReShipIsVisibleAsPriorityValidationCandidate(t *testing.T) {
	ordinary := testPR(1, headA)
	legacy := withMergeHead(addComment(testPR(99, headB, "ship"), 30, completion(headA, 20, 25)))

	got := analyze([]apiPull{ordinary, legacy}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 99 ||
		got.ReviewCandidates[0].Stage != "legacy-integration-review" {
		t.Fatalf("legacy re-ship validation did not get priority: %+v", got.ReviewCandidates)
	}
	if !hasFinding(got, "legacy_ship_waiting_review_validation") {
		t.Fatalf("legacy re-ship is invisible: %+v", got)
	}
}

func TestOrdinaryFixRoundIsNotAnIntegrationOwner(t *testing.T) {
	// changes-requested, push, no review of the new head yet. The comment trail
	// is identical to a legacy re-ship; only the single-parent head tells them
	// apart, and this PR must not seize the integration lane.
	fixRound := addComment(testPR(99, headB, "ship"), 30, completion(headA, 20, 25))

	got := analyze([]apiPull{fixRound}, "ivanarama")
	if got.IntegrationOwner != nil {
		t.Fatalf("ordinary fix round became a false integration owner: %+v", got.IntegrationOwner)
	}
	if len(got.ContentReviewCandidates) != 1 || got.ContentReviewCandidates[0].Number != 99 ||
		got.ContentReviewCandidates[0].Stage != "review" {
		t.Fatalf("new head after a fix round left the content lane: %+v", got.ContentReviewCandidates)
	}
	if hasFinding(got, "legacy_ship_waiting_review_validation") ||
		!hasFinding(got, "ship_waiting_next_round_review") {
		t.Fatalf("fix round was reported as legacy lineage: %+v", got.Findings)
	}
}

func TestReviewedFixRoundStaysOrdinaryMergeCandidate(t *testing.T) {
	// Two review rounds, the current head green: depth exceeds the completions
	// of this head, which used to be read as a legacy base-sync.
	item := addComment(testPR(99, headB, "ship", "reviewed"), 30, completion(headA, 20, 25))
	item = addComment(item, 31, completion(headB, 40, 45))

	got := analyze([]apiPull{item}, "ivanarama")
	if got.IntegrationOwner != nil {
		t.Fatalf("reviewed fix round became a false integration owner: %+v", got.IntegrationOwner)
	}
	if len(got.MergeCandidates) != 1 || got.MergeCandidates[0].Stage != "merge" ||
		len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Number != 99 {
		t.Fatalf("ordinary merge candidate was routed into the integration lane: %+v", got)
	}
	if hasFinding(got, "legacy_ship_waiting_merge") {
		t.Fatalf("review depth alone was accepted as legacy proof: %+v", got.Findings)
	}
}

func TestLegacyMergeHeadWithCurrentProofOwnsTheLane(t *testing.T) {
	item := withMergeHead(addComment(testPR(99, headB, "ship", "reviewed"), 30, completion(headA, 20, 25)))
	item = addComment(item, 31, completion(headB, 40, 45))

	got := analyze([]apiPull{item}, "ivanarama")
	if got.IntegrationOwner == nil || got.IntegrationOwner.Number != 99 ||
		got.IntegrationOwner.Stage != "legacy-integration-merge-ready" ||
		len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Number != 99 ||
		!hasFinding(got, "legacy_ship_waiting_merge") {
		t.Fatalf("genuine legacy base-sync lost the lane: %+v", got)
	}
}

func TestBaseSyncOwnerSurvivesLowerNumberedFixRounds(t *testing.T) {
	// The deadlock this guards: ordinary fix rounds with smaller numbers used to
	// win the lane, which hid the real base-sync owner from REVIEW while MERGE
	// refused the impostor — so neither stage could move.
	impostor := addComment(testPR(20, headB, "ship", "reviewed"), 30, completion(headA, 20, 25))
	impostor = addComment(impostor, 31, completion(headB, 40, 45))
	owner := withMergeHead(addComment(testPR(90, headB, "ship", "reviewed"), 32, syncIntent(headA, 10, 21, 26)))
	owner = addComment(owner, 33, syncDone(32, headA, headB))

	got := analyze([]apiPull{impostor, owner}, "ivanarama")
	if got.IntegrationOwner == nil || got.IntegrationOwner.Number != 90 ||
		got.IntegrationOwner.Stage != "integration-review" {
		t.Fatalf("base-sync owner lost the lane to a fix round: %+v", got.IntegrationOwner)
	}
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 90 {
		t.Fatalf("REVIEW cannot reach the base-sync owner: %+v", got.ReviewCandidates)
	}
	if len(got.MergeExecutable) != 0 {
		t.Fatalf("MERGE was offered a PR while the owner waits for REVIEW: %+v", got.MergeExecutable)
	}
	if len(got.MergeCandidates) != 1 || got.MergeCandidates[0].Number != 20 {
		t.Fatalf("ordinary candidate left the merge queue: %+v", got.MergeCandidates)
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
	first := withMergeHead(addComment(testPR(20, headB, "ship"), 30, completion(headA, 20, 25)))
	second := withMergeHead(addComment(testPR(30, headB, "ship"), 31, completion(headA, 21, 26)))
	ordinary := testPR(1, headA)

	got := analyze([]apiPull{second, ordinary, first}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 20 {
		t.Fatalf("single-flight owner is not exclusive: %+v", got.ReviewCandidates)
	}
	if len(got.ContentReviewCandidates) != 1 || got.ContentReviewCandidates[0].Number != 1 {
		t.Fatalf("content backlog disappeared behind the integration owner: %+v", got)
	}
	if len(got.ReviewBacklog) != 2 {
		t.Fatalf("total review backlog hid deferred content: %+v", got)
	}
	if !hasFinding(got, "single_flight_barrier") {
		t.Fatalf("single-flight barrier is invisible: %+v", got)
	}
}

func TestWithoutIntegrationOwnerContentCandidatesAreExecutable(t *testing.T) {
	got := analyze([]apiPull{testPR(20, headA), testPR(10, headB)}, "ivanarama")
	if got.IntegrationOwner != nil || len(got.ReviewCandidates) != 2 ||
		got.ReviewCandidates[0].Number != 10 || len(got.ContentReviewCandidates) != 2 {
		t.Fatalf("content lane was not exposed as executable: %+v", got)
	}
}

func TestCurrentReviewedHeadIsVisibleAsWaitingShip(t *testing.T) {
	item := addComment(testPR(10, headA, "reviewed"), 30, completion(headA, 20, 25))
	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewedWaitingShip) != 1 || got.ReviewedWaitingShip[0].Number != 10 ||
		len(got.ReviewCandidates) != 0 {
		t.Fatalf("accepted current HEAD was not shown as waiting for ship: %+v", got)
	}
}

func TestTrustedShipWithCurrentProofIsVisibleToMerge(t *testing.T) {
	item := addComment(testPR(10, headA, "reviewed", "ship"), 30, completion(headA, 20, 25))
	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.MergeCandidates) != 1 || got.MergeCandidates[0].Number != 10 ||
		got.MergeCandidates[0].Stage != "merge" ||
		len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Number != 10 ||
		got.MergeExecutable[0].UpdatedAt != "2026-09-01T00:00:00Z" {
		t.Fatalf("ordinary merge candidate was hidden: %+v", got)
	}
}

func TestStickyShipDoesNotHideInitialReview(t *testing.T) {
	item := testPR(10, headA, "ship")
	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 10 ||
		got.ReviewCandidates[0].Stage != "review" ||
		!hasFinding(got, "ship_waiting_initial_review") {
		t.Fatalf("sticky ship hid first-time review: %+v", got)
	}
}

func TestIntegrationReviewBlocksUnrelatedMergeWake(t *testing.T) {
	owner := addComment(testPR(20, headB, "ship", "reviewed"), 30, syncIntent(headA, 10, 20, 25))
	owner = addComment(owner, 31, syncDone(30, headA, headB))
	ordinary := addComment(testPR(40, headA, "reviewed", "ship"), 50, completion(headA, 45, 46))

	got := analyze([]apiPull{ordinary, owner}, "ivanarama")
	if got.IntegrationOwner == nil || got.IntegrationOwner.Number != 20 ||
		len(got.MergeCandidates) != 1 || got.MergeCandidates[0].Number != 40 ||
		len(got.MergeExecutable) != 0 {
		t.Fatalf("MERGE wake ignored the integration-review barrier: %+v", got)
	}
}

func TestStaleReviewedLabelDoesNotHideNewHead(t *testing.T) {
	item := addComment(testPR(10, headB, "reviewed"), 30, completion(headA, 20, 25))
	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 10 ||
		len(got.ReviewedWaitingShip) != 0 {
		t.Fatalf("stale reviewed label hid a new HEAD: %+v", got)
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

func TestSingleFlightOwnerDoesNotChangeAtMergeReadyStage(t *testing.T) {
	result := report{
		ReviewCandidates: []candidate{
			{Number: 10, Stage: "integration-merge-ready", IntegrationAt: "2026-09-02T00:00:00Z"},
			{Number: 20, Stage: "integration-review", IntegrationAt: "2026-09-01T00:00:00Z"},
		},
		MergeCandidates: []candidate{
			{Number: 10, Stage: "integration-merge-ready"},
		},
	}
	sortCandidates(result.ReviewCandidates)
	applySingleFlight(&result)
	setMergeExecutable(&result)

	if result.IntegrationOwner == nil || result.IntegrationOwner.Number != 20 {
		t.Fatalf("merge-ready phase stole the integration owner: %+v", result.IntegrationOwner)
	}
	if len(result.ReviewCandidates) != 1 || result.ReviewCandidates[0].Number != 20 {
		t.Fatalf("REVIEW did not retain the stable owner: %+v", result.ReviewCandidates)
	}
	if len(result.MergeExecutable) != 0 {
		t.Fatalf("MERGE bypassed the stable owner: %+v", result.MergeExecutable)
	}

	result = report{
		ReviewCandidates: []candidate{
			{Number: 10, Stage: "integration-review", IntegrationAt: "2026-09-02T00:00:00Z"},
			{Number: 20, Stage: "integration-merge-ready", IntegrationAt: "2026-09-01T00:00:00Z"},
		},
		MergeCandidates: []candidate{
			{Number: 20, Stage: "integration-merge-ready"},
		},
	}
	sortCandidates(result.ReviewCandidates)
	applySingleFlight(&result)
	setMergeExecutable(&result)
	if result.IntegrationOwner == nil || result.IntegrationOwner.Number != 20 ||
		len(result.MergeExecutable) != 1 || result.MergeExecutable[0].Number != 20 {
		t.Fatalf("stable owner did not advance to MERGE: %+v", result)
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

func TestOrdinaryMergeCandidatesIgnoreReviewDepth(t *testing.T) {
	items := []candidate{
		{Number: 20, Depth: 0, Stage: "merge", Priority: 2},
		{Number: 10, Depth: 8, Stage: "merge", Priority: 2},
		{Number: 30, Depth: 9, Stage: "merge", Priority: 1},
	}
	sortMergeCandidates(items)
	if items[0].Number != 30 || items[1].Number != 10 || items[2].Number != 20 {
		t.Fatalf("MERGE order must be priority then number: %+v", items)
	}
}

func TestContractRejectsIncompleteTargetReviewGate(t *testing.T) {
	current, err := os.ReadFile(filepath.Join("..", "..", ".claude", "skills", "review-queue", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := os.ReadFile(filepath.Join("..", "..", ".claude", "skills", "review-queue", "references", "legacy-protocol.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"обычная `stage=review` либо специальная `stage=pre-review-validation` цель",
		"routing labels, review-depth и стабильную server timeline/epoch",
	} {
		t.Run(fragment, func(t *testing.T) {
			incomplete := strings.Replace(string(current), fragment, "", 1)
			if incomplete == string(current) {
				t.Fatalf("test fragment is absent from the active contract: %q", fragment)
			}
			path := filepath.Join(t.TempDir(), "review-queue", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(incomplete), 0o600); err != nil { //nolint:gosec // G703: test-owned path below t.TempDir
				t.Fatal(err)
			}
			legacyPath := filepath.Join(filepath.Dir(path), "references", "legacy-protocol.md")
			if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil { //nolint:gosec // G703: test-owned path below t.TempDir
				t.Fatal(err)
			}
			got := report{State: "green"}
			checkContract(&got, path)
			if !hasFinding(got, "unsafe_target_review_contract") {
				t.Fatalf("incomplete target gate stayed green: %+v", got.Findings)
			}
		})
	}
}

func TestActiveContractPassesHealthCheck(t *testing.T) {
	path := filepath.Join("..", "..", ".claude", "skills", "review-queue", "SKILL.md")
	got := report{State: "green"}
	checkContract(&got, path)
	if len(got.Findings) != 0 || got.State != "green" {
		t.Fatalf("active pipeline contract is unhealthy: %+v", got.Findings)
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

func TestShipWithoutReviewHistoryRemainsInitialReviewCandidate(t *testing.T) {
	got := analyze([]apiPull{testPR(99, headB, "ship")}, "ivanarama")
	if len(got.ReviewCandidates) != 1 || got.ReviewCandidates[0].Number != 99 ||
		hasFinding(got, "legacy_ship_waiting_review_validation") ||
		!hasFinding(got, "ship_waiting_initial_review") {
		t.Fatalf("sticky ship did not remain reviewable: %+v", got)
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

func TestShipWithProtocolHistoryButNoCompletionsIsVisible(t *testing.T) {
	item := testPR(7, headA, "ship")
	item = addComment(item, 40, syncIntent(headB, 20, 25, 30))
	item = addComment(item, 41, syncDone(40, headB, headB))

	got := analyze([]apiPull{item}, "ivanarama")
	if len(got.HumanWaiting) != 1 || got.HumanWaiting[0].Number != 7 {
		t.Fatalf("ship PR disappeared from every queue: %+v", got)
	}
	if !hasFinding(got, "ship_without_current_review_proof") {
		t.Fatalf("ship PR disappeared without a finding: %+v", got)
	}
}

func testIssue(number int, comments ...apiComment) apiIssue {
	return apiIssue{Number: number, Title: "Issue", HTMLURL: "https://example.test/issue", CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-02T00:00:00Z", State: "open", Thread: comments}
}

func issueWithLabels(number int, labels ...string) apiIssue {
	item := testIssue(number, issueComment(10, "<!-- pp:triage -->"))
	for _, label := range labels {
		item.Labels = append(item.Labels, apiLabel{Name: label})
	}
	return item
}

func issueComment(id int64, body string) apiComment {
	timestamp := fmt.Sprintf("2026-09-01T10:%02d:00Z", id%60)
	return apiComment{ID: id, CreatedAt: timestamp, UpdatedAt: timestamp,
		User: apiUser{Login: "ivanarama"}, Body: body}
}

func triageRouteRoot(issue int, id int64, route string) (apiComment, string) {
	record := fmt.Sprintf("pp-triage-route-v1\nissue=%d\nissue-updated=2026-09-01T00:00:00Z\ntitle-sha256=%s\nbody-sha256=%s\nanalysis-sha256=%s\ncomments-sha256=%s\nlabels-sha256=%s\nevents-watermark=1\nclass=bug\nroute=%s\nmanual=false\nreply=none\n",
		issue, epoch, epoch, epoch, epoch, epoch, route)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(record)))
	body := fmt.Sprintf("<!-- pp:triage -->\n%s<!-- pp:triage-route-claim fingerprint-sha256=%s owner=11111111-1111-1111-1111-111111111111 -->", record, fingerprint)
	return issueComment(id, body), fingerprint
}

func completedTriageRoute(issue int, route string) []apiComment {
	root, fingerprint := triageRouteRoot(issue, 10, route)
	return []apiComment{
		root,
		issueComment(11, "<!-- pp:triage-route-labels claim=10 fingerprint-sha256="+fingerprint+" events-through=1 labels-sha256="+epoch+" -->"),
		issueComment(12, "<!-- pp:triage-route-done claim=10 fingerprint-sha256="+fingerprint+" -->"),
	}
}

func TestMojibakeInTriageVisibleTextIsRed(t *testing.T) {
	broken := issueComment(10, "**РўСЂРёР°Р¶.**\nРљРѕСЂРµРЅСЊ РЅР°Р№РґРµРЅ.\n<!-- pp:triage -->\npp-triage-route-v1")
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{testIssue(1281, broken)}, nil, "ivanarama")
	result.finish()

	if result.State != "red" || !hasFinding(result, "triage_text_mojibake") {
		t.Fatalf("broken human-facing triage was not diagnosed: %+v", result)
	}
}

func TestDisplayRepairMarkerResolvesMojibakeFinding(t *testing.T) {
	broken := issueComment(10, "**РўСЂРёР°Р¶.**\nРљРѕСЂРµРЅСЊ РЅР°Р№РґРµРЅ.\n<!-- pp:triage -->")
	repair := issueComment(20, "Исправление опубликовано выше.\n<!-- pp:display-repair comment=10 -->")
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{testIssue(1281, broken, repair)}, nil, "ivanarama")
	result.finish()

	if hasFinding(result, "triage_text_mojibake") {
		t.Fatalf("trusted repair marker did not resolve the finding: %+v", result)
	}
}

func TestCorrectRussianTriageIsNotMojibake(t *testing.T) {
	good := issueComment(10, "**Триаж.**\nКорень найден, решение проверено.\n<!-- pp:triage -->")
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{testIssue(1289, good)}, nil, "ivanarama")

	if hasFinding(result, "triage_text_mojibake") {
		t.Fatalf("valid Russian was rejected: %+v", result)
	}
}

func TestIssueQueuesSeparatePlanFixAndHumanWork(t *testing.T) {
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{
		issueWithLabels(10, "plan-needed", "approved", "queue:p0"),
		issueWithLabels(11, "approved"),
		issueWithLabels(12, "ready-fix"),
		issueWithLabels(13, "needs-decision"),
		issueWithLabels(14, "plan-needed", "needs-decision"),
	}, nil, "ivanarama")

	if len(result.PlanCandidates) != 1 || result.PlanCandidates[0].Number != 10 || result.PlanCandidates[0].Priority != 0 {
		t.Fatalf("plan candidate not exposed with priority: %+v", result.PlanCandidates)
	}
	if len(result.FixCandidates) != 2 || result.FixCandidates[0].Number != 11 || result.FixCandidates[1].Number != 12 {
		t.Fatalf("fix issues not exposed: %+v", result.FixCandidates)
	}
	if len(result.HumanWaiting) != 2 {
		t.Fatalf("human issues not separated: %+v", result.HumanWaiting)
	}
}

func TestFixQueueExcludesInWorkAndOpenPullReferences(t *testing.T) {
	result := analyze(nil, "ivanarama")
	issues := []apiIssue{
		issueWithLabels(20, "approved", "in-work"),
		issueWithLabels(21, "approved"),
		issueWithLabels(22, "approved"),
	}
	prs := []apiPull{{Number: 100, State: "open", Title: "fix: issue #21", Body: "Fixes #21"}}
	analyzeIssues(&result, issues, prs, "ivanarama")

	if len(result.FixCandidates) != 1 || result.FixCandidates[0].Number != 22 {
		t.Fatalf("FIX queue included work already owned by a PR: %+v", result.FixCandidates)
	}
}

func TestFixQueueRequiresCompletedTriageRoute(t *testing.T) {
	root, fingerprint := triageRouteRoot(30, 10, "ready-fix")
	unfinished := testIssue(30, root)
	unfinished.Labels = []apiLabel{{Name: "approved"}}
	completeRoot, completeFingerprint := triageRouteRoot(31, 10, "ready-fix")
	complete := testIssue(31, completeRoot,
		issueComment(11, "<!-- pp:triage-route-labels claim=10 fingerprint-sha256="+completeFingerprint+" events-through=1 labels-sha256="+fingerprint+" -->"),
		issueComment(12, "<!-- pp:triage-route-done claim=10 fingerprint-sha256="+completeFingerprint+" -->"))
	complete.Labels = []apiLabel{{Name: "approved"}}
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{unfinished, complete}, nil, "ivanarama")

	if len(result.FixCandidates) != 1 || result.FixCandidates[0].Number != 31 {
		t.Fatalf("FIX queue accepted an unfinished TRIAGE handoff: %+v", result.FixCandidates)
	}
	if !hasFinding(result, "fix_issue_not_executable") {
		t.Fatalf("unfinished TRIAGE handoff was not diagnosed: %+v", result.Findings)
	}
}

func TestIssueRouteDiagnosticsUseCommittedTriageForAllLabels(t *testing.T) {
	readyRoute := testIssue(40, completedTriageRoute(40, "ready-fix")...)
	readyRoute.Labels = []apiLabel{{Name: "needs-decision"}}
	humanRoute := testIssue(41, completedTriageRoute(41, "needs-decision")...)
	humanRoute.Labels = []apiLabel{{Name: "ready-fix"}}
	unfinishedRoot, _ := triageRouteRoot(42, 10, "ready-fix")
	unfinished := testIssue(42, unfinishedRoot)
	unfinished.Labels = []apiLabel{{Name: "needs-decision"}}

	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{readyRoute, humanRoute, unfinished}, nil, "ivanarama")

	for _, number := range []int{40, 41} {
		if !hasIssueFinding(result, "triage_route_label_mismatch", number) {
			t.Fatalf("route mismatch for issue #%d was not diagnosed: %+v", number, result.Findings)
		}
	}
	if !hasIssueFinding(result, "fix_issue_not_executable", 42) {
		t.Fatalf("unfinished route under needs-decision was hidden: %+v", result.Findings)
	}
	if len(result.FixCandidates) != 0 {
		t.Fatalf("route mismatch leaked into the executable FIX allowlist: %+v", result.FixCandidates)
	}
	if len(result.HumanWaiting) != 3 {
		t.Fatalf("route diagnostics did not preserve human-visible work: %+v", result.HumanWaiting)
	}
}

func TestApprovedOverridesCompletedTriageRouteMismatch(t *testing.T) {
	issue := testIssue(43, completedTriageRoute(43, "needs-decision")...)
	issue.Labels = []apiLabel{{Name: "ready-fix"}, {Name: "approved"}}
	result := analyze(nil, "ivanarama")
	analyzeIssues(&result, []apiIssue{issue}, nil, "ivanarama")

	if hasIssueFinding(result, "triage_route_label_mismatch", 43) ||
		len(result.FixCandidates) != 1 || result.FixCandidates[0].Number != 43 {
		t.Fatalf("approved did not override the triage route: %+v", result)
	}
}

func TestPublicCommandReportsRouteMismatchesAndUnfinishedHumanRoute(t *testing.T) {
	readyRoute := testIssue(50, completedTriageRoute(50, "ready-fix")...)
	readyRoute.Labels = []apiLabel{{Name: "needs-decision"}}
	humanRoute := testIssue(51, completedTriageRoute(51, "needs-decision")...)
	humanRoute.Labels = []apiLabel{{Name: "ready-fix"}}
	unfinishedRoot, _ := triageRouteRoot(52, 10, "ready-fix")
	unfinished := testIssue(52, unfinishedRoot)
	unfinished.Labels = []apiLabel{{Name: "needs-decision"}}

	temp := t.TempDir()
	issuesPath := filepath.Join(temp, "issues.json")
	prsPath := filepath.Join(temp, "prs.json")
	issuesJSON, err := json.Marshal([]apiIssue{readyRoute, humanRoute, unfinished})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issuesPath, issuesJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prsPath, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}

	//nolint:gosec // The executable and flags are fixed; variable arguments are test-owned temporary paths.
	command := exec.Command("go", "run", ".", "-json", "-prs", prsPath, "-issues", issuesPath,
		"-contract", filepath.Join("..", "..", ".claude", "skills", "review-queue", "SKILL.md"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pipelinehealth failed: %v\n%s", err, output)
	}
	var got report
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode pipelinehealth output: %v\n%s", err, output)
	}
	if !hasIssueFinding(got, "triage_route_label_mismatch", 50) ||
		!hasIssueFinding(got, "triage_route_label_mismatch", 51) ||
		!hasIssueFinding(got, "fix_issue_not_executable", 52) {
		t.Fatalf("public command hid route diagnostics: %+v", got.Findings)
	}
}

func hasIssueFinding(result report, code string, issue int) bool {
	for _, item := range result.Findings {
		if item.Code == code && item.Issue == issue {
			return true
		}
	}
	return false
}
