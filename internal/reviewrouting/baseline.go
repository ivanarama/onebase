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
