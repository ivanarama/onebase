package main

import "testing"

func TestReviewAgainOutsideFencedCode(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		override bool
	}{
		{name: "backticks", body: "Instruction:\n```text\npp:review-again\n```"},
		{name: "tildes", body: "~~~text\npp:review-again\n~~~"},
		{name: "unclosed backticks", body: "```\npp:review-again"},
		{name: "unclosed tildes", body: "~~~\npp:review-again"},
		{name: "shorter closing fence", body: "````\n```\npp:review-again\n````"},
		{name: "different closing character", body: "```\n~~~\npp:review-again\n```"},
		{name: "closing fence with info", body: "```\n```text\npp:review-again\n```"},
		{name: "indented fences", body: "   ```text\npp:review-again\n   ```"},
		{name: "overindented closing fence", body: "```\n    ```\npp:review-again\n```"},
		{name: "CRLF fences", body: "```text\r\npp:review-again\n```\r\n"},
		{name: "inline code", body: "`pp:review-again`"},
		{name: "quoted line", body: "> pp:review-again"},
		{name: "indented marker", body: " pp:review-again"},
		{name: "trailing space", body: "pp:review-again "},
		{name: "plain marker", body: "pp:review-again", override: true},
		{name: "marker after backticks", body: "```\nexample\n```\npp:review-again", override: true},
		{name: "marker after longer close", body: "~~~\nexample\n~~~~ \t\npp:review-again", override: true},
		{name: "marker before unclosed fence", body: "pp:review-again\n```\nexample", override: true},
		{name: "invalid backtick info", body: "```info`\npp:review-again", override: true},
		{name: "overindented opener", body: "    ```\npp:review-again", override: true},
		{name: "short fence", body: "``\npp:review-again", override: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := addComment(testPR(10, headA, "reviewed"), 30, completion(headA, 20, 25))
			item = addComment(item, 31, tc.body)
			got := analyze([]apiPull{item}, "ivanarama")
			if returnedToReview := len(got.ReviewCandidates) == 1; returnedToReview != tc.override {
				t.Fatalf("returned to REVIEW = %v, want %v: %+v", returnedToReview, tc.override, got)
			}

			// A quoted instruction must not separate two completions into
			// different epochs and hide the duplicate-review finding.
			item = addComment(item, 40, completion(headA, 35, 36))
			got = analyze([]apiPull{item}, "ivanarama")
			if duplicate := hasFinding(got, "same_head_reviewed_twice"); duplicate != !tc.override {
				t.Fatalf("duplicate review = %v, want %v: %+v", duplicate, !tc.override, got)
			}
		})
	}
}
