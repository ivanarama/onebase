package reviewrouting

import (
	"fmt"
	"sort"
)

type ReviewProof struct{ Publication, Claim, Completion Record }

func (proof ReviewProof) Source() map[string]any {
	data := proof.Publication.Payload
	identityHash, _ := IdentityDigest(data["identity"])
	return map[string]any{"publication_id": proof.Publication.ID, "claim_id": proof.Claim.ID,
		"completion_id": proof.Completion.ID, "head": data["identity"].(map[string]any)["head_sha"],
		"identity_sha256": identityHash, "epoch": data["epoch"], "report_sha256": data["report_sha256"]}
}

func (proof ReviewProof) Outcome() string { return proof.Publication.Payload["outcome"].(string) }

func reviewProofs(records []Record) ([]ReviewProof, map[string]bool, error) {
	publications := []Record{}
	byEpoch := map[string]Record{}
	ignored := map[string]bool{}
	for _, record := range records {
		data := record.Payload
		if data["kind"] != "publication" {
			continue
		}
		key := data["identity"].(map[string]any)["head_sha"].(string) + "/" + data["epoch"].(string)
		winner, ok := byEpoch[key]
		if !ok {
			byEpoch[key] = record
			publications = append(publications, record)
			continue
		}
		left, err := Context(data)
		if err != nil {
			return nil, nil, err
		}
		right, err := Context(winner.Payload)
		if err != nil {
			return nil, nil, err
		}
		if left != right {
			return nil, nil, fmt.Errorf("conflicting publications for the same HEAD/epoch")
		}
		ignored[record.ID] = true
	}
	proofs := []ReviewProof{}
	for _, publication := range publications {
		context, err := Context(publication.Payload)
		if err != nil {
			return nil, nil, err
		}
		claims, completions := []Record{}, []Record{}
		for _, record := range records {
			data := record.Payload
			if data["kind"] != "claim" && data["kind"] != "completion" {
				continue
			}
			if ignored[data["publication_id"].(string)] {
				ignored[record.ID] = true
				continue
			}
			if data["publication_id"] != publication.ID {
				continue
			}
			if data["context_sha256"] != context || !before(publication, record) {
				return nil, nil, fmt.Errorf("proof does not bind its publication/order")
			}
			if err := ownerAt(records, publication, record, "review-publication"); err != nil {
				return nil, nil, err
			}
			if data["kind"] == "claim" {
				claims = append(claims, record)
			} else {
				completions = append(completions, record)
			}
		}
		if len(claims) > 0 {
			for _, record := range claims[1:] {
				ignored[record.ID] = true
			}
		}
		for _, completion := range completions {
			if len(claims) == 0 || completion.Payload["claim_id"] != claims[0].ID || !before(claims[0], completion) {
				return nil, nil, fmt.Errorf("completion does not bind canonical earliest claim")
			}
		}
		if len(completions) > 0 {
			for _, record := range completions[1:] {
				ignored[record.ID] = true
			}
			proofs = append(proofs, ReviewProof{publication, claims[0], completions[0]})
		}
	}
	known := map[string]bool{}
	for _, record := range records {
		if record.Payload["kind"] == "publication" {
			known[record.ID] = true
		}
	}
	for _, record := range records {
		data := record.Payload
		if (data["kind"] == "claim" || data["kind"] == "completion") && !known[data["publication_id"].(string)] {
			return nil, nil, fmt.Errorf("proof publication history is missing")
		}
	}
	sort.Slice(proofs, func(i, j int) bool { return before(proofs[i].Completion, proofs[j].Completion) })
	return proofs, ignored, nil
}

