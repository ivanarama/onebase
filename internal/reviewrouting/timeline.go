package reviewrouting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	legacyClaim    = regexp.MustCompile(`^(?:PromptPilot service marker: REVIEW result publication claimed\.\n)?<!-- pp:review-claim ([0-9a-f]{40}) review-comment=([0-9]+) epoch-sha256=([0-9a-f]{64}) -->$`)
	legacyComplete = regexp.MustCompile(`^(?:PromptPilot service marker: REVIEW result committed\.\n)?<!-- pp:head-reviewed ([0-9a-f]{40}) review-comment=([0-9]+) claim=([0-9]+) epoch-sha256=([0-9a-f]{64}) -->$`)
	legacyReview   = regexp.MustCompile(`(?ms)^Reviewed-SHA: ([0-9a-f]{40})$.*^Outcome-Label: (reviewed|changes-requested|needs-decision)$.*^<!-- pp:review pp:tail=([0-9]+) -->$`)
	manualOverride = regexp.MustCompile(`(?m)^pp:review-again$`)
)

type timelineEdge struct {
	cursor, id, kind string
	node             map[string]any
}

func mapValue(value any) map[string]any { m, _ := value.(map[string]any); return m }
func stringValue(value any) string      { text, _ := value.(string); return text }
func actor(node map[string]any) string  { return stringValue(mapValue(node["author"])["login"]) }

func edges(snapshot map[string]any) ([]timelineEdge, error) {
	values, ok := snapshot["edges"].([]any)
	if !ok {
		return nil, fmt.Errorf("routing needs complete typed timeline edges")
	}
	cursors, ids := map[string]bool{}, map[string]bool{}
	result := []timelineEdge{}
	for _, raw := range values {
		edge, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("partial timeline edge")
		}
		node, ok := edge["node"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("partial timeline node")
		}
		cursor, id, kind := stringValue(edge["cursor"]), stringValue(node["id"]), stringValue(node["__typename"])
		if !wireText(cursor) || !wireText(id) || kind == "" || cursors[cursor] || ids[id] {
			return nil, fmt.Errorf("missing/duplicate timeline server identity")
		}
		cursors[cursor], ids[id] = true, true
		result = append(result, timelineEdge{cursor, id, kind, node})
	}
	return result, nil
}

func serverID(value any) (string, error) {
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case json.Number:
		text = string(v)
	case int:
		text = fmt.Sprint(v)
	case int64:
		text = fmt.Sprint(v)
	default:
		return "", fmt.Errorf("routing comment ID must be exact integer or decimal text")
	}
	if !idPattern.MatchString(text) {
		return "", fmt.Errorf("invalid bounded server comment ID")
	}
	return text, nil
}

func ReadRecords(snapshot map[string]any, trusted string) ([]Record, []string, error) {
	if !loginPattern.MatchString(trusted) {
		return nil, nil, fmt.Errorf("invalid trusted account")
	}
	if err := validatePR(snapshot["pr_key"]); err != nil {
		return nil, nil, err
	}
	items, err := edges(snapshot)
	if err != nil {
		return nil, nil, err
	}
	result, deleted := []Record{}, []string{}
	ids := map[string]bool{}
	for _, edge := range items {
		node := edge.node
		if edge.kind == "CommentDeletedEvent" {
			text, _ := node["createdAt"].(string)
			if _, err := serverTime(text); err != nil {
				return nil, nil, err
			}
			deleted = append(deleted, text)
		}
		if edge.kind != "IssueComment" || actor(node) != trusted {
			continue
		}
		body, ok := node["body"].(string)
		if !ok {
			return nil, nil, fmt.Errorf("trusted comment body is unavailable")
		}
		if !strings.Contains(body, "pp:review-routing-") {
			continue
		}
		if node["lastEditedAt"] != nil {
			return nil, nil, fmt.Errorf("trusted routing record was edited")
		}
		payload, err := Parse(body)
		if err != nil {
			return nil, nil, err
		}
		if !equalValue(payload["pr"], snapshot["pr_key"]) {
			return nil, nil, fmt.Errorf("routing record belongs to another lifetime PR identity")
		}
		id, err := serverID(node["fullDatabaseId"])
		if err != nil {
			return nil, nil, err
		}
		record := Record{id, stringValue(node["createdAt"]), payload}
		if err := record.validate(); err != nil {
			return nil, nil, err
		}
		if ids[id] {
			return nil, nil, fmt.Errorf("duplicate routing server ID")
		}
		ids[id] = true
		result = append(result, record)
		if len(result) > 4096 {
			return nil, nil, fmt.Errorf("routing lifetime history exceeds bound")
		}
	}
	sort.Slice(result, func(i, j int) bool { return before(result[i], result[j]) })
	return result, deleted, nil
}

type Epoch struct {
	Hash, AnchorID string
	anchorIndex    int
	suffix         []timelineEdge
}

