package reviewrouting

import (
	"fmt"
	"sort"
)

type Page struct {
	Header    map[string]any
	Edges     []any
	UpdatedAt any
	HasNext   bool
	EndCursor string
}

func identityFromGraphQL(pr map[string]any) map[string]any {
	base, head := mapValue(pr["baseRepository"]), mapValue(pr["headRepository"])
	return map[string]any{"base_repository_id": base["id"], "base_repository": base["nameWithOwner"],
		"base_ref": pr["baseRefName"], "pr_id": pr["id"], "pr_number": pr["number"],
		"head_repository_id": head["id"], "head_repository": head["nameWithOwner"],
		"head_ref": pr["headRefName"], "head_sha": pr["headRefOid"], "cross_repository": pr["isCrossRepository"]}
}

// NormalizePage validates one authenticated query's data object. The caller
// must still enforce all pages, stable headers and two identical full passes.
func NormalizePage(data any, repository, repositoryID string, number int64) (Page, error) {
	var result Page
	if !repositoryPattern.MatchString(repository) || !wireText(repositoryID) || number < 1 || number > 2147483647 {
		return result, fmt.Errorf("invalid routing repository/PR scope")
	}
	repo := mapValue(mapValue(data)["repository"])
	if repo["id"] != repositoryID || repo["nameWithOwner"] != repository {
		return result, fmt.Errorf("routing timeline repository identity changed")
	}
	pr := mapValue(repo["pullRequest"])
	identity := identityFromGraphQL(pr)
	digest, err := IdentityDigest(identity)
	if err != nil {
		return result, err
	}
	returned, err := integer(identity["pr_number"], 1, 2147483647)
	if err != nil {
		return result, err
	}
	if returned != number || identity["base_repository"] != repository || identity["base_repository_id"] != repositoryID {
		return result, fmt.Errorf("routing PR identity scope changed")
	}
	author, err := object(pr["author"], "id login")
	if err != nil {
		return result, err
	}
	if !wireText(author["id"]) || !matches(author["login"], loginPattern) {
		return result, fmt.Errorf("routing PR author identity unavailable")
	}
	if !matches(pr["baseRefOid"], headPattern) {
		return result, fmt.Errorf("routing integration base SHA is invalid")
	}
	if pr["state"] != "OPEN" && pr["state"] != "CLOSED" && pr["state"] != "MERGED" {
		return result, fmt.Errorf("routing lifecycle is unknown")
	}
	if _, ok := pr["isDraft"].(bool); !ok {
		return result, fmt.Errorf("routing draft state is unknown")
	}
	labels := mapValue(pr["labels"])
	labelNext, ok := mapValue(labels["pageInfo"])["hasNextPage"].(bool)
	if !ok {
		return result, fmt.Errorf("routing label completeness is unknown")
	}
	labelNodes, ok := labels["nodes"].([]any)
	if !ok {
		return result, fmt.Errorf("routing labels are incomplete")
	}
	labelNames := []string{}
	seen := map[string]bool{}
	for _, raw := range labelNodes {
		name := stringValue(mapValue(raw)["name"])
		if !wireText(name) || seen[name] {
			return result, fmt.Errorf("routing labels are invalid/duplicate")
		}
		seen[name] = true
		labelNames = append(labelNames, name)
	}
	sort.Strings(labelNames)
	names := []any{}
	for _, name := range labelNames {
		names = append(names, name)
	}
	header := map[string]any{"headRefOid": pr["headRefOid"], "baseRefOid": pr["baseRefOid"], "baseRefName": pr["baseRefName"],
		"state": pr["state"], "isDraft": pr["isDraft"], "identity": identity, "identity_sha256": digest,
		"pr_key": map[string]any{"base_repository_id": repositoryID, "pr_id": identity["pr_id"], "pr_number": identity["pr_number"]},
		"author": author, "labels": names, "labelsComplete": !labelNext}
	connection := mapValue(pr["timelineItems"])
	pageInfo := mapValue(connection["pageInfo"])
	next, ok := pageInfo["hasNextPage"].(bool)
	if !ok {
		return result, fmt.Errorf("routing timeline pagination is unknown")
	}
	values, ok := connection["edges"].([]any)
	if !ok {
		return result, fmt.Errorf("routing timeline edges are partial")
	}
	if _, err := edges(map[string]any{"edges": values}); err != nil {
		return result, err
	}
	cursor := stringValue(pageInfo["endCursor"])
	if next && !wireText(cursor) {
		return result, fmt.Errorf("routing pagination cursor is missing")
	}
	updatedAt, found := connection["updatedAt"]
	if !found {
		return result, fmt.Errorf("routing timeline watermark is missing")
	}
	return Page{header, values, updatedAt, next, cursor}, nil
}

func Equal(left, right any) bool { return equalValue(left, right) }

func ValidateSnapshot(snapshot map[string]any, repository string, number int64, head string) error {
	if _, err := object(snapshot, "headRefOid baseRefOid baseRefName state isDraft identity identity_sha256 pr_key author labels labelsComplete updatedAt edges"); err != nil {
		return err
	}
	digest, err := IdentityDigest(snapshot["identity"])
	if err != nil {
		return err
	}
	identity := snapshot["identity"].(map[string]any)
	returned, err := integer(identity["pr_number"], 1, 2147483647)
	if err != nil {
		return err
	}
	if returned != number || identity["base_repository"] != repository || identity["head_sha"] != head || snapshot["headRefOid"] != head || snapshot["identity_sha256"] != digest || snapshot["baseRefName"] != "main" || !matches(snapshot["baseRefOid"], headPattern) {
		return fmt.Errorf("typed snapshot differs from queue PR scope")
	}
	key := map[string]any{"base_repository_id": identity["base_repository_id"], "pr_id": identity["pr_id"], "pr_number": identity["pr_number"]}
	if !equalValue(key, snapshot["pr_key"]) {
		return fmt.Errorf("typed snapshot lifetime PR key differs")
	}
	if snapshot["state"] != "OPEN" && snapshot["state"] != "CLOSED" && snapshot["state"] != "MERGED" {
		return fmt.Errorf("typed snapshot lifecycle is unknown")
	}
	if _, ok := snapshot["isDraft"].(bool); !ok {
		return fmt.Errorf("typed snapshot draft state is unknown")
	}
	if _, ok := snapshot["labelsComplete"].(bool); !ok {
		return fmt.Errorf("typed snapshot label completeness is unknown")
	}
	author, err := object(snapshot["author"], "id login")
	if err != nil {
		return err
	}
	if !wireText(author["id"]) || !matches(author["login"], loginPattern) {
		return fmt.Errorf("typed snapshot author identity unavailable")
	}
	labels, ok := snapshot["labels"].([]any)
	if !ok {
		return fmt.Errorf("typed snapshot labels are unknown")
	}
	previous := ""
	for _, raw := range labels {
		name, ok := raw.(string)
		if !ok || !wireText(name) || previous != "" && name <= previous {
			return fmt.Errorf("typed snapshot labels are invalid, duplicate or unsorted")
		}
		previous = name
	}
	_, err = edges(snapshot)
	return err
}