func attemptPhases(records, attempts []Record) ([]Record, error) {
	roots := map[string]Record{}
	for _, root := range attempts {
		roots[root.ID] = root
	}
	result := []Record{}
	seen := map[string]Record{}
	for _, record := range records {
		data := record.Payload
		if data["kind"] != "phase" {
			continue
		}
		root, ok := roots[data["attempt_id"].(string)]
		if !ok {
			return nil, fmt.Errorf("phase canonical attempt history is missing")
		}
		context, err := Context(root.Payload)
		if err != nil {
			return nil, err
		}
		if data["context_sha256"] != context || !before(root, record) {
			return nil, fmt.Errorf("phase does not bind its attempt/order")
		}
		if err := ownerAt(records, root, record, "technical-attempt"); err != nil {
			return nil, err
		}
		phase := data["phase"].(string)
		key := root.ID + "/" + phase
		if previous, ok := seen[key]; ok {
			if !equalValue(previous.Payload, data) {
				return nil, fmt.Errorf("conflicting phase receipts")
			}
			continue
		}
		done := map[string]bool{}
		for _, item := range result {
			if item.Payload["attempt_id"] == root.ID {
				done[item.Payload["phase"].(string)] = true
			}
		}
		required := map[string]string{"handoff-done": "assigned", "started": "handoff-done", "fix-done": "started"}
		if root.Payload["executor"].(map[string]any)["kind"] == "author" {
			required["fix-done"] = "handoff-done"
		}
		if prerequisite := required[phase]; prerequisite != "" && !done[prerequisite] {
			return nil, fmt.Errorf("phase prerequisite is missing")
		}
		if done["barrier"] {
			return nil, fmt.Errorf("attempt is parked behind ambiguity barrier")
		}
		if phase == "fix-done" {
			details := data["details"].(map[string]any)
			digest, err := Digest(root.Payload["source"])
			if err != nil {
				return nil, err
			}
			if details["source_trailer"] != digest || details["new_head"] == root.Payload["source"].(map[string]any)["head"] {
				return nil, fmt.Errorf("FIX completion source trailer/advanced HEAD differs")
			}
		}
		result = append(result, record)
		seen[key] = record
	}
	return result, nil
}

func attemptSettlements(records, attempts, phases []Record) ([]Record, error) {
	roots := map[string]Record{}
	for _, root := range attempts {
		roots[root.ID] = root
	}
	settled := map[string]Record{}
	result := []Record{}
	for _, record := range records {
		data := record.Payload
		if data["kind"] != "settlement" {
			continue
		}
		root, ok := roots[data["attempt_id"].(string)]
		if !ok {
			return nil, fmt.Errorf("settlement attempt history is missing")
		}
		context, err := Context(root.Payload)
		if err != nil {
			return nil, err
		}
		if !before(root, record) || !equalValue(data["source"], root.Payload["source"]) || data["context_sha256"] != context {
			return nil, fmt.Errorf("settlement lacks exact canonical attempt/context/source")
		}
		if previous, ok := settled[root.ID]; ok {
			if !equalValue(previous.Payload, data) {
				return nil, fmt.Errorf("conflicting attempt settlements")
			}
			continue
		}
		state := phasesFor(phases, root.ID)
		if _, ok := state["fix-done"]; ok {
			return nil, fmt.Errorf("settlement cannot replace successful FIX")
		}
		for _, phase := range state {
			if !before(phase, record) {
				return nil, fmt.Errorf("settlement precedes a later attempt phase")
			}
		}
		for _, phase := range []string{"barrier", "started"} {
			var expected any
			if node, ok := state[phase]; ok {
				expected = node.ID
			}
			if data[phase+"_id"] != expected {
				return nil, fmt.Errorf("settlement expected barrier/started receipt differs")
			}
		}
		expected := "not-started"
		if root.Payload["executor"].(map[string]any)["kind"] == "author" {
			expected = "author-unassigned"
		} else if _, ok := state["started"]; ok {
			expected = "pipeline-stopped"
		}
		if data["executor_outcome"] != expected {
			return nil, fmt.Errorf("settlement does not confirm this exact old executor")
		}
		if err := ownerAt(records, root, record, "technical-attempt"); err != nil {
			return nil, err
		}
		settled[root.ID] = record
		result = append(result, record)
	}
	return result, nil
}

func phasesFor(phases []Record, attemptID string) map[string]Record {
	result := map[string]Record{}
	for _, phase := range phases {
		if phase.Payload["attempt_id"] == attemptID {
			result[phase.Payload["phase"].(string)] = phase
		}
	}
	return result
}