func CurrentEpoch(snapshot map[string]any, trusted string) (Epoch, error) {
	items, err := edges(snapshot)
	if err != nil {
		return Epoch{}, err
	}
	info, err := epoch(items, stringValue(snapshot["headRefOid"]), trusted)
	if err != nil {
		return Epoch{}, err
	}
	if err := epochSafety(info, trusted); err != nil {
		return Epoch{}, err
	}
	return info, nil
}

func epoch(items []timelineEdge, head, trusted string) (Epoch, error) {
	var result Epoch
	anchor := -1
	if !headPattern.MatchString(head) {
		return result, fmt.Errorf("invalid epoch HEAD")
	}
	for i, edge := range items {
		node := edge.node
		if edge.kind == "PullRequestCommit" && mapValue(node["commit"])["oid"] == head || edge.kind == "HeadRefForcePushedEvent" && mapValue(node["afterCommit"])["oid"] == head || edge.kind == "HeadRefRestoredEvent" {
			anchor = i
		}
	}
	if anchor < 0 {
		return result, fmt.Errorf("HEAD has no server timeline anchor")
	}
	for i := anchor + 1; i < len(items); i++ {
		edge := items[i]
		if edge.kind == "IssueComment" && actor(edge.node) == trusted && edge.node["lastEditedAt"] == nil && manualOverride.MatchString(stringValue(edge.node["body"])) {
			anchor = i
		}
	}
	id := items[anchor].id
	for _, r := range id {
		if r > 127 {
			return result, fmt.Errorf("epoch server anchor is not ASCII")
		}
	}
	sum := sha256.Sum256([]byte("pp-review-epoch-v1\nhead=" + head + "\nanchor-node=" + id + "\n"))
	return Epoch{hex.EncodeToString(sum[:]), id, anchor, items[anchor+1:]}, nil
}

func epochSafety(info Epoch, trusted string) error {
	dangerous := map[string]bool{"HeadRefForcePushedEvent": true, "HeadRefDeletedEvent": true, "HeadRefRestoredEvent": true,
		"BaseRefChangedEvent": true, "BaseRefForcePushedEvent": true, "BaseRefDeletedEvent": true, "CommentDeletedEvent": true, "MergedEvent": true, "PullRequestCommit": true}
	for _, edge := range info.suffix {
		if dangerous[edge.kind] {
			return fmt.Errorf("unsupported epoch event %s", edge.kind)
		}
		if edge.kind == "IssueComment" && actor(edge.node) == trusted && edge.node["lastEditedAt"] != nil {
			return fmt.Errorf("trusted comment was edited in epoch")
		}
	}
	return nil
}

type Completed struct {
	Kind, CompletionID, PublicationID, ReviewID, Head, Epoch, Outcome string
	index                                                             int
}
type Verified struct {
	Depth     int
	Completed []Completed
	Ledger    Ledger
}

func legacyProof(info Epoch, head, trusted string) (*Completed, error) {
	type comment struct {
		id    string
		index int
		node  map[string]any
	}
	comments := []comment{}
	byID := map[string]comment{}
	for i, edge := range info.suffix {
		if edge.kind != "IssueComment" || actor(edge.node) != trusted || edge.node["lastEditedAt"] != nil {
			continue
		}
		id, err := serverID(edge.node["fullDatabaseId"])
		if err != nil {
			return nil, err
		}
		item := comment{id, i, edge.node}
		comments = append(comments, item)
		byID[id] = item
	}
	var winner *comment
	var winningReview string
	for _, item := range comments {
		match := legacyClaim.FindStringSubmatch(strings.TrimSpace(stringValue(item.node["body"])))
		if match == nil || match[1] != head || match[3] != info.Hash {
			continue
		}
		if !idPattern.MatchString(match[2]) {
			return nil, fmt.Errorf("invalid legacy claim review ID")
		}
		copy := item
		winner = &copy
		winningReview = match[2]
		break
	}
	if winner == nil {
		return nil, nil
	}
	for _, item := range comments {
		match := legacyComplete.FindStringSubmatch(strings.TrimSpace(stringValue(item.node["body"])))
		if match == nil || match[1] != head || match[4] != info.Hash || match[3] != winner.id || match[2] != winningReview || item.index <= winner.index {
			continue
		}
		review, ok := byID[match[2]]
		if !ok || review.index >= winner.index {
			continue
		}
		outcome := legacyReview.FindStringSubmatch(stringValue(review.node["body"]))
		if outcome == nil || outcome[1] != head {
			continue
		}
		return &Completed{Kind: "legacy", CompletionID: item.id, ReviewID: review.id, Head: head, Epoch: info.Hash, Outcome: outcome[2]}, nil
	}
	return nil, nil
}

