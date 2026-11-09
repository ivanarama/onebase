package main

import "testing"

// The ordinary public CLI must retain typed lifetime history even with the
// paired feature disabled. It must not advertise new execution capability.
func TestPublicDisabledRoutingRetainsUnknownActorHistory(t *testing.T) {
	for _, fault := range []string{"null", "missing", "empty", "trusted"} {
		t.Run(fault, func(t *testing.T) {
			state := routingGolden(t, "retained-clean")
			for _, raw := range state["edges"].([]any) {
				node := routingObject(routingObject(raw)["node"])
				if node["__typename"] != "IssueComment" {
					continue
				}
				switch fault {
				case "null":
					node["author"] = nil
				case "missing":
					delete(node, "author")
				case "empty":
					node["author"] = map[string]any{}
				}
			}
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-contract", routingSkillFixture(t, false))
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"content_review_candidates", "review_candidates", "fix_candidates", "merge_candidates", "merge_executable"} {
				if len(value[field].([]any)) != 0 {
					t.Fatalf("disabled typed history acquired %s: %v", field, value[field])
				}
			}
			if len(value["human_waiting"].([]any)) != 1 || value["review_routing_contract"] != nil || value["fix_execution_contract"] != nil || value["integration_owner"] != nil {
				t.Fatalf("disabled history lost its barrier or acquired authority: %v", value)
			}
		})
	}
}

func TestPublicDisabledRoutingPreservesOrdinaryForeignAndGhostComments(t *testing.T) {
	for _, author := range []any{nil, map[string]any{"login": "github-actions[bot]"}, map[string]any{"login": "foreign"}} {
		state := routingGolden(t, "two-legacy-rounds")
		body := "ordinary unrelated app: status"
		if routingObject(author)["login"] == "foreign" {
			body = "<!-- pp:review-routing-v1 fake foreign marker -->"
		}
		state["edges"] = append(state["edges"].([]any), map[string]any{"cursor": "UNRELATED", "node": map[string]any{
			"__typename": "IssueComment", "id": "IC_UNRELATED", "fullDatabaseId": "99999", "createdAt": "2026-10-29T00:00:10Z", "lastEditedAt": nil, "author": author, "body": body}})
		value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-contract", routingSkillFixture(t, false))
		if err != nil || len(value["content_review_candidates"].([]any)) != 1 || len(value["human_waiting"].([]any)) != 0 {
			t.Fatalf("ordinary disabled mode changed: %v %v", value, err)
		}
	}
}
