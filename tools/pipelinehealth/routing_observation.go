package main

import (
	"slices"
	"time"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

// Observation is additive display data, never an execution contract. In
// particular a report category cannot grant a FIX, ship or resume permission.
type routingObservation struct {
	Version   string                   `json:"version"`
	Complete  bool                     `json:"complete"`
	Truncated bool                     `json:"truncated"`
	Items     []routingObservationItem `json:"items"`
}

type routingObservationItem struct {
	Number          int              `json:"number"`
	Head            string           `json:"head"`
	Route           string           `json:"route"`
	EvidenceStatus  string           `json:"evidence_status"`
	Categories      []string         `json:"categories"`
	Source          map[string]any   `json:"source,omitempty"`
	Blockers        []map[string]any `json:"blockers"`
	BlockersOmitted int              `json:"blockers_omitted"`
	Recipient       any              `json:"recipient"`
	BudgetUsed      *int             `json:"budget_used"`
	BudgetLimit     int64            `json:"budget_limit"`
	TransitionAt    string           `json:"transition_at,omitempty"`
	TransitionKind  string           `json:"transition_kind,omitempty"`
	TransitionID    string           `json:"transition_id,omitempty"`
	Reasons         []string         `json:"reasons"`
}

type routingObservationInput struct {
	pr     apiPull
	item   candidate
	ledger *reviewrouting.Ledger
	epoch  string
}

// Legacy election and single-flight selection must finish before observing
// their route. This never feeds display data back into election/admission.
func (context *routingContext) finishObservations(result *report) {
	for _, input := range context.observations {
		context.observe(result, input.pr, input.item, input.ledger, input.epoch)
	}
	context.observations = nil
}

func observedContains(items []candidate, number int) bool {
	return slices.ContainsFunc(items, func(item candidate) bool { return item.Number == number })
}

func (context *routingContext) observe(result *report, pr apiPull, item candidate, ledger *reviewrouting.Ledger, epoch string) {
	observation := result.RoutingObservation
	if observation == nil {
		return
	}
	if len(observation.Items) >= 128 {
		observation.Complete, observation.Truncated = false, true
		return
	}
	row := routingObservationItem{Number: item.Number, Head: item.Head, Route: "unknown", EvidenceStatus: "unknown",
		Categories: []string{}, Blockers: []map[string]any{}, Reasons: []string{}, Recipient: "unassigned", BudgetLimit: context.maxAttempts}
	switch {
	case observedContains(*result.EngineeringWaiting, item.Number):
		row.Route, row.Recipient = "engineering", "engineering"
	case pr.Draft || labelSet(pr.Labels)["hold"] || labelSet(pr.Labels)["manual"]:
		row.Route = "paused"
	case observedContains(*result.TechnicalWaiting, item.Number):
		row.Route, row.Recipient = "technical", "technical-fix"
	case observedContains(result.FixCandidates, item.Number):
		// The completed ordinary election also routes legacy corrections to
		// FIX. This identifies its role, not a typed category or executor.
		row.Route, row.Recipient = "technical", "technical-fix"
	case observedContains(result.HumanWaiting, item.Number):
		row.Route, row.Recipient = "decision", "owner"
	case observedContains(result.ReviewedWaitingShip, item.Number):
		row.Route, row.Recipient = "waiting_ship", "owner"
	case observedContains(result.MergeCandidates, item.Number):
		row.Route, row.Recipient = "merge", "merge"
	case observedContains(result.ContentReviewCandidates, item.Number) || observedContains(result.ReviewCandidates, item.Number):
		row.Route, row.Recipient = "review", "independent-review"
	}
	for _, reason := range result.Findings {
		if reason.PR == item.Number && (reason.Code == "routing_engineering_wait" || reason.Code == "routing_technical_wait") {
			row.Reasons = append(row.Reasons, reason.Message)
		}
	}
	if ledger != nil {
		used := len(ledger.Attempts)
		row.BudgetUsed = &used
		if len(ledger.Proofs) > 0 {
			proof := ledger.Proofs[len(ledger.Proofs)-1]
			row.Source, row.EvidenceStatus = proof.Source(), "older_head_or_epoch"
			row.TransitionAt, row.TransitionKind = proof.Completion.CreatedAt, "review-completion"
			row.TransitionID = proof.Completion.ID
			entry := context.snapshots[pr.Number]
			current := row.Source["head"] == item.Head && proof.Publication.Payload["epoch"] == epoch && epoch != "" &&
				reviewrouting.Equal(proof.Publication.Payload["identity"], entry.value["identity"]) &&
				reviewrouting.Equal(proof.Publication.Payload["author"], entry.value["author"])
			if current {
				row.EvidenceStatus = "verified_current"
				blocking := routingObject(proof.Publication.Payload["report"])["blocking"]
				for _, raw := range routingArray(blocking) {
					blocker := routingObject(raw)
					category := routingText(blocker["kind"])
					if !slices.Contains(row.Categories, category) {
						row.Categories = append(row.Categories, category)
					}
					if len(row.Blockers) < 64 {
						row.Blockers = append(row.Blockers, blocker)
					} else {
						row.BlockersOmitted++
					}
				}
			}
		}
		if len(ledger.Attempts) > 0 {
			attempt := ledger.Attempts[len(ledger.Attempts)-1]
			// An executor is bound to its source, not implicitly to a newer HEAD.
			if row.EvidenceStatus == "verified_current" && row.Route == "technical" && reviewrouting.Equal(attempt.Payload["source"], row.Source) {
				row.Recipient = attempt.Payload["executor"]
			}
			if observedLater(attempt.CreatedAt, attempt.ID, row.TransitionAt, row.TransitionID) {
				row.TransitionAt, row.TransitionKind = attempt.CreatedAt, "attempt-reserved"
				row.TransitionID = attempt.ID
			}
		}
		for _, records := range [][]reviewrouting.Record{ledger.Phases, ledger.Settlements} {
			for _, record := range records {
				if observedLater(record.CreatedAt, record.ID, row.TransitionAt, row.TransitionID) {
					row.TransitionAt = record.CreatedAt
					row.TransitionID = record.ID
					row.TransitionKind = routingText(record.Payload["phase"])
					if row.TransitionKind == "" {
						row.TransitionKind = routingText(record.Payload["kind"])
					}
				}
			}
		}
	}
	// A complete fetch is not a complete current classification. Unknown
	// history or an older source cannot prove zero current product blockers.
	if row.EvidenceStatus != "verified_current" {
		observation.Complete = false
	}
	observation.Items = append(observation.Items, row)
}

func routingArray(value any) []any {
	items, _ := value.([]any)
	return items
}

func observedLater(left, leftID, right, rightID string) bool {
	l, leftErr := time.Parse(time.RFC3339Nano, left)
	r, rightErr := time.Parse(time.RFC3339Nano, right)
	// Canonical authenticated record ids are positive decimal strings without
	// leading zeros. Compare their numeric order without narrowing 128 digits.
	laterID := len(leftID) > len(rightID) || len(leftID) == len(rightID) && leftID > rightID
	return leftErr == nil && (rightErr != nil || l.After(r) || l.Equal(r) && laterID)
}
