package reviewrouting

import (
	"reflect"
	"testing"
)

func TestReadRecordsDoesNotInterpretQuotedWireAsLifetimeHistory(t *testing.T) {
	var snapshot map[string]any
	for _, raw := range fixture(t, "review-routing-history-v1.json")["cases"].([]any) {
		item := raw.(map[string]any)
		if item["name"] == "third-typed-round" {
			snapshot = item["snapshot"].(map[string]any)
			break
		}
	}
	if snapshot == nil {
		t.Fatal("missing typed fixture")
	}
	original, deleted, err := ReadRecords(snapshot, "owner")
	if err != nil || len(original) == 0 || len(deleted) != 0 {
		t.Fatalf("fixture: %v", err)
	}
	wire, err := Render(original[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"prose":          "Review of the pp:review-routing- protocol.",
		"inline":         "Quoted `" + wire + "` example.",
		"fenced":         "```json\n" + wire + "\n```",
		"unclosed-fence": "~~~json\n" + wire,
		"quoted":         "> " + wire,
		"consumer":       "<!-- pp:review-routing-v1-consumer stage=review -->",
	} {
		t.Run(name, func(t *testing.T) {
			state := clone(t, snapshot)
			state["edges"] = append(state["edges"].([]any), map[string]any{"cursor": "QUOTED", "node": map[string]any{
				"__typename": "IssueComment", "id": "IC_QUOTED", "fullDatabaseId": "99999", "createdAt": "2026-10-29T00:20:00Z",
				"lastEditedAt": nil, "author": map[string]any{"login": "owner"}, "body": body}})
			got, deletions, err := ReadRecords(state, "owner")
			if err != nil || !reflect.DeepEqual(got, original) || !reflect.DeepEqual(deletions, deleted) {
				t.Fatalf("ordinary mention changed genuine typed history/attempt budget: %v", err)
			}
		})
	}
}