// VerifiedDepth proves canonical legacy and typed transactions over the full
// normalized timeline. It does not authenticate a supplied offline snapshot.
func VerifiedDepth(snapshot map[string]any, trusted string) (Verified, error) {
	var result Verified
	items, err := edges(snapshot)
	if err != nil {
		return result, err
	}
	byID := map[string]timelineEdge{}
	positions := map[string]int{}
	for i, edge := range items {
		if edge.kind == "CommentDeletedEvent" {
			return result, fmt.Errorf("review depth has deleted/unknown proof history")
		}
		if edge.kind != "IssueComment" {
			continue
		}
		id, err := serverID(edge.node["fullDatabaseId"])
		if err != nil {
			return result, err
		}
		if _, ok := byID[id]; ok {
			return result, fmt.Errorf("duplicate server comment IDs")
		}
		byID[id], positions[id] = edge, i
		if actor(edge.node) == trusted && edge.node["lastEditedAt"] != nil {
			return result, fmt.Errorf("review depth has edited trusted history")
		}
	}
	completed := []Completed{}
	seenReviews := map[string]bool{}
	for _, edge := range items {
		if edge.kind != "IssueComment" || actor(edge.node) != trusted {
			continue
		}
		match := legacyComplete.FindStringSubmatch(strings.TrimSpace(stringValue(edge.node["body"])))
		if match == nil {
			continue
		}
		if !idPattern.MatchString(match[2]) || !idPattern.MatchString(match[3]) {
			return result, fmt.Errorf("invalid legacy proof ID")
		}
		if _, ok := byID[match[2]]; !ok {
			return result, fmt.Errorf("legacy completion references missing REVIEW")
		}
		if _, ok := byID[match[3]]; !ok {
			return result, fmt.Errorf("legacy completion references missing claim")
		}
		id, err := serverID(edge.node["fullDatabaseId"])
		if err != nil {
			return result, err
		}
		info, err := epoch(items[:positions[id]+1], match[1], trusted)
		if err != nil {
			return result, err
		}
		if err := epochSafety(info, trusted); err != nil {
			return result, err
		}
		proof, err := legacyProof(info, match[1], trusted)
		if err != nil {
			return result, err
		}
		if info.Hash != match[4] || proof == nil || proof.ReviewID != match[2] {
			return result, fmt.Errorf("legacy completion is not canonical claim-bound proof")
		}
		// legacyProof has already bound the earliest winning claim. Verify this
		// receipt's explicit claim rather than accepting another receipt's proof.
		canonical := legacyComplete.FindStringSubmatch(strings.TrimSpace(stringValue(byID[proof.CompletionID].node["body"])))
		if canonical == nil || canonical[3] != match[3] {
			return result, fmt.Errorf("legacy canonical claim differs")
		}
		if !seenReviews[proof.ReviewID] {
			seenReviews[proof.ReviewID] = true
			proof.index = positions[proof.CompletionID]
			completed = append(completed, *proof)
		}
	}
	history, deleted, err := ReadRecords(snapshot, trusted)
	if err != nil {
		return result, err
	}
	ledger, err := Reduce(history, deleted, 16)
	if err != nil {
		return result, err
	}
	type boundary struct {
		root Record
		id   string
	}
	boundaries := []boundary{}
	for _, record := range history {
		if record.Payload["kind"] == "publication" {
			boundaries = append(boundaries, boundary{record, record.ID})
		}
	}
	for _, proof := range ledger.Proofs {
		boundaries = append(boundaries, boundary{proof.Publication, proof.Completion.ID})
	}
	for _, entry := range boundaries {
		payload := entry.root.Payload
		info, err := epoch(items[:positions[entry.id]+1], payload["identity"].(map[string]any)["head_sha"].(string), trusted)
		if err != nil {
			return result, err
		}
		if err := epochSafety(info, trusted); err != nil {
			return result, err
		}
		if info.Hash != payload["epoch"] || info.AnchorID != payload["anchor"] {
			return result, fmt.Errorf("typed proof lacks matching historical server epoch/anchor")
		}
	}
	for _, proof := range ledger.Proofs {
		source := proof.Source()
		completed = append(completed, Completed{
			Kind: "typed", CompletionID: proof.Completion.ID, PublicationID: proof.Publication.ID, Head: source["head"].(string),
			Epoch: source["epoch"].(string), Outcome: proof.Outcome(), index: positions[proof.Completion.ID]})
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].index < completed[j].index })
	epochs := map[string]bool{}
	for _, item := range completed {
		key := item.Head + "/" + item.Epoch
		if epochs[key] {
			return result, fmt.Errorf("legacy/typed completions conflict in same HEAD epoch")
		}
		epochs[key] = true
	}
	for _, proof := range ledger.Proofs {
		prior := int64(0)
		for _, item := range completed {
			if item.index < positions[proof.Publication.ID] {
				prior++
			}
		}
		declared, _ := integer(proof.Publication.Payload["depth"], 0, 2147483647)
		if declared != prior {
			return result, fmt.Errorf("typed publication depth contradicts verified history")
		}
	}
	return Verified{len(completed), completed, ledger}, nil
}
