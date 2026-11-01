package main

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

func inspectRouting(repository, trusted, fixture string, number int, positivePurpose string) (map[string]any, error) {
	value := map[string]any{"protocol": reviewrouting.Version, "purpose": "read-only-routing-inspection",
		"repository": repository, "number": number, "mutation_authorized": false, "authority": "authenticated-read-only-snapshot"}
	if fixture != "" {
		value["authority"] = "unverified-offline-input"
	}
	pr := apiPull{Number: number, State: "open"}
	pr.Base.Ref = "main"
	entry, found := loadRoutingSnapshots(newGHPipelineGraphQLClient(), []apiPull{pr}, repository, fixture)[number]
	if !found {
		entry.err = fmt.Errorf("routing target snapshot is unavailable")
	}
	finish := func(err error) (map[string]any, error) { value["error"] = err.Error(); return value, err }
	if entry.err != nil {
		return finish(entry.err)
	}
	snapshot := entry.value
	if err := reviewrouting.ValidateSnapshot(snapshot, repository, int64(number), routingText(snapshot["headRefOid"])); err != nil {
		return finish(err)
	}
	if snapshot["labelsComplete"] != true {
		return finish(fmt.Errorf("routing label snapshot is incomplete"))
	}
	value["snapshot"] = snapshot
	verified, err := reviewrouting.VerifiedDepth(snapshot, trusted)
	if err != nil {
		return finish(err)
	}
	value["verified_depth"], value["ledger"] = verified.Depth, verified.Ledger.Values()
	requireMerged := snapshot["state"] == "MERGED"
	if positivePurpose != "" {
		requireMerged = positivePurpose == "tail"
		value["positive_review_purpose"] = positivePurpose
	}
	positive, positiveErr := reviewrouting.PositiveReview(snapshot, trusted, requireMerged)
	if positiveErr == nil {
		var mergeSHA, mergeEvent any
		if positive.MergeSHA != "" {
			mergeSHA, mergeEvent = positive.MergeSHA, positive.MergeEventID
		}
		value["positive_review"] = map[string]any{"descriptor": positive.Descriptor, "merge_sha": mergeSHA, "merge_event": mergeEvent,
			"publication_created_at": positive.Proof.Publication.CreatedAt, "tail": routingObject(positive.Proof.Publication.Payload["report"])["tail"], "mutation_authorized": false}
	} else {
		value["positive_review_error"] = positiveErr.Error()
	}
	return value, nil
}
