package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// runGraphQLRetryFixture is an offline sequence for the real public CLI. Each
// fresh GH process consumes one response and records its actual stdin request;
// it never retries, contacts a network service, or invents a cost receipt.
func runGraphQLRetryFixture(path string) {
	if len(os.Args) < 3 || os.Args[1] != "api" || os.Args[2] != "graphql" {
		fmt.Fprintln(os.Stderr, "ghstub: sequence requires api graphql")
		os.Exit(80)
	}
	var steps []struct {
		Output json.RawMessage `json:"output"`
		Error  string          `json:"error"`
		Exit   int             `json:"exit"`
	}
	//nolint:gosec // Parent public-CLI test supplies this isolated fixture path.
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &steps) != nil {
		os.Exit(81)
	}
	//nolint:gosec // Adjacent counter is owned by the same isolated test.
	counter, _ := os.ReadFile(path + ".index")
	index, _ := strconv.Atoi(string(counter))
	if index < 0 || index >= len(steps) {
		os.Exit(82)
	}
	request, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil || !json.Valid(request) {
		os.Exit(83)
	}
	//nolint:gosec // Only the parent test's fixture evidence is written.
	if os.WriteFile(path+".request-"+strconv.Itoa(index), request, 0o600) != nil {
		os.Exit(84)
	}
	//nolint:gosec // Only the isolated fixture counter is written.
	if os.WriteFile(path+".index", []byte(strconv.Itoa(index+1)), 0o600) != nil {
		os.Exit(85)
	}
	_, _ = os.Stdout.Write(steps[index].Output)
	_, _ = io.WriteString(os.Stderr, steps[index].Error)
	os.Exit(steps[index].Exit)
}
