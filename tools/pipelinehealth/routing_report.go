package main

import (
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

type routingContract struct {
	Protocol string            `json:"protocol"`
	Snapshot string            `json:"snapshot"`
	Readers  map[string]string `json:"reader_sha256"`
}

type routingPurposeContract struct {
	Protocol string   `json:"protocol"`
	Snapshot string   `json:"snapshot"`
	Purposes []string `json:"purposes"`
}

type routingFixCandidate struct {
	Stage  string         `json:"stage"`
	Number int            `json:"number"`
	Head   string         `json:"head"`
	Source map[string]any `json:"source"`
}

type routingSettlementCandidate struct {
	routingFixCandidate
	AttemptID string `json:"attempt_id"`
}

type routingProgressCandidate struct {
	routingFixCandidate
	AttemptID string `json:"attempt_id"`
	NewHead   string `json:"new_head"`
}

type routingContext struct {
	snapshots           map[int]routingSnapshot
	repository, trusted string
	maxAttempts         int64
	readers             map[string]string
	readersErr          error
	positiveReadersErr  error
	complete            bool
}

func prepareRouting(prs []apiPull, repository, trusted, contract, fixture string, limit int64) *routingContext {
	context := &routingContext{snapshots: loadRoutingSnapshots(newGHPipelineGraphQLClient(), prs, repository, fixture),
		repository: repository, trusted: trusted, maxAttempts: limit, complete: true}
	context.readers, context.readersErr = reviewrouting.ConsumerTrees(filepath.Dir(filepath.Dir(contract)))
	positiveReaders, positiveErr := reviewrouting.PositiveConsumerTrees(filepath.Dir(filepath.Dir(contract)))
	context.positiveReadersErr = positiveErr
	if positiveErr == nil && !maps.Equal(positiveReaders, context.readers) {
		context.positiveReadersErr = fmt.Errorf("positive REVIEW consumer trees changed between compatibility reads")
	}
	for _, pr := range prs {
		if pr.State != "open" || pr.Base.Ref != "main" {
			continue
		}
		entry, found := context.snapshots[pr.Number]
		if !found {
			entry.err = fmt.Errorf("typed queue target lacks its complete snapshot")
		}
		if entry.err == nil {
			entry.err = reviewrouting.ValidateSnapshot(entry.value, repository, int64(pr.Number), pr.Head.SHA)
		}
		if entry.err == nil {
			labels := []any{}
			names := []string{}
			for _, label := range pr.Labels {
				names = append(names, label.Name)
			}
			sort.Strings(names)
			for _, name := range names {
				labels = append(labels, name)
			}
			if entry.value["state"] != "OPEN" || entry.value["isDraft"] != pr.Draft || !reviewrouting.Equal(entry.value["labels"], labels) || entry.value["labelsComplete"] != true {
				entry.err = fmt.Errorf("typed snapshot lifecycle/labels changed from queue or are incomplete")
			}
		}
		if entry.err != nil {
			context.complete = false
		}
		context.snapshots[pr.Number] = entry
	}
	return context
}

func (context *routingContext) attach(result *report) {
	result.RoutingObservation = &routingObservation{Version: "routing-observation-v1", Complete: context.complete && context.readersErr == nil, Items: []routingObservationItem{}}
	fixes, settlements := []routingFixCandidate{}, []routingSettlementCandidate{}
	progress := []routingProgressCandidate{}
	positive := []routingFixCandidate{}
	technical, engineering := []candidate{}, []candidate{}
	result.TechnicalFixCandidates, result.TechnicalFixSettlementCandidates = &fixes, &settlements
	result.TechnicalFixProgressCandidates = &progress
	result.PositiveMergeCandidates = &positive
	result.TechnicalWaiting, result.EngineeringWaiting = &technical, &engineering
	if context.readersErr != nil {
		result.add("yellow", "routing_readers_incompatible", 0, context.readersErr.Error())
	}
	if context.complete && context.readersErr == nil {
		result.ReviewRoutingContract = &routingContract{reviewrouting.Version, "complete", context.readers}
		result.FixBridgeContract = &routingPurposeContract{"claimed-fix-v1", "complete", []string{"author-handoff", "author-progress"}}
		result.FixExecutionContract = &routingPurposeContract{"claimed-pipeline-fix-v1", "complete", []string{"admission", "start", "heartbeat", "progress", "settlement"}}
		if context.positiveReadersErr == nil {
			result.PositiveReviewContract = &routingPurposeContract{"positive-review-v1", "complete", []string{"merge", "tail"}}
		}
	}
	if !context.complete {
		result.add("yellow", "routing_snapshot_incomplete", 0, "typed routing snapshot is incomplete; no compatible contract or execution admission is advertised")
	}
	// These opt-in compatibility declarations grant no mutation/process authority.
	// Installation and enablement still require acceptance of the exact pair.
}

func (context *routingContext) engineering(result *report, item candidate, reason string) bool {
	item.Stage = "engineering-recovery"
	*result.EngineeringWaiting = append(*result.EngineeringWaiting, item)
	result.add("yellow", "routing_engineering_wait", item.Number, reason)
	return true
}

func (context *routingContext) technical(result *report, item candidate, reason string) bool {
	item.Stage = "technical-fix"
	*result.TechnicalWaiting = append(*result.TechnicalWaiting, item)
	result.add("yellow", "routing_technical_wait", item.Number, reason)
	return true
}

func routingFootprint(snapshot map[string]any, trusted string) bool {
	values, _ := snapshot["edges"].([]any)
	for _, raw := range values {
		node := routingObject(routingObject(raw)["node"])
		if node["__typename"] == "CommentDeletedEvent" {
			return true
		}
		if node["__typename"] == "IssueComment" && routingObject(node["author"])["login"] == trusted &&
			(strings.Contains(routingText(node["body"]), "pp:review-routing-") || node["lastEditedAt"] != nil) {
			return true
		}
	}
	return false
}

func legacyRoutingHistory(comments []apiComment, trusted string) bool {
	for _, comment := range comments {
		if strings.Contains(comment.Body, "<!-- pp:review-routing-") &&
			(comment.User.Login == trusted || strings.TrimSpace(comment.User.Login) == "") {
			return true
		}
	}
	return false
}

func routingPhases(ledger reviewrouting.Ledger, attemptID string) map[string]reviewrouting.Record {
	result := map[string]reviewrouting.Record{}
	for _, phase := range ledger.Phases {
		if phase.Payload["attempt_id"] == attemptID {
			result[routingText(phase.Payload["phase"])] = phase
		}
	}
	return result
}

func routingSettled(ledger reviewrouting.Ledger, attemptID string) bool {
	for _, item := range ledger.Settlements {
		if item.Payload["attempt_id"] == attemptID {
			return true
		}
	}
	return false
}

func (context *routingContext) consume(result *report, pr apiPull, item candidate, labels map[string]bool) bool {
	var observedLedger *reviewrouting.Ledger
	var observedEpoch string
	defer func() { context.observe(result, pr, item, observedLedger, observedEpoch) }()
	entry, found := context.snapshots[pr.Number]
	if !found {
		return context.engineering(result, item, "complete typed timeline is unavailable")
	}
	if entry.err != nil {
		return context.engineering(result, item, entry.err.Error())
	}
	snapshot := entry.value
	footprint := routingFootprint(snapshot, context.trusted)
	verified, err := reviewrouting.VerifiedDepth(snapshot, context.trusted)
	if err != nil {
		return context.engineering(result, item, err.Error())
	}
	if !footprint {
		if item.Depth != verified.Depth {
			return context.engineering(result, item, "legacy checker depth differs from independently verified complete history")
		}
		return false
	}
	item.Depth = verified.Depth
	ledger := verified.Ledger
	observedLedger = &ledger
	info, err := reviewrouting.CurrentEpoch(snapshot, context.trusted)
	if err != nil {
		return context.engineering(result, item, err.Error())
	}
	observedEpoch = info.Hash
	if !context.complete || context.readersErr != nil {
		return context.engineering(result, item, "paired checker/four-reader compatibility is incomplete; no typed target is admitted")
	}
	if pr.Draft || labels["hold"] || labels["manual"] {
		return true
	}
	if labels["needs-decision"] {
		result.HumanWaiting = append(result.HumanWaiting, item)
		return true
	}
	// Unknown old process/delivery is only an administrative observation hint.
	// A lease expiry, absent PID or terminal task cannot prove a stopped executor.
	if len(ledger.Attempts) > 0 {
		attempt := ledger.Attempts[len(ledger.Attempts)-1]
		phases := routingPhases(ledger, attempt.ID)
		_, done := phases["fix-done"]
		source := routingObject(attempt.Payload["source"])
		if !routingSettled(ledger, attempt.ID) && item.Head != source["head"] {
			if !labels["ship"] && !labels["changes-requested"] && len(ledger.Proofs) > 0 &&
				reviewrouting.Equal(ledger.Proofs[len(ledger.Proofs)-1].Source(), source) &&
				(!done || routingObject(phases["fix-done"].Payload["details"])["new_head"] == item.Head) &&
				routingProgressSafe(snapshot, ledger.Proofs[len(ledger.Proofs)-1], attempt, phases, context.trusted) {
				*result.TechnicalFixProgressCandidates = append(*result.TechnicalFixProgressCandidates,
					routingProgressCandidate{routingFixCandidate{"fix-pr", item.Number, source["head"].(string), source}, attempt.ID, item.Head})
			}
			// Retain the exact completed output for the original publisher's
			// final readback. This is neither another START nor a new attempt.
			if !done {
				*result.TechnicalFixSettlementCandidates = append(*result.TechnicalFixSettlementCandidates,
					routingSettlementCandidate{routingFixCandidate{"fix-pr", item.Number, item.Head, source}, attempt.ID})
				return context.engineering(result, item, "changed HEAD requires proof of the exact old executor and original delivery before one REVIEW resume; spent budget is preserved")
			}
		}
	}
	if ledger.ParkingID != "" {
		var grant *reviewrouting.Record
		for i := range ledger.Resumes {
			if ledger.Resumes[i].ID == ledger.ResumeID {
				grant = &ledger.Resumes[i]
				break
			}
		}
		if grant == nil || grant.Payload["head"] != item.Head {
			return context.engineering(result, item, "engineering parking needs one explicit exact-current-HEAD resume")
		}
	}
	// Recover the existing immutable report, never silently replace an older
	// unfinished publication with another model audit.
	records, _, err := reviewrouting.ReadRecords(snapshot, context.trusted)
	if err != nil {
		return context.engineering(result, item, err.Error())
	}
	completed, ignored := map[string]bool{}, map[string]bool{}
	for _, proof := range ledger.Proofs {
		completed[proof.Publication.ID] = true
	}
	for _, id := range ledger.IgnoredIDs {
		ignored[id] = true
	}
	for _, record := range records {
		if record.Payload["kind"] != "publication" || completed[record.ID] || ignored[record.ID] {
			continue
		}
		if routingObject(record.Payload["identity"])["head_sha"] == item.Head && record.Payload["epoch"] == info.Hash {
			result.ContentReviewCandidates = append(result.ContentReviewCandidates, item)
			return true
		}
		return context.engineering(result, item, "an older HEAD has unfinished immutable REVIEW publication; recover its original receipt")
	}
	if len(ledger.Proofs) == 0 {
		return context.engineering(result, item, "typed history has no canonical completed REVIEW")
	}
	proof := ledger.Proofs[len(ledger.Proofs)-1]
	current := proof.Source()["head"] == item.Head && proof.Publication.Payload["epoch"] == info.Hash
	if current {
		if !reviewrouting.Equal(proof.Publication.Payload["identity"], snapshot["identity"]) || !reviewrouting.Equal(proof.Publication.Payload["author"], snapshot["author"]) {
			return context.engineering(result, item, "canonical REVIEW identity or author differs from the current snapshot")
		}
		switch proof.Outcome() {
		case "reviewed":
			if context.positiveReadersErr != nil {
				return context.engineering(result, item, context.positiveReadersErr.Error())
			}
			positive, positiveErr := reviewrouting.PositiveReview(snapshot, context.trusted, false)
			if positiveErr != nil {
				return context.engineering(result, item, positiveErr.Error())
			}
			if labels["ship"] {
				if labels["changes-requested"] {
					return context.engineering(result, item, "positive typed source conflicts with a blocking FIX label")
				}
				trustedShip, shipErr := reviewrouting.PositiveShip(snapshot, context.trusted)
				if shipErr != nil || !trustedShip {
					return context.engineering(result, item, "positive typed source has no trusted current-epoch ship transition")
				}
				item.Stage = "merge"
				result.MergeCandidates = append(result.MergeCandidates, item)
				*result.PositiveMergeCandidates = append(*result.PositiveMergeCandidates, routingFixCandidate{"merge", item.Number, item.Head, positive.Descriptor})
				return true
			}
			result.ReviewedWaitingShip = append(result.ReviewedWaitingShip, item)
			return true
		case "needs-decision":
			result.HumanWaiting = append(result.HumanWaiting, item)
			return true
		case "engineering-handoff":
			return context.engineering(result, item, "finite technical budget or insufficient evidence requires engineering recovery")
		case "changes-requested":
			return context.engineering(result, item, "typed early-round correction requires explicit compatible FIX routing")
		case "technical-handoff":
			source := proof.Source()
			var root *reviewrouting.Record
			for i := range ledger.Attempts {
				if reviewrouting.Equal(ledger.Attempts[i].Payload["source"], source) {
					root = &ledger.Attempts[i]
					break
				}
			}
			if labels["ship"] || labels["changes-requested"] {
				return context.engineering(result, item, "typed technical source conflicts with a legacy blocking/ship label")
			}
			if root == nil && int64(len(ledger.Attempts)) >= context.maxAttempts {
				return context.engineering(result, item, "current lifetime technical budget is exhausted; prior slots are not refunded")
			}
			if root != nil {
				phases := routingPhases(ledger, root.ID)
				_, done := phases["fix-done"]
				_, barrier := phases["barrier"]
				if done || barrier || routingSettled(ledger, root.ID) {
					return context.engineering(result, item, "canonical technical attempt has ended or is ambiguous; no second provider")
				}
			}
			*result.TechnicalFixCandidates = append(*result.TechnicalFixCandidates, routingFixCandidate{"fix-pr", item.Number, item.Head, source})
			return context.technical(result, item, "one source-bound technical attempt; execution still requires the independent public admission/START gate")
		}
	}
	if len(ledger.Attempts) > 0 {
		previous := ledger.Attempts[len(ledger.Attempts)-1]
		done, ok := routingPhases(ledger, previous.ID)["fix-done"]
		if (!ok || routingObject(done.Payload["details"])["new_head"] != item.Head) && ledger.ResumeID == "" {
			return context.engineering(result, item, "new REVIEW lacks completed post-push provenance or an exact-HEAD resume")
		}
	}
	if proof.Outcome() == "technical-handoff" {
		reserved := false
		for _, attempt := range ledger.Attempts {
			if reviewrouting.Equal(attempt.Payload["source"], proof.Source()) {
				reserved = true
			}
		}
		if !reserved {
			return context.engineering(result, item, "technical REVIEW source still awaits its single bound FIX attempt")
		}
	}
	if labels["changes-requested"] {
		return context.engineering(result, item, "new typed REVIEW target is still in the legacy FIX route")
	}
	result.ContentReviewCandidates = append(result.ContentReviewCandidates, item)
	return true
}

// A progress candidate is only a source-bound readback hint. The coordinator
// must independently prove the finite forward graph, exact trailers, current
// identity, original process cleanup and retained local task before any write.
func routingProgressSafe(snapshot map[string]any, proof reviewrouting.ReviewProof, attempt reviewrouting.Record, phases map[string]reviewrouting.Record, trusted string) bool {
	if _, ok := phases["barrier"]; ok {
		return false
	}
	if _, ok := phases["handoff-done"]; !ok {
		return false
	}
	_, started := phases["started"]
	if (routingObject(attempt.Payload["executor"])["kind"] == "pipeline") != started {
		return false
	}
	anchor := proof.Publication.Payload["anchor"]
	found := false
	dangerous := map[string]bool{"HeadRefForcePushedEvent": true, "HeadRefDeletedEvent": true,
		"HeadRefRestoredEvent": true, "BaseRefChangedEvent": true, "BaseRefForcePushedEvent": true,
		"BaseRefDeletedEvent": true, "CommentDeletedEvent": true, "MergedEvent": true}
	for _, raw := range snapshot["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		if node["id"] == anchor {
			found = true
			continue
		}
		if !found {
			continue
		}
		if dangerous[routingText(node["__typename"])] {
			return false
		}
		if node["__typename"] == "IssueComment" && routingObject(node["author"])["login"] == trusted &&
			(node["lastEditedAt"] != nil || hasReviewAgain(routingText(node["body"]))) {
			return false
		}
	}
	return found
}
