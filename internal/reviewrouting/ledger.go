package reviewrouting

import (
	"fmt"
	"sort"
	"time"
)

type Ledger struct {
	Proofs                                     []ReviewProof
	Attempts, Phases, Resumes, Settlements     []Record
	IgnoredIDs                                 []string
	ParkingID, ResumeID, ResumingPublicationID string
}

// Reduce rebuilds historical counters. Its caller must separately prove a
// complete authenticated timeline and historical server epochs/anchors.
// Deleted history is always unknown, including an otherwise empty ledger.
func Reduce(input []Record, deleted []string, maxAttempts int64) (Ledger, error) {
	var result Ledger
	if maxAttempts < 1 || maxAttempts > 16 || len(input) > 4096 {
		return result, fmt.Errorf("routing lifetime history is incomplete or oversized")
	}
	if len(deleted) != 0 {
		return result, fmt.Errorf("comment deletion makes lifetime budget unknown")
	}
	records := append([]Record{}, input...)
	ids := map[string]bool{}
	prHash := ""
	for _, record := range records {
		if err := record.validate(); err != nil {
			return result, err
		}
		if ids[record.ID] {
			return result, fmt.Errorf("duplicate routing server record ID")
		}
		ids[record.ID] = true
		hash, err := Digest(record.Payload["pr"])
		if err != nil {
			return result, err
		}
		if prHash != "" && prHash != hash {
			return result, fmt.Errorf("history crosses lifetime PR identities")
		}
		prHash = hash
	}
	sort.Slice(records, func(i, j int) bool { return before(records[i], records[j]) })
	proofs, ignored, err := reviewProofs(records)
	if err != nil {
		return result, err
	}
	proofMap := map[string]ReviewProof{}
	for _, proof := range proofs {
		proofMap[proof.Completion.ID] = proof
	}
	attempts := []Record{}
	byPrevious := map[string]Record{}
	for _, record := range records {
		data := record.Payload
		if data["kind"] != "attempt" {
			continue
		}
		source := data["source"].(map[string]any)
		proof, ok := proofMap[source["completion_id"].(string)]
		if !ok {
			return result, fmt.Errorf("technical attempt lacks canonical completed proof")
		}
		context, err := Context(proof.Publication.Payload)
		if err != nil {
			return result, err
		}
		if !equalValue(proof.Source(), source) || data["context_sha256"] != context || proof.Outcome() != "technical-handoff" || !before(proof.Completion, record) {
			return result, fmt.Errorf("technical attempt lacks exact canonical completed proof")
		}
		previous, _ := data["previous"].(string)
		if existing, ok := byPrevious[previous]; ok {
			left, err := Context(data)
			if err != nil {
				return result, err
			}
			right, err := Context(existing.Payload)
			if err != nil {
				return result, err
			}
			if left != right {
				return result, fmt.Errorf("conflicting attempt roots/executors")
			}
			ignored[record.ID] = true
			continue
		}
		latest := ""
		for _, item := range proofs {
			if before(item.Completion, record) {
				latest = item.Completion.ID
			}
		}
		if latest != proof.Completion.ID {
			return result, fmt.Errorf("attempt source is not latest completed REVIEW")
		}
		sequence, err := integer(data["attempt"], 1, 16)
		if err != nil {
			return result, err
		}
		expectedPrevious := ""
		expectedSeries := any(proof.Completion.ID)
		if len(attempts) > 0 {
			expectedPrevious = attempts[len(attempts)-1].ID
			expectedSeries = attempts[0].Payload["series"]
		}
		if int64(len(attempts)) >= maxAttempts || sequence != int64(len(attempts)+1) || previous != expectedPrevious || data["series"] != expectedSeries {
			return result, fmt.Errorf("attempt lifetime budget/continuity differs")
		}
		used, err := integer(proof.Publication.Payload["budget_used"], 0, 16)
		if err != nil {
			return result, err
		}
		if used != int64(len(attempts)) {
			return result, fmt.Errorf("attempt source has stale lifetime budget")
		}
		executor := data["executor"].(map[string]any)
		if executor["kind"] == "author" && executor["login"] != proof.Publication.Payload["author"].(map[string]any)["login"] {
			return result, fmt.Errorf("author handoff differs from bound PR author")
		}
		for _, item := range attempts {
			if equalValue(item.Payload["source"], source) {
				return result, fmt.Errorf("same source cannot reserve another attempt")
			}
		}
		attempts = append(attempts, record)
		byPrevious[previous] = record
	}
	roots := map[string]Record{}
	for _, item := range records {
		if item.Payload["kind"] == "publication" || item.Payload["kind"] == "attempt" {
			roots[item.ID] = item
		}
	}
	for _, item := range records {
		if item.Payload["kind"] == "lease" {
			if _, ok := roots[item.Payload["root"].(string)]; !ok {
				return result, fmt.Errorf("lease root history is missing")
			}
		}
	}
	for _, root := range roots {
		purpose := "technical-attempt"
		if root.Payload["kind"] == "publication" {
			purpose = "review-publication"
		}
		nodes, err := leaseNodes(records, root, purpose)
		if err != nil {
			return result, err
		}
		if _, err := resolveLease(nodes, time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)); err != nil {
			return result, err
		}
	}
	visible := []Record{}
	for _, item := range records {
		if !ignored[item.ID] {
			visible = append(visible, item)
		}
	}
	phases, err := attemptPhases(visible, attempts)
	if err != nil {
		return result, err
	}
	settlements, err := attemptSettlements(visible, attempts, phases)
	if err != nil {
		return result, err
	}
	settled := map[string]Record{}
	for _, item := range settlements {
		settled[item.Payload["attempt_id"].(string)] = item
	}
	publications := []Record{}
	for _, item := range visible {
		if item.Payload["kind"] == "publication" {
			publications = append(publications, item)
		}
	}
	for _, publication := range publications {
		var previous *Record
		for i := range attempts {
			if before(attempts[i], publication) {
				previous = &attempts[i]
			}
		}
		if previous == nil {
			continue
		}
		state := phasesFor(phases, previous.ID)
		completed, done := state["fix-done"]
		settlement, wasSettled := settled[previous.ID]
		resumedFailure := wasSettled && before(settlement, publication) && publication.Payload["resume_id"] != nil
		if !resumedFailure && (!done || !before(completed, publication) || completed.Payload["details"].(map[string]any)["new_head"] != publication.Payload["identity"].(map[string]any)["head_sha"] && publication.Payload["resume_id"] == nil) {
			return result, fmt.Errorf("new REVIEW lacks completed post-push provenance")
		}
	}
	// Every later attempt is also checked directly against its immediately
	// preceding canonical root, even when its publication is an equivalent loser.
	for i := 1; i < len(attempts); i++ {
		previous, current := attempts[i-1], attempts[i]
		proof := proofMap[current.Payload["source"].(map[string]any)["completion_id"].(string)]
		completed, done := phasesFor(phases, previous.ID)["fix-done"]
		settlement, wasSettled := settled[previous.ID]
		resumedFailure := wasSettled && before(settlement, proof.Publication) && proof.Publication.Payload["resume_id"] != nil
		if !resumedFailure && (!done || !before(completed, proof.Publication) || completed.Payload["details"].(map[string]any)["new_head"] != current.Payload["source"].(map[string]any)["head"] && proof.Publication.Payload["resume_id"] == nil) {
			return result, fmt.Errorf("next attempt lacks completed post-push provenance")
		}
	}
	for _, proof := range proofs {
		historical := int64(0)
		for _, attempt := range attempts {
			if before(attempt, proof.Publication) {
				historical++
			}
		}
		used, _ := integer(proof.Publication.Payload["budget_used"], 0, 16)
		if used != historical {
			return result, fmt.Errorf("publication budget differs from lifetime reservations")
		}
	}
	publicationIDs := map[string]bool{}
	for _, item := range publications {
		publicationIDs[item.ID] = true
	}
	settlementIDs := map[string]Record{}
	for _, item := range settlements {
		settlementIDs[item.ID] = item
	}
	activePark, resumingPublication := "", ""
	var activeResume *Record
	resumes := []Record{}
	for _, item := range records {
		data := item.Payload
		switch data["kind"] {
		case "resume":
			resumes = append(resumes, item)
			var previous any
			for _, root := range attempts {
				if before(root, item) {
					previous = root.ID
				}
			}
			if activePark == "" || data["parking_id"] != activePark || data["previous"] != previous || resumingPublication != "" {
				return result, fmt.Errorf("resume lacks current parking/lifetime state")
			}
			if settlement, ok := settlementIDs[activePark]; ok && data["head"] == settlement.Payload["source"].(map[string]any)["head"] {
				return result, fmt.Errorf("settled attempt resume requires new HEAD")
			}
			copy := item
			activeResume = &copy
		case "settlement":
			if _, ok := settlementIDs[item.ID]; !ok {
				continue
			}
			if activePark != "" || resumingPublication != "" {
				return result, fmt.Errorf("settlement cannot replace existing parking")
			}
			activePark = item.ID
			activeResume = nil
		case "publication":
			if !publicationIDs[item.ID] {
				continue
			}
			if activePark == "" {
				if data["resume_id"] != nil {
					return result, fmt.Errorf("unparked REVIEW cannot consume resume")
				}
				continue
			}
			if activeResume == nil || resumingPublication != "" || data["resume_id"] != activeResume.ID || activeResume.Payload["head"] != data["identity"].(map[string]any)["head_sha"] {
				return result, fmt.Errorf("parking requires one exact-HEAD resume before REVIEW")
			}
			resumingPublication = item.ID
		case "completion":
			proof, ok := proofMap[item.ID]
			if !ok {
				continue
			}
			if activePark != "" {
				if resumingPublication != proof.Publication.ID {
					return result, fmt.Errorf("parking appeared before REVIEW completion")
				}
				activePark = ""
				activeResume = nil
				resumingPublication = ""
			} else if proof.Publication.Payload["resume_id"] != nil {
				return result, fmt.Errorf("resume was already consumed by another REVIEW")
			}
			if proof.Outcome() == "engineering-handoff" {
				activePark = proof.Completion.ID
			}
		}
	}
	ignoredIDs := []string{}
	for id := range ignored {
		ignoredIDs = append(ignoredIDs, id)
	}
	sort.Strings(ignoredIDs)
	resumeID := ""
	if activeResume != nil {
		resumeID = activeResume.ID
	}
	return Ledger{proofs, attempts, phases, resumes, settlements, ignoredIDs, activePark, resumeID, resumingPublication}, nil
}

func optionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func recordValues(records []Record) []any {
	values := []any{}
	for _, record := range records {
		value := map[string]any{"id": record.ID}
		for key, item := range record.Payload {
			value[key] = item
		}
		values = append(values, value)
	}
	return values
}

func (ledger Ledger) Values() map[string]any {
	attemptIDs, resumeIDs, ignored := []any{}, []any{}, []any{}
	for _, item := range ledger.Attempts {
		attemptIDs = append(attemptIDs, item.ID)
	}
	for _, item := range ledger.Resumes {
		resumeIDs = append(resumeIDs, item.ID)
	}
	for _, id := range ledger.IgnoredIDs {
		ignored = append(ignored, id)
	}
	proofs := []any{}
	for _, proof := range ledger.Proofs {
		proofs = append(proofs, map[string]any{"source": proof.Source(), "outcome": proof.Outcome()})
	}
	var series any
	if len(ledger.Attempts) > 0 {
		series = ledger.Attempts[0].Payload["series"]
	}
	result := map[string]any{"budget_used": len(ledger.Attempts), "series": series, "attempt_ids": attemptIDs,
		"proofs": proofs, "phases": recordValues(ledger.Phases), "resume_ids": resumeIDs,
		"active_parking_id": optionalID(ledger.ParkingID), "active_resume_id": optionalID(ledger.ResumeID),
		"resuming_publication_id": optionalID(ledger.ResumingPublicationID), "ignored_ids": ignored, "mutation_authorized": false}
	if len(ledger.Settlements) > 0 {
		result["settlements"] = recordValues(ledger.Settlements)
	}
	return result
}
