package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type graphQLRetryStep struct {
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error"`
	Exit   int             `json:"exit"`
}

// Invoked as GH_EXE by the real pipelinehealth CLI, with a private fixture
// directory and counter for this scenario. No live GitHub calls are possible.
func runGraphQLRetryFixture(path string) {
	//nolint:gosec // G703: path is provided by the parent test under t.TempDir.
	data, err := os.ReadFile(path)
	var steps []graphQLRetryStep
	if err != nil || json.Unmarshal(data, &steps) != nil {
		os.Exit(81)
	}
	//nolint:gosec // G703: this counter is adjacent to the parent's private fixture.
	counter, _ := os.ReadFile(path + ".index")
	index, _ := strconv.Atoi(string(counter))
	if index >= len(steps) {
		os.Exit(82)
	}
	request, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil || !json.Valid(request) {
		os.Exit(83)
	}
	//nolint:gosec // G703: only this test's fixture evidence is written.
	if os.WriteFile(path+".request-"+strconv.Itoa(index), request, 0o600) != nil {
		os.Exit(84)
	}
	//nolint:gosec // G703: only the isolated fixture counter is written.
	if os.WriteFile(path+".index", []byte(strconv.Itoa(index+1)), 0o600) != nil {
		os.Exit(85)
	}
	_, _ = os.Stdout.Write(steps[index].Output)
	_, _ = io.WriteString(os.Stderr, steps[index].Error)
	os.Exit(steps[index].Exit)
}

func TestGraphQLCLIGatewayReadRecovery(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	name := "pipelinehealth"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	//nolint:gosec // The output path and package are fixed by this test.
	build := exec.Command("go", "build", "-o", binary, "./tools/pipelinehealth")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marshal := func(value any) json.RawMessage {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	empty := graphQLRetryStep{Output: marshal(map[string]any{"data": snapshotResult(0, nil, false, nil, 0, nil, false, nil)})}
	gateway := func(code int) graphQLRetryStep {
		return graphQLRetryStep{Error: fmt.Sprintf("gh: HTTP %d\n", code), Exit: 1}
	}
	partial := gateway(504)
	partial.Output = marshal(map[string]any{"data": snapshotResult(0, nil, false, nil, 0, nil, false, nil), "errors": []any{map[string]any{"message": "RATE_LIMITED"}}})
	incomplete := graphQLRetryStep{Output: marshal(map[string]any{"data": map[string]any{"repository": map[string]any{}}})}
	pageOne := graphQLRetryStep{Output: marshal(map[string]any{"data": snapshotResult(0, nil, false, nil, 2, []any{gqlTestIssueNode("issue-1", 41, nil)}, true, "after-issue-1")})}
	pageTwo := graphQLRetryStep{Output: marshal(map[string]any{"data": snapshotResult(0, nil, false, nil, 2, []any{gqlTestIssueNode("issue-2", 42, nil)}, false, nil)})}
	changedPage := graphQLRetryStep{Output: marshal(map[string]any{"data": snapshotResult(0, nil, false, nil, 3, []any{gqlTestIssueNode("issue-2", 42, nil)}, false, nil)})}
	tests := []struct {
		name    string
		steps   []graphQLRetryStep
		calls   int
		want    string
		success bool
		classes []int // Equal classes must repeat the exact query and variables.
	}{
		{"healthy", []graphQLRetryStep{empty}, 1, "", true, nil},
		{"504 then complete snapshot", []graphQLRetryStep{gateway(504), empty}, 2, "", true, nil},
		{"502 503 then complete snapshot", []graphQLRetryStep{gateway(502), gateway(503), empty}, 3, "", true, nil},
		{"persistent 504 stops after three", []graphQLRetryStep{gateway(504), gateway(504), gateway(504), empty}, 3, "HTTP 504", false, nil},
		{"auth 401 is terminal", []graphQLRetryStep{gateway(401), empty}, 1, "HTTP 401", false, nil},
		{"forbidden 403 is terminal", []graphQLRetryStep{gateway(403), empty}, 1, "HTTP 403", false, nil},
		{"rate limit 429 is terminal", []graphQLRetryStep{gateway(429), empty}, 1, "HTTP 429", false, nil},
		{"500 is outside bounded policy", []graphQLRetryStep{gateway(500), empty}, 1, "HTTP 500", false, nil},
		{"GraphQL error with partial data is terminal", []graphQLRetryStep{partial, empty}, 1, "RATE_LIMITED", false, nil},
		{"invalid JSON is terminal", []graphQLRetryStep{{Output: json.RawMessage(`"broken"`)}, empty}, 1, "decode GraphQL envelope", false, nil},
		{"recovered read still validates schema", []graphQLRetryStep{gateway(504), incomplete, empty}, 2, "missing", false, nil},
		{"retry only the failed second page", []graphQLRetryStep{pageOne, gateway(504), pageTwo}, 3, "", true, []int{0, 1, 1}},
		{"queue churn after gateway restarts full snapshot", []graphQLRetryStep{pageOne, gateway(504), changedPage, empty}, 4, "", true, []int{0, 1, 1, 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "steps.json")
			if err := os.WriteFile(path, marshal(test.steps), 0o600); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // binary was built by this test under t.TempDir.
			command := exec.Command(binary, "-transport", "graphql", "-json")
			command.Dir = root
			command.Env = append(os.Environ(), "GH_EXE="+helper, "PIPELINEHEALTH_TEST_GH_HELPER=1", "PIPELINEHEALTH_TEST_GH_RETRY_FIXTURE="+path)
			output, err := command.Output()
			counter, readErr := os.ReadFile(path + ".index")
			if readErr != nil || string(counter) != strconv.Itoa(test.calls) {
				t.Fatalf("wrong transport attempts: got %q, want %d; err=%v, read=%v", counter, test.calls, err, readErr)
			}
			if test.success {
				if err != nil || !json.Valid(output) {
					t.Fatalf("gateway prevented completed read: err=%v stdout=%s", err, output)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 2 || len(output) != 0 || !strings.Contains(string(exit.Stderr), test.want) {
					t.Fatalf("failure was not closed: err=%v stdout=%s", err, output)
				}
			}
			requests := map[int]string{}
			for index := 0; index < test.calls; index++ {
				request, err := os.ReadFile(path + ".request-" + strconv.Itoa(index))
				class := 0
				if test.classes != nil {
					class = test.classes[index]
				}
				if previous, exists := requests[class]; err != nil || exists && string(request) != previous {
					t.Fatalf("retry changed query/variables: attempt=%d err=%v", index+1, err)
				}
				requests[class] = string(request)
				var payload struct {
					Query     string
					Variables map[string]any
				}
				if json.Unmarshal(request, &payload) != nil || !strings.HasPrefix(strings.TrimSpace(payload.Query), "query ") {
					t.Fatalf("transport attempted a non-query operation: %s", request)
				}
				if test.classes != nil && (class == 1 && payload.Variables["issueCursor"] != "after-issue-1" || class == 0 && payload.Variables["issueCursor"] != nil) {
					t.Fatalf("retry/restart lost the exact cursor: %s", request)
				}
			}
		})
	}
}
