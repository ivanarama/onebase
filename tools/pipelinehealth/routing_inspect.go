package main

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

func inspectRouting(repository, trusted, fixture string, number int) (map[string]any, error) {
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
	return value, nil
}
