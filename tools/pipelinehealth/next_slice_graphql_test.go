package main

import (
	"strings"
	"testing"
)

func TestNextSliceTargetedClosedPullRead(t *testing.T) {
	issue := apiIssue{Number: 1274, State: "open", Labels: []apiLabel{{Name: "in-work"}}, Thread: []apiComment{
		{ID: 1, User: apiUser{Login: "ivanarama"}, CreatedAt: "2026-09-01T00:00:00Z", UpdatedAt: "2026-09-01T00:00:00Z", Body: "<!-- pp:plan-ready issue=1274 pr=1329 path=Plans/159-undefined-and-typed-empty-values.md -->"},
		{ID: 2, User: apiUser{Login: "ivanarama"}, CreatedAt: "2026-09-02T00:00:00Z", UpdatedAt: "2026-09-02T00:00:00Z", Body: "\u0412\u0437\u044f\u0442\u043e \u0432 \u0440\u0430\u0431\u043e\u0442\u0443: #1374. <!-- pp:in-work -->"},
	}}
	page := func(next bool, cursor any, comments []any) map[string]any {
		return map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"number": 1374, "title": "partial", "body": "Part of #1274", "url": "https://example.test/1374", "state": "MERGED", "headRefOid": headA, "merged": true, "mergeCommit": map[string]any{"oid": headB}, "comments": connection(2, comments, next, cursor), "timelineItems": connection(1, []any{map[string]any{"commit": map[string]any{"oid": headB}}}, false, nil)}}}
	}
	comment := func(id string) any {
		return map[string]any{"fullDatabaseId": id, "createdAt": "2026-09-02T00:00:00Z", "updatedAt": "2026-09-02T00:00:00Z", "lastEditedAt": nil, "body": "unrelated", "author": map[string]any{"login": "ivanarama"}}
	}
	client := &scriptedGraphQLClient{t: t, results: []any{page(true, "second", []any{comment("9007199254740993")}), page(false, nil, []any{comment("9007199254740994")})}}
	prs, err := loadNextSlicePulls(client, "ivanarama/onebase", "ivanarama", []apiIssue{issue}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 1374 || prs[0].SliceEvidence == nil || !prs[0].SliceEvidence.Complete || prs[0].SliceEvidence.MergeEvents != 1 {
		t.Fatalf("bad targeted observation: %+v", prs)
	}
	if len(client.calls) != 2 || client.calls[0].query != nextSlicePullQuery || client.calls[1].variables["cursor"] == nil {
		t.Fatalf("pagination not used: %+v", client.calls)
	}
	issue.Thread[1].User.Login = "attacker"
	client = &scriptedGraphQLClient{t: t}
	prs, err = loadNextSlicePulls(client, "ivanarama/onebase", "ivanarama", []apiIssue{issue}, nil)
	if err != nil || len(prs) != 0 || len(client.calls) != 0 {
		t.Fatal("untrusted marker triggered a lookup")
	}
	issue.Thread[1].User.Login = "ivanarama"
	edited := "2026-09-02T00:00:00Z"
	issue.Thread[1].LastEditedAt = &edited
	client = &scriptedGraphQLClient{t: t}
	_, err = loadNextSlicePulls(client, "ivanarama/onebase", "ivanarama", []apiIssue{issue}, nil)
	if err != nil || len(client.calls) != 0 {
		t.Fatal("same-second edit triggered a lookup")
	}
	issue.Thread[1].LastEditedAt = nil
	client = &scriptedGraphQLClient{t: t, results: []any{page(true, "same", []any{comment("1")}), page(true, "same", []any{comment("2")})}}
	_, err = loadNextSlicePulls(client, "ivanarama/onebase", "ivanarama", []apiIssue{issue}, nil)
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("repeated cursor accepted: %v", err)
	}
}
