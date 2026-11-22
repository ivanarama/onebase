package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestGraphQLRetryKeepsSharedPhysicalQueryBound(t *testing.T) {
	for _, remaining := range []int{0, 1, 2} {
		t.Run(strings.Repeat("available-", remaining), func(t *testing.T) {
			calls := 0
			transport := &ghPipelineGraphQLClient{run: func(_ context.Context, _ string, _ []byte, _, stderr io.Writer) error {
				calls++
				_, _ = io.WriteString(stderr, "gh: Bad Gateway (HTTP 502)\n")
				return errors.New("exit 1")
			}}
			bounded := &limitedPipelineGraphQLClient{delegate: transport, used: maxPipelineGraphQLQueries - remaining}
			_, _, err := loadStableGraphQLSnapshot(bounded, "ivanarama/onebase", "ivanarama", true, true)
			if err == nil || !strings.Contains(err.Error(), "exceeded") || calls != remaining || bounded.used != maxPipelineGraphQLQueries {
				t.Fatalf("physical budget was reset or nested: calls=%d used=%d err=%v", calls, bounded.used, err)
			}
		})
	}
}

func TestGraphQLGatewayDoesNotRetryMutationOrAmbiguousOperation(t *testing.T) {
	for _, query := range []string{"mutation { addComment { id } }", "subscription { event }", "queryMutation { write }", "{ viewer { login } }", "# query\nmutation { write }"} {
		t.Run(query, func(t *testing.T) {
			calls := 0
			transport := &ghPipelineGraphQLClient{run: func(_ context.Context, _ string, _ []byte, stdout, stderr io.Writer) error {
				calls++
				_, _ = io.WriteString(stdout, `{"data":{"value":"untrusted failure data"}}`)
				_, _ = io.WriteString(stderr, "gh: HTTP 504\n")
				return errors.New("exit 1")
			}}
			bounded := limitPipelineGraphQLClient(transport)
			destination := map[string]any{"value": "original"}
			err := bounded.Query(query, nil, &destination)
			if err == nil || calls != 1 || destination["value"] != "original" {
				t.Fatalf("failed operation replayed or leaked data: calls=%d destination=%v err=%v", calls, destination, err)
			}
		})
	}
}
