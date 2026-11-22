package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// This optional observation never participates in routing decisions. The
// PromptPilot index remains the owner of candidate and recovery state.
type tailBacklogHealth struct {
	State                 string         `json:"state"`
	Discovery             *tailDiscovery `json:"discovery,omitempty"`
	Counts                map[string]int `json:"counts,omitempty"`
	Completions           map[string]int `json:"completions,omitempty"`
	RecheckDue            *int           `json:"recheck_due,omitempty"`
	ProofUnavailableCount *int           `json:"proof_unavailable_count,omitempty"`
	OldestReadyAt         string         `json:"oldest_ready_at,omitempty"`
	LastSuccessfulDrainAt string         `json:"last_successful_drain_at,omitempty"`
	LastDrainFailure      *tailFailure   `json:"last_drain_failure,omitempty"`
}

type tailDiscovery struct {
	State            string         `json:"state"`
	CompleteThrough  string         `json:"discovery_complete_through"`
	LastCompleteAt   string         `json:"last_complete_at"`
	LastError        string         `json:"last_error,omitempty"`
	VerifyPending    bool           `json:"verify_pending,omitempty"`
	PendingIntervals int            `json:"pending_intervals"`
	Uncovered        []tailInterval `json:"uncovered_intervals,omitempty"`
}

type tailInterval struct {
	Lower string `json:"lower"`
	Upper string `json:"upper"`
}

type tailFailure struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

func readTailBacklog(path, repository string, now time.Time) *tailBacklogHealth {
	var snapshot struct {
		tailBacklogHealth
		Repository string `json:"repository"`
		Base       string `json:"base"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &snapshot) != nil ||
		!strings.EqualFold(snapshot.Repository, repository) || snapshot.Base != "main" ||
		snapshot.Counts == nil || snapshot.Completions == nil ||
		(snapshot.ProofUnavailableCount != nil && *snapshot.ProofUnavailableCount < 0) ||
		(snapshot.RecheckDue != nil && *snapshot.RecheckDue < 0) {
		return &tailBacklogHealth{State: "unavailable"}
	}
	observation := snapshot.tailBacklogHealth
	observation.State = "not-observed"
	if scan := observation.Discovery; scan != nil {
		observation.State = "incomplete"
		if scan.LastError != "" || (observation.ProofUnavailableCount != nil && *observation.ProofUnavailableCount > 0) {
			observation.State = "unavailable"
		} else if scan.State == "complete" && scan.PendingIntervals == 0 && !scan.VerifyPending &&
			scan.CompleteThrough != "" && observation.Counts["pending-proof"] == 0 &&
			observation.RecheckDue != nil && *observation.RecheckDue == 0 &&
			observation.ProofUnavailableCount != nil {
			if freshTailTime(scan.LastCompleteAt, now) && freshTailTime(scan.CompleteThrough, now) {
				observation.State = "complete"
			} else {
				observation.State = "stale"
			}
		}
	}
	for _, counts := range []map[string]int{observation.Counts, observation.Completions} {
		for state, count := range counts {
			if count < 0 {
				return &tailBacklogHealth{State: "unavailable"}
			}
			if count == 0 && observation.State != "complete" {
				delete(counts, state)
			}
		}
	}
	return &observation
}

func freshTailTime(value string, now time.Time) bool {
	at, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !at.After(now) && now.Sub(at) < 5*time.Minute
}
