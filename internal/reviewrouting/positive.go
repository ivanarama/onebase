package reviewrouting

import "fmt"

// Positive is a read-only source proof. It grants no ship, CI or mutation
// authority. The caller must obtain a complete authenticated snapshot first.
type Positive struct {
	Proof                  ReviewProof
	Descriptor             map[string]any
	MergeSHA, MergeEventID string
}

// PositiveShip checks the last actual label transition in this current epoch.
// It is still read-only evidence; label presence alone is not authorization.
func PositiveShip(snapshot map[string]any, trusted string) (bool, error) {
	info, err := CurrentEpoch(snapshot, trusted)
	if err != nil {
		return false, err
	}
	last := false
	found := false
	for _, item := range info.suffix {
		if (item.kind == "LabeledEvent" || item.kind == "UnlabeledEvent") && mapValue(item.node["label"])["name"] == "ship" {
			found = true
			last = item.kind == "LabeledEvent" && mapValue(item.node["actor"])["login"] == trusted
		}
	}
	return found && last, nil
}

// PositiveReview verifies the exact current typed REVIEW, including its
// observed merge boundary for TAIL. Historical audit bindings are checked on
// the full actual timeline, never on a fabricated former-HEAD snapshot.
func PositiveReview(snapshot map[string]any, trusted string, merged bool) (Positive, error) {
	var result Positive
	state := "OPEN"
	if merged {
		state = "MERGED"
	}
	if snapshot["state"] != state || snapshot["baseRefName"] != "main" || snapshot["isDraft"] != false || snapshot["labelsComplete"] != true {
		return result, fmt.Errorf("positive typed REVIEW lifecycle/labels are incomplete")
	}
	items, err := edges(snapshot)
	if err != nil {
		return result, err
	}
	verified, err := VerifiedDepth(snapshot, trusted)
	if err != nil {
		return result, err
	}
	ledger := verified.Ledger
	if len(ledger.Proofs) == 0 || ledger.ParkingID != "" {
		return result, fmt.Errorf("positive typed REVIEW has no completed unparked source")
	}
	proof := ledger.Proofs[len(ledger.Proofs)-1]
	if proof.Outcome() != "reviewed" || proof.Source()["head"] != snapshot["headRefOid"] || !Equal(proof.Publication.Payload["identity"], snapshot["identity"]) || !Equal(proof.Publication.Payload["author"], snapshot["author"]) {
		return result, fmt.Errorf("positive typed REVIEW source differs from current identity/author/HEAD")
	}
	history, _, err := ReadRecords(snapshot, trusted)
	if err != nil {
		return result, err
	}
	completed, ignored := map[string]bool{}, map[string]bool{}
	for _, item := range ledger.Proofs {
		completed[item.Publication.ID] = true
	}
	for _, id := range ledger.IgnoredIDs {
		ignored[id] = true
	}
	for _, item := range history {
		if item.Payload["kind"] == "publication" && !completed[item.ID] && !ignored[item.ID] {
			return result, fmt.Errorf("positive typed REVIEW has an unfinished publication")
		}
	}
	merges := []int{}
	for i, item := range items {
		if item.kind == "MergedEvent" {
			merges = append(merges, i)
		}
	}
	if merged && len(merges) != 1 || !merged && len(merges) != 0 {
		return result, fmt.Errorf("positive typed REVIEW has unsupported merge lifecycle")
	}
	cut := len(items)
	if merged {
		cut = merges[0]
	}
	prefix := map[string]any{}
	for key, value := range snapshot {
		prefix[key] = value
	}
	prefix["edges"] = snapshot["edges"].([]any)[:cut]
	info, err := CurrentEpoch(prefix, trusted)
	if err != nil {
		return result, err
	}
	if proof.Source()["epoch"] != info.Hash || proof.Publication.Payload["anchor"] != info.AnchorID {
		return result, fmt.Errorf("positive typed REVIEW is not the current server epoch")
	}
	positions, nodes := map[string]int{}, map[string]any{}
	for i, item := range items {
		if item.kind == "IssueComment" {
			id, idErr := serverID(item.node["fullDatabaseId"])
			if idErr != nil {
				return result, idErr
			}
			positions[id], nodes[id] = i, item.id
		}
	}
	publication, pubOK := positions[proof.Publication.ID]
	claim, claimOK := positions[proof.Claim.ID]
	completion, doneOK := positions[proof.Completion.ID]
	if !pubOK || !claimOK || !doneOK || info.anchorIndex >= publication || publication >= claim || claim >= completion || completion >= cut {
		return result, fmt.Errorf("positive typed REVIEW proof does not precede the exact merge boundary")
	}
	for _, item := range items[completion+1:] {
		if item.kind == "IssueComment" && actor(item.node) == trusted && HasReviewAgain(stringValue(item.node["body"])) {
			return result, fmt.Errorf("positive typed REVIEW was superseded by a later override")
		}
	}
	if merged {
		if _, timeErr := serverTime(stringValue(items[cut].node["createdAt"])); timeErr != nil {
			return result, fmt.Errorf("positive typed REVIEW merged event time is unavailable: %w", timeErr)
		}
		result.MergeSHA = stringValue(mapValue(items[cut].node["commit"])["oid"])
		result.MergeEventID = items[cut].id
		if !headPattern.MatchString(result.MergeSHA) {
			return result, fmt.Errorf("positive typed REVIEW merged commit is unavailable")
		}
		deleted := false
		for _, item := range items[cut+1:] {
			switch item.kind {
			case "HeadRefDeletedEvent":
				if deleted {
					return result, fmt.Errorf("positive typed REVIEW has repeated post-merge deletion")
				}
				deleted = true
			case "PullRequestCommit", "HeadRefForcePushedEvent", "HeadRefRestoredEvent", "BaseRefChangedEvent", "BaseRefForcePushedEvent", "BaseRefDeletedEvent", "MergedEvent":
				return result, fmt.Errorf("positive typed REVIEW has unsupported post-merge lifecycle")
			}
		}
	}
	result.Proof = proof
	result.Descriptor = map[string]any{"protocol": Version, "kind": "positive-review", "outcome": "reviewed", "source": proof.Source(), "identity": snapshot["identity"], "author": snapshot["author"], "anchor": info.AnchorID,
		"nodes": map[string]any{"publication": nodes[proof.Publication.ID], "claim": nodes[proof.Claim.ID], "completion": nodes[proof.Completion.ID]}}
	return result, nil
}
