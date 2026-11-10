package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

// Comments/merge shape alone cannot distinguish full content re-review from
// integration review. Only this narrow ambiguous case needs a complete epoch.
func needsFullCurrentReview(pr apiPull, trusted string) bool {
	count, completed, override := currentProtocolState(pr.Comments, trusted, pr.Head.SHA)
	return needsHeadParents(pr, trusted) && headIsBaseSyncMerge(pr) &&
		reviewDepth(pr.Comments, trusted) > count && count > 0 && override > 0 && override < completed
}

func loadFullCurrentReview(client pipelineGraphQLClient, pr apiPull, repository, trusted string) (bool, error) {
	identity, err := pullIdentity(pr, repository, "")
	if err != nil {
		return false, nil // Unknown REST-style identity cannot establish this fact.
	}
	snapshot, err := stableRoutingSnapshot(client, repository, pr.Base.Repo.NodeID, pr.Number)
	if err != nil {
		return false, fmt.Errorf("PR #%d full current review: %w", pr.Number, err)
	}
	if err := reviewrouting.ValidateSnapshot(snapshot, repository, int64(pr.Number), pr.Head.SHA); err != nil {
		return false, err
	}
	names := make([]string, 0, len(pr.Labels))
	for _, label := range pr.Labels {
		names = append(names, label.Name)
	}
	sort.Strings(names)
	labels := make([]any, 0, len(names))
	for _, name := range names {
		labels = append(labels, name)
	}
	if snapshot["identity_sha256"] != identity.digest || snapshot["baseRefOid"] != pr.Base.SHA ||
		snapshot["state"] != "OPEN" || snapshot["isDraft"] != pr.Draft ||
		!reviewrouting.Equal(snapshot["labels"], labels) || snapshot["labelsComplete"] != true ||
		!sameFullReviewComments(pr.Comments, snapshot) {
		return false, &pipelineSnapshotChurn{"full current review differs from queue snapshot"}
	}
	return verifiedFullCurrentReview(snapshot, trusted), nil
}

func sameFullReviewComments(comments []apiComment, snapshot map[string]any) bool {
	byID := make(map[int64]apiComment, len(comments))
	for _, comment := range comments {
		byID[comment.ID] = comment
	}
	seen := 0
	for _, raw := range snapshot["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		if node["__typename"] != "IssueComment" {
			continue
		}
		id, err := strconv.ParseInt(routingText(node["fullDatabaseId"]), 10, 64)
		comment, found := byID[id]
		updated := node["createdAt"]
		if node["lastEditedAt"] != nil {
			updated = node["lastEditedAt"]
		}
		if err != nil || !found || comment.Body != node["body"] || comment.CreatedAt != node["createdAt"] ||
			comment.UpdatedAt != updated || comment.User.Login != routingText(routingObject(node["author"])["login"]) {
			return false
		}
		delete(byID, id)
		seen++
	}
	return seen == len(comments) && len(byID) == 0
}

func verifiedFullCurrentReview(snapshot map[string]any, trusted string) bool {
	epoch, err := reviewrouting.CurrentEpoch(snapshot, trusted)
	if err != nil {
		return false
	}
	anchorFound := false
	afterAnchor := map[string]bool{}
	for _, raw := range snapshot["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		if node["__typename"] != "IssueComment" {
			continue
		}
		body := routingText(node["body"])
		// Integration markers stay conservative. Typed records share the body
		// boundary classifier used by ReadRecords and the ordinary fallback;
		// their original body and all strict parser checks remain unchanged.
		if hasBaseSyncRecordCandidate(body) || reviewrouting.HasRecordPrefix(body) {
			return false
		}
		if node["id"] == epoch.AnchorID {
			anchorFound = routingText(routingObject(node["author"])["login"]) == trusted &&
				node["lastEditedAt"] == nil && reviewrouting.HasReviewAgain(body)
		} else if anchorFound && strings.Contains(body, "pp:fix-handoff") {
			return false
		}
		if anchorFound && node["id"] != epoch.AnchorID {
			afterAnchor[routingText(node["fullDatabaseId"])] = true
		}
	}
	if !anchorFound {
		return false
	}
	verified, err := reviewrouting.VerifiedDepth(snapshot, trusted)
	if err != nil {
		return false
	}
	for _, proof := range verified.Completed {
		if afterAnchor[proof.CompletionID] && proof.Kind == "legacy" && proof.Head == snapshot["headRefOid"] &&
			proof.Epoch == epoch.Hash && proof.Outcome == "reviewed" {
			return true
		}
	}
	return false
}

// hasBaseSyncRecordCandidate recognizes an actual protocol-line candidate,
// including malformed/unknown versions, without treating explanations or quoted
// examples as integration history. Fence boundaries follow HasReviewAgain;
// leading noncanonical whitespace/BOM remains fail-closed, like HasRecordPrefix.
// This does not parse or authorize a carry: any candidate still closes this lane.
func hasBaseSyncRecordCandidate(body string) bool {
	var fence byte
	var fenceLength int
	for _, line := range strings.Split(body, "\n") {
		boundary := strings.TrimLeftFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
		if fence == 0 && strings.HasPrefix(boundary, "<!-- pp:base-sync-") {
			return true
		}
		text := strings.TrimLeft(line, " ")
		if len(line)-len(text) > 3 || len(text) < 3 || (text[0] != '`' && text[0] != '~') {
			continue
		}
		length := 1
		for length < len(text) && text[length] == text[0] {
			length++
		}
		if fence != 0 {
			if text[0] == fence && length >= fenceLength && strings.Trim(text[length:], " \t\r") == "" {
				fence, fenceLength = 0, 0
			}
			continue
		}
		if length >= 3 && (text[0] != '`' || !strings.Contains(text[length:], "`")) {
			fence, fenceLength = text[0], length
		}
	}
	return false
}
