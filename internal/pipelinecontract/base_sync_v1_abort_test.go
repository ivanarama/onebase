package pipelinecontract

import (
	"fmt"
	"testing"
)

func TestBaseSyncV1AbortIsDiagnosticAndReturnsExactHeadToFullReview(t *testing.T) {
	merge := skill(t, "merge-shepherd")
	review := skill(t, "review-queue")

	requireAllCompact(t, merge,
		"<!-- pp:base-sync-v1-aborted intent=<id> head=<40hex> reason=commit-before-intent -->",
		"Это только tombstone незавершённого intent, а не proof и не разрешение на merge",
		"Маркер валиден лишь от `ivanarama`, без редактирования, для текущего `head`",
		"последующий push ничего не закрывает и не даёт полномочий",
		"текущий HEAD должен пройти обычное полное REVIEW",
		"Новый trusted `LabeledEvent` после anchor текущего HEAD",
	)
	requireAllCompact(t, review,
		"<!-- pp:base-sync-v1-aborted intent=<id> head=<40hex> reason=commit-before-intent -->",
		"обычное полное содержательное REVIEW",
		"сокращённое интеграционное REVIEW здесь запрещено",
		"REVIEW не ставит `ship`",
	)
}

type v1AbortMarker struct {
	intentID string
	head     string
	body     string
	actor    string
	edited   bool
	sequence int
}

type currentHeadReviewProof struct {
	head      string
	canonical bool
	reviewed  bool
}

type v1AbortMergeState struct {
	marker                  v1AbortMarker
	currentHead             string
	currentHeadAnchor       int
	latestHeadEvent         int
	review                  currentHeadReviewProof
	shipTransitions         []shipTransition
	latestLifecycleOverride bool
}

func exactV1AbortMarker(intentID, head string) string {
	return fmt.Sprintf("<!-- pp:base-sync-v1-aborted intent=%s head=%s reason=commit-before-intent -->", intentID, head)
}

// mergeAfterV1Abort models the ordinary exact-HEAD authorization reached after
// recovery. The abort marker only tombstones an unprovable v1 transaction; it
// is deliberately insufficient without a fresh canonical review and human ship.
func mergeAfterV1Abort(state v1AbortMergeState) bool {
	marker := state.marker
	if marker.intentID == "" || marker.head == "" || marker.actor != "ivanarama" || marker.edited ||
		marker.body != exactV1AbortMarker(marker.intentID, marker.head) || marker.head != state.currentHead {
		return false
	}
	if state.latestHeadEvent != state.currentHeadAnchor || state.latestLifecycleOverride {
		return false
	}
	if !state.review.canonical || !state.review.reviewed || state.review.head != state.currentHead {
		return false
	}
	if len(state.shipTransitions) == 0 {
		return false
	}
	latestShip := state.shipTransitions[0]
	for _, transition := range state.shipTransitions[1:] {
		if transition.sequence > latestShip.sequence {
			latestShip = transition
		}
	}
	return latestShip.actor == "ivanarama" && latestShip.labeled &&
		latestShip.sequence > state.currentHeadAnchor && latestShip.sequence > marker.sequence
}

func TestBaseSyncV1AbortMarkerNeverAuthorizesMergeByItself(t *testing.T) {
	const head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	marker := v1AbortMarker{
		intentID: "5732000770",
		head:     head,
		body:     exactV1AbortMarker("5732000770", head),
		actor:    "ivanarama",
		sequence: 20,
	}
	base := v1AbortMergeState{
		marker:            marker,
		currentHead:       head,
		currentHeadAnchor: 10,
		latestHeadEvent:   10,
	}

	if mergeAfterV1Abort(base) {
		t.Fatal("the v1 abort marker alone must never authorize merge")
	}

	base.review = currentHeadReviewProof{head: head, canonical: true, reviewed: true}
	base.shipTransitions = []shipTransition{{sequence: 30, actor: "ivanarama", labeled: true}}
	if !mergeAfterV1Abort(base) {
		t.Fatal("full current-HEAD proof plus a new trusted human ship must restore the ordinary merge path")
	}

	tests := []struct {
		name   string
		mutate func(*v1AbortMergeState)
	}{
		{name: "marker by wrong actor", mutate: func(s *v1AbortMergeState) { s.marker.actor = "github-actions[bot]" }},
		{name: "edited marker", mutate: func(s *v1AbortMergeState) { s.marker.edited = true }},
		{name: "malformed marker", mutate: func(s *v1AbortMergeState) { s.marker.body += " " }},
		{name: "marker for wrong head", mutate: func(s *v1AbortMergeState) {
			s.marker.head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			s.marker.body = exactV1AbortMarker(s.marker.intentID, s.marker.head)
		}},
		{name: "later push including ABA", mutate: func(s *v1AbortMergeState) { s.latestHeadEvent = 40 }},
		{name: "later lifecycle override", mutate: func(s *v1AbortMergeState) { s.latestLifecycleOverride = true }},
		{name: "missing canonical proof", mutate: func(s *v1AbortMergeState) { s.review.canonical = false }},
		{name: "non-reviewed proof", mutate: func(s *v1AbortMergeState) { s.review.reviewed = false }},
		{name: "proof for wrong head", mutate: func(s *v1AbortMergeState) { s.review.head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }},
		{name: "missing new ship", mutate: func(s *v1AbortMergeState) { s.shipTransitions = nil }},
		{name: "ship before current anchor", mutate: func(s *v1AbortMergeState) { s.shipTransitions[0].sequence = 9 }},
		{name: "old ship before abort", mutate: func(s *v1AbortMergeState) { s.shipTransitions[0].sequence = 15 }},
		{name: "ship by wrong actor", mutate: func(s *v1AbortMergeState) { s.shipTransitions[0].actor = "app" }},
		{name: "latest transition removes ship", mutate: func(s *v1AbortMergeState) {
			s.shipTransitions = append(s.shipTransitions, shipTransition{sequence: 31, actor: "ivanarama", labeled: false})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := base
			state.shipTransitions = append([]shipTransition(nil), base.shipTransitions...)
			tt.mutate(&state)
			if mergeAfterV1Abort(state) {
				t.Fatal("unproven or stale v1-abort recovery authorized merge")
			}
		})
	}
}
