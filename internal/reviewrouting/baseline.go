package reviewrouting

import "fmt"

func projection(snapshot map[string]any, prefix []any) map[string]any {
	result := map[string]any{}
	for key, item := range snapshot {
		if key != "baseRefOid" && key != "updatedAt" {
			result[key] = item
		}
	}
	result["edges"] = prefix
	return result
}

func Capture(snapshot map[string]any) (map[string]any, error) {
	items, err := edges(snapshot)
	if err != nil {
		return nil, err
	}
	digest, err := Digest(projection(snapshot, snapshot["edges"].([]any)))
	if err != nil {
		return nil, err
	}
	var cursor, id any
	if len(items) > 0 {
		cursor, id = items[len(items)-1].cursor, items[len(items)-1].id
	}
	result := map[string]any{"snapshot_sha256": digest, "last_edge_cursor": cursor, "last_edge_node_id": id, "base_ref_oid": snapshot["baseRefOid"]}
	if err := audit(result); err != nil {
		return nil, err
	}
	return result, nil
}

// HistoricalAudit uses retained original metadata and server identities. Today's
// labels/state and later commit-date insertions cannot erase a completed audit.
// Live mutation gates still require VerifyAudit's strict complete prefix.
func HistoricalAudit(snapshot, binding, identity, author map[string]any, publicationIndex int, publicationContext, trusted string) (map[string]any, error) {
	if err := audit(binding); err != nil {
		return nil, err
	}
	items, err := edges(snapshot)
	if err != nil {
		return nil, err
	}
	if publicationIndex < 0 || publicationIndex > len(items) {
		return nil, fmt.Errorf("invalid historical publication position")
	}
	original := map[string]any{}
	if retained, ok := binding["history"]; ok {
		history := mapValue(retained)
		header := mapValue(history["header"])
		digest, err := IdentityDigest(identity)
		if err != nil {
			return nil, err
		}
		if !equalValue(header["identity"], identity) || !equalValue(header["author"], author) || header["headRefOid"] != identity["head_sha"] || !equalValue(header["pr_key"], snapshot["pr_key"]) || header["identity_sha256"] != digest {
			return nil, fmt.Errorf("retained audit header differs from its publication")
		}
		for key, value := range header {
			original[key] = value
		}
		manifest := history["edges"].([]any)
		wanted := map[string]string{}
		for _, raw := range manifest {
			edge := mapValue(raw)
			wanted[edge["node_id"].(string)] = edge["cursor"].(string)
		}
		observed, values := []any{}, []any{}
		for i, item := range items[:publicationIndex] {
			if cursor, ok := wanted[item.id]; ok && cursor == item.cursor {
				observed = append(observed, map[string]any{"cursor": item.cursor, "node_id": item.id})
				values = append(values, snapshot["edges"].([]any)[i])
			} else if item.kind != "PullRequestCommit" {
				own := false
				if item.kind == "IssueComment" && actor(item.node) == trusted {
					payload, parseErr := Parse(stringValue(item.node["body"]))
					if parseErr == nil {
						bound := stringValue(payload["context_sha256"])
						switch payload["kind"] {
						case "publication":
							bound, parseErr = Context(payload)
							own = parseErr == nil
						case "claim", "completion":
							own = true
						case "lease":
							own = payload["purpose"] == "review-publication"
						}
						own = own && bound == publicationContext
					}
				}
				if !own {
					return nil, fmt.Errorf("historical audit has unexplained independent event")
				}
			}
		}
		if !equalValue(observed, manifest) {
			return nil, fmt.Errorf("historical audit original nodes are missing or reordered")
		}
		original["edges"] = values
	} else {
		for key, value := range snapshot {
			original[key] = value
		}
		original["identity"], original["headRefOid"] = identity, identity["head_sha"]
		original["identity_sha256"], err = IdentityDigest(identity)
		if err != nil {
			return nil, err
		}
		original["edges"] = snapshot["edges"].([]any)[:publicationIndex]
	}
	original["baseRefOid"] = binding["base_ref_oid"]
	observed, err := Capture(original)
	if err != nil || !equalValue(observed["snapshot_sha256"], binding["snapshot_sha256"]) || observed["last_edge_cursor"] != binding["last_edge_cursor"] || observed["last_edge_node_id"] != binding["last_edge_node_id"] {
		return nil, fmt.Errorf("historical publication audit prefix is unavailable or changed")
	}
	return original, nil
}

func VerifyAudit(snapshot, binding map[string]any, publicationContext, trusted string) error {
	if err := audit(binding); err != nil {
		return err
	}
	if !hashPattern.MatchString(publicationContext) {
		return fmt.Errorf("invalid publication context")
	}
	items, err := edges(snapshot)
	if err != nil {
		return err
	}
	end := 0
	found := binding["last_edge_cursor"] == nil
	if !found {
		for i, item := range items {
			if item.cursor == binding["last_edge_cursor"] && item.id == binding["last_edge_node_id"] {
				end, found = i+1, true
				break
			}
		}
	}
	if !found {
		return fmt.Errorf("signed audit boundary is missing or changed")
	}
	values := snapshot["edges"].([]any)
	digest, err := Digest(projection(snapshot, values[:end]))
	if err != nil {
		return err
	}
	if digest != binding["snapshot_sha256"] {
		return fmt.Errorf("signed audit prefix changed")
	}
	records, deleted, err := ReadRecords(map[string]any{"pr_key": snapshot["pr_key"], "edges": values[end:]}, trusted)
	if err != nil {
		return err
	}
	if len(deleted) > 0 || len(records) != len(values)-end {
		return fmt.Errorf("audit suffix contains independent event/comment")
	}
	for _, record := range records {
		data := record.Payload
		bound := ""
		switch data["kind"] {
		case "publication":
			bound, err = Context(data)
			if err != nil {
				return err
			}
		case "lease", "claim", "completion":
			bound = data["context_sha256"].(string)
			if data["kind"] == "lease" && data["purpose"] != "review-publication" {
				return fmt.Errorf("audit suffix contains another lease purpose")
			}
		default:
			return fmt.Errorf("audit suffix contains another operation")
		}
		if bound != publicationContext {
			return fmt.Errorf("audit suffix differs from publication context")
		}
	}
	return nil
}
