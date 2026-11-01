package main

import (
	"encoding/json"
	"testing"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

func TestPublicPipelineStartHintRequiresExactCurrentUnfinishedPipeline(t *testing.T) {
	contract := routingSkillFixture(t, true)
	for _, test := range []struct {
		name string
		want int
	}{
		{"fresh-technical-source", 0},
		{"pipeline-start-spent-once", 1},
		{"changed-head-with-unsettled-pipeline", 0},
		{"one-fix-done-needs-review", 0},
		{"two-attempts-engineering-parking", 0},
		{"settled-pipeline-budget-one", 0},
		{"explicit-report-case-1", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := routingGolden(t, test.name)
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-technical-handoff", "-contract", contract)
			if err != nil {
				t.Fatal(err)
			}
			starts, present := value["technical_fix_start_candidates"].([]any)
			if !present || len(starts) != test.want {
				t.Fatalf("public START allowlist missing or differs: %+v", value)
			}
			if test.want == 0 {
				return
			}
			verified, err := reviewrouting.VerifiedDepth(state, "owner")
			if err != nil {
				t.Fatal(err)
			}
			attempt := verified.Ledger.Attempts[len(verified.Ledger.Attempts)-1]
			started := routingPhases(verified.Ledger, attempt.ID)["started"]
			details := routingObject(started.Payload["details"])
			want := map[string]any{"stage": "fix-pr", "number": routingObject(state["identity"])["pr_number"], "head": state["headRefOid"],
				"source": attempt.Payload["source"], "attempt_id": attempt.ID, "started_id": started.ID,
				"task_id": details["task_id"], "attempt_uuid": details["attempt_uuid"]}
			// routingCLI's ordinary JSON reader uses float64. Decode its JSON
			// with the protocol reader before strict canonical-value comparison.
			raw, err := json.Marshal(starts[0])
			if err != nil {
				t.Fatal(err)
			}
			got, err := reviewrouting.Decode(raw, 1<<20)
			if err != nil || !reviewrouting.Equal(got, want) {
				t.Fatalf("START does not bind exact canonical task/source: got%+v want%+v", starts[0], want)
			}
		})
	}
}

func TestPublicPipelineStartHintRefusesStoppedOrIncompleteInputs(t *testing.T) {
	for _, fault := range []string{"hold", "manual", "ship", "changes-requested", "draft", "reader", "edited-start", "untrusted-start"} {
		t.Run(fault, func(t *testing.T) {
			state := routingGolden(t, "pipeline-start-spent-once")
			contract := routingSkillFixture(t, fault != "reader")
			switch fault {
			case "hold", "manual", "ship", "changes-requested":
				state["labels"] = []any{fault}
			case "draft":
				state["isDraft"] = true
			case "edited-start", "untrusted-start":
				for _, raw := range state["edges"].([]any) {
					node := routingObject(routingObject(raw)["node"])
					if node["__typename"] != "IssueComment" {
						continue
					}
					record, err := reviewrouting.Parse(routingText(node["body"]))
					if err == nil && record["kind"] == "phase" && record["phase"] == "started" {
						if fault == "edited-start" {
							node["lastEditedAt"] = node["createdAt"]
						} else {
							node["author"] = map[string]any{"login": "other"}
						}
					}
				}
			}
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-technical-handoff", "-contract", contract)
			if err != nil {
				t.Fatal(err)
			}
			starts, present := value["technical_fix_start_candidates"].([]any)
			if !present || len(starts) != 0 {
				t.Fatalf("%s admitted START: %+v", fault, value)
			}
		})
	}
}

func TestPublicSettlementReadbackSurvivesResumeWithoutAnotherStart(t *testing.T) {
	for _, test := range []struct {
		name               string
		settlement, review int
	}{
		{"settled-pipeline-budget-one", 1, 0},
		{"one-resume-with-spent-budget", 1, 1},
		{"consumed-resume-cannot-be-reused", 0, 0},
		{"resumed-review-clears-park-without-refund", 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := routingGolden(t, test.name)
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)},
				"-technical-handoff", "-contract", routingSkillFixture(t, true))
			if err != nil {
				t.Fatal(err)
			}
			if len(value["technical_fix_settlement_candidates"].([]any)) != test.settlement ||
				len(value["content_review_candidates"].([]any)) != test.review ||
				len(value["technical_fix_start_candidates"].([]any)) != 0 {
				t.Fatalf("administrative readback changed source/REVIEW/START routing: %+v", value)
			}
			if test.settlement == 1 {
				hint := routingObject(value["technical_fix_settlement_candidates"].([]any)[0])
				if hint["head"] != state["headRefOid"] || hint["head"] == routingObject(hint["source"])["head"] {
					t.Fatalf("settlement readback lost exact current/old-source distinction: %+v", hint)
				}
			}
		})
	}
}
