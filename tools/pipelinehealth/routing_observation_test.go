package main

import (
	"slices"
	"testing"
)

func TestPublicRoutingObservationKeepsEvidenceAndLifetimeBudget(t *testing.T) {
	for _, test := range []struct {
		name, route, evidence string
		budget                any
	}{
		{"fresh-technical-source", "technical", "verified_current", float64(0)},
		{"pipeline-start-spent-once", "technical", "verified_current", float64(1)},
		{"two-attempts-engineering-parking", "engineering", "verified_current", float64(2)},
		{"changed-head-with-unsettled-pipeline", "engineering", "older_head_or_epoch", float64(1)},
		{"explicit-report-case-1", "decision", "verified_current", float64(0)},
		{"explicit-report-case-2", "decision", "verified_current", float64(0)},
		{"explicit-report-case-3", "engineering", "verified_current", float64(0)},
		{"missing-claim", "engineering", "unknown", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := routingGolden(t, test.name)
			value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
			if err != nil {
				t.Fatal(err)
			}
			observation := value["routing_observation"].(map[string]any)
			row := observation["items"].([]any)[0].(map[string]any)
			if observation["complete"] != (test.evidence == "verified_current") {
				t.Fatalf("unknown or older proof advertised a complete current classification: %v", observation)
			}
			if observation["version"] != "routing-observation-v1" || row["head"] != snapshot["headRefOid"] || row["number"] != float64(42) ||
				row["route"] != test.route || row["evidence_status"] != test.evidence || row["budget_used"] != test.budget || row["budget_limit"] != float64(2) {
				t.Fatalf("observation lost identity/category/spent budget: %v", row)
			}
			if test.evidence == "verified_current" && (row["source"] == nil || row["transition_at"] == nil || len(row["blockers"].([]any)) == 0) {
				t.Fatalf("current structured evidence was discarded: %v", row)
			}
			if test.evidence != "verified_current" && len(row["blockers"].([]any)) != 0 {
				t.Fatal("old or unverified evidence was presented as a current blocker")
			}
			category := map[string]string{"explicit-report-case-1": "product", "explicit-report-case-2": "safety_exception", "explicit-report-case-3": "insufficient_data"}[test.name]
			if category != "" && !slices.Contains(row["categories"].([]any), any(category)) {
				t.Fatalf("explicit evidence category became a generic decision: %v", row)
			}
			if test.name == "pipeline-start-spent-once" && (row["transition_kind"] != "started" || row["transition_id"] != "4003") {
				t.Fatalf("same-second canonical event order was lost: %v", row)
			}
		})
	}
}

func TestPublicRoutingObservationWaitsForLegacyDecisionElection(t *testing.T) {
	snapshot := routingGolden(t, "two-legacy-rounds")
	snapshot["labels"] = []any{"needs-decision"}
	value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil {
		t.Fatal(err)
	}
	observation := value["routing_observation"].(map[string]any)
	row := observation["items"].([]any)[0].(map[string]any)
	if len(value["human_waiting"].([]any)) != 1 || row["route"] != "decision" || row["recipient"] != "owner" ||
		row["evidence_status"] != "unknown" || row["budget_used"] != nil || len(row["categories"].([]any)) != 0 || observation["complete"] != false {
		t.Fatalf("legacy stop disappeared or acquired an inferred product category: %v", value)
	}
}
