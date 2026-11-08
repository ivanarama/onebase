package main

import (
	"strings"
	"testing"
)

func routingMentionComment(snapshot map[string]any, body string) {
	snapshot["edges"] = append(snapshot["edges"].([]any), map[string]any{"cursor": "MENTION", "node": map[string]any{
		"__typename": "IssueComment", "id": "IC_MENTION", "fullDatabaseId": "99999", "createdAt": "2026-10-29T00:20:00Z",
		"lastEditedAt": nil, "author": map[string]any{"login": "owner"}, "body": body}})
}

func TestPublicRoutingMentionsRemainOrdinaryComments(t *testing.T) {
	marker := "<!-- pp:review-routing-v1 kind=publication sha256=" + strings.Repeat("a", 64) + " -->"
	for _, mode := range []string{"disabled", "enabled"} {
		t.Run(mode, func(t *testing.T) {
			contract := routingSkillFixture(t, mode == "enabled")
			for name, body := range map[string]string{
				"ordinary":            "An ordinary review explanation.",
				"prose":               "The pp:review-routing- namespace is described here.",
				"inline":              "The review quotes `" + marker + "` without publishing a record.",
				"consumer":            "The skill contains <!-- pp:review-routing-v1-consumer stage=review -->.",
				"standalone-consumer": "<!-- pp:review-routing-v1-consumer stage=review -->",
				"fix-consumer":        "<!-- pp:review-routing-v1-consumer stage=fix -->",
				"merge-consumer":      "<!-- pp:review-routing-v1-consumer stage=merge -->",
				"tail-consumer":       "<!-- pp:review-routing-v1-consumer stage=tail -->",
				"backtick-fence":      "```text\n" + marker + "\n{}\n```",
				"tilde-fence":         "~~~text\n" + marker + "\n{}\n~~~",
				"unclosed-fence":      "```text\n" + marker + "\n{}",
				"quote":               "> " + marker + "\n> {}",
				"diff":                "An example diff:\n+ " + marker,
			} {
				t.Run(name, func(t *testing.T) {
					state := routingGolden(t, "two-legacy-rounds")
					routingMentionComment(state, body)
					args := []string{"-contract", contract}
					if mode == "enabled" {
						args = append(args, "-technical-handoff")
					}
					value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, args...)
					engineering, _ := value["engineering_waiting"].([]any)
					if err != nil || len(value["content_review_candidates"].([]any)) != 1 ||
						len(value["human_waiting"].([]any)) != 0 || len(engineering) != 0 {
						t.Fatalf("ordinary comment changed public selection: %v %v", value, err)
					}
					candidate := value["content_review_candidates"].([]any)[0].(map[string]any)
					if candidate["review_depth"] != float64(2) {
						t.Fatalf("mention changed the two canonical legacy proofs: %v", candidate)
					}
				})
			}
		})
	}
}

func TestPublicRoutingRecordBarriersRemainClosed(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled"} {
		t.Run(mode, func(t *testing.T) {
			contract := routingSkillFixture(t, mode == "enabled")
			for _, fault := range []string{"malformed", "future-version", "leading-space", "leading-newline", "leading-BOM", "consumer-unknown-stage", "consumer-extra-attribute", "consumer-future-version", "edited", "deleted", "unknown-actor"} {
				t.Run(fault, func(t *testing.T) {
					state := routingGolden(t, "two-legacy-rounds")
					body := "<!-- pp:review-routing-v1 kind=publication sha256=" + strings.Repeat("a", 64) + " -->\n{}"
					if fault == "future-version" {
						body = strings.Replace(body, "routing-v1", "routing-v2", 1)
					}
					switch fault {
					case "leading-space":
						body = " " + body
					case "leading-newline":
						body = "\n" + body
					case "leading-BOM":
						body = "\ufeff" + body
					case "consumer-unknown-stage":
						body = "<!-- pp:review-routing-v1-consumer stage=unknown -->"
					case "consumer-extra-attribute":
						body = "<!-- pp:review-routing-v1-consumer stage=review extra=yes -->"
					case "consumer-future-version":
						body = "<!-- pp:review-routing-v2-consumer stage=review -->"
					}
					routingMentionComment(state, body)
					node := routingObject(routingObject(state["edges"].([]any)[len(state["edges"].([]any))-1])["node"])
					switch fault {
					case "edited":
						node["lastEditedAt"] = "2026-10-29T00:21:00Z"
					case "deleted":
						state["edges"] = append(state["edges"].([]any), map[string]any{"cursor": "DELETED", "node": map[string]any{
							"__typename": "CommentDeletedEvent", "id": "DELETED", "createdAt": "2026-10-29T00:21:00Z"}})
					case "unknown-actor":
						node["author"] = nil
					}
					args := []string{"-contract", contract}
					if mode == "enabled" {
						args = append(args, "-technical-handoff")
					}
					value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, args...)
					if err != nil {
						t.Fatal(err)
					}
					for _, field := range []string{"review_candidates", "content_review_candidates", "fix_candidates", "merge_candidates", "merge_executable"} {
						if len(value[field].([]any)) != 0 {
							t.Fatalf("%s acquired %s: %v", fault, field, value[field])
						}
					}
					waiting := "human_waiting"
					if mode == "enabled" {
						waiting = "engineering_waiting"
					}
					if len(value[waiting].([]any)) != 1 {
						t.Fatalf("%s lost its explicit barrier: %v", fault, value)
					}
				})
			}
		})
	}
}
