package main

import (
	"fmt"
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

type routingContext struct {
	snapshots           map[int]routingSnapshot
	repository, trusted string
	maxAttempts         int64
	readers             map[string]string
	readersErr          error
	complete            bool
}

func prepareRouting(prs []apiPull, repository, trusted, contract, fixture string, limit int64) *routingContext {
	context := &routingContext{snapshots: loadRoutingSnapshots(newGHPipelineGraphQLClient(), prs, repository, fixture),
		repository: repository, trusted: trusted, maxAttempts: limit, complete: true}
	context.readers, context.readersErr = reviewrouting.ConsumerTrees(filepath.Dir(filepath.Dir(contract)))
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
	fixes, settlements := []routingFixCandidate{}, []routingSettlementCandidate{}
	technical, engineering := []candidate{}, []candidate{}
	result.TechnicalFixCandidates, result.TechnicalFixSettlementCandidates = &fixes, &settlements
	result.TechnicalWaiting, result.EngineeringWaiting = &technical, &engineering
	if context.readersErr != nil {
		result.add("yellow", "routing_readers_incompatible", 0, context.readersErr.Error())
	}
	if context.complete && context.readersErr == nil {
		result.ReviewRoutingContract = &routingContract{reviewrouting.Version, "complete", context.readers}
	}
	if !context.complete {
		result.add("yellow", "routing_snapshot_incomplete", 0, "typed routing snapshot is incomplete; no compatible contract or execution admission is advertised")
	}
	// The execution capability is intentionally not emitted by this preparatory
	// checker until all four actual consumer routes pass the complete paired gate.
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
	info, err := reviewrouting.CurrentEpoch(snapshot, context.trusted)
	if err != nil {
		return context.engineering(result, item, err.Error())
	}
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
		if !done && !routingSettled(ledger, attempt.ID) && item.Head != source["head"] {
			*result.TechnicalFixSettlementCandidates = append(*result.TechnicalFixSettlementCandidates,
				routingSettlementCandidate{routingFixCandidate{"fix-pr", item.Number, item.Head, source}, attempt.ID})
			return context.engineering(result, item, "changed HEAD requires proof of the exact old executor and original delivery before one REVIEW resume; spent budget is preserved")
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
			// Native typed MERGE/TAIL acceptance remains a separate paired gate.
			// Never relabel this as an ordinary legacy proof or elect a loop.
			if labels["ship"] {
				return context.engineering(result, item, "typed reviewed source is waiting for qualified MERGE/TAIL consumer acceptance")
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
