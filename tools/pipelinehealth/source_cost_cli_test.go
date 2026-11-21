package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSourceCostPublicCLIReceiptAndClosedFailures(t *testing.T) {
	root, binary := repoRoot(t), pipelinehealthCLI(t)
	// Reuse the standalone fixture executable, rather than spawning this entire
	// race-instrumented test binary once per physical GraphQL request. The meter
	// and all assertions still run under the ordinary parent race test.
	helper := buildGhStub(t)
	preview := func(cost int) graphQLRetryStep {
		return graphQLRetryStep{Output: json.RawMessage(sourceCostResponse(t, cost, false))}
	}
	dataFor := func(value map[string]any, cost int) graphQLRetryStep {
		value[sourceGraphQLRateAlias] = map[string]any{"cost": cost}
		value[sourceGraphQLViewerAlias] = map[string]any{"login": "ivanarama"}
		raw, err := json.Marshal(map[string]any{"data": value})
		if err != nil {
			t.Fatal(err)
		}
		return graphQLRetryStep{Output: raw}
	}
	data := func(cost int) graphQLRetryStep {
		return dataFor(snapshotResult(0, nil, false, nil, 0, nil, false, nil), cost)
	}
	nullErrors := func(value graphQLRetryStep) graphQLRetryStep {
		value.Output = json.RawMessage(strings.TrimSuffix(string(value.Output), "}") + `,"errors":null}`)
		return value
	}
	wrongViewer := data(1)
	wrongViewer.Output = json.RawMessage(strings.Replace(string(wrongViewer.Output), `"login":"ivanarama"`, `"login":"other"`, 1))
	incomplete := graphQLRetryStep{Output: json.RawMessage(sourceCostResponse(t, 1, true))}
	dense := data(1)
	dense.Output = json.RawMessage(strings.Replace(string(dense.Output), `"data":{`,
		`"data":{"padding":[`+strings.Repeat("0,", sourceCostMaxJSONNodes)+"0],", 1))
	pageOne := dataFor(snapshotResult(0, nil, false, nil, 2, []any{gqlTestIssueNode("issue-1", 41, nil)}, true, "after-issue-1"), 1)
	changedPage := dataFor(snapshotResult(0, nil, false, nil, 3, []any{gqlTestIssueNode("issue-2", 42, nil)}, false, nil), 1)
	tests := []struct {
		name      string
		allowance int
		steps     []graphQLRetryStep
		calls     int
		exit      int
		complete  bool
		uncertain bool
		charged   int64
		redReport bool
	}{
		{"complete success", 7, []graphQLRetryStep{preview(2), data(1)}, 2, 0, true, false, 3, false},
		{"red findings are still complete", 7, []graphQLRetryStep{preview(2), data(1)}, 2, 1, true, false, 3, true},
		{"budget stops before data", 1, []graphQLRetryStep{preview(1)}, 1, 75, false, false, 1, false},
		{"errors null preview", 7, []graphQLRetryStep{nullErrors(preview(1))}, 1, 2, false, true, 7, false},
		{"errors null data", 7, []graphQLRetryStep{preview(1), nullErrors(data(1))}, 2, 2, false, true, 2, false},
		{"gateway preview no retry", 7, []graphQLRetryStep{{Error: "gh: HTTP 504\n", Exit: 1}}, 1, 2, false, true, 7, false},
		{"gateway data no retry", 7, []graphQLRetryStep{preview(1), {Error: "gh: HTTP 502\n", Exit: 1}}, 2, 2, false, true, 2, false},
		{"actual exceeds quote", 7, []graphQLRetryStep{preview(1), data(2)}, 2, 2, false, true, 3, false},
		{"wrong credential viewer", 7, []graphQLRetryStep{preview(1), wrongViewer}, 2, 2, false, true, 2, false},
		{"cost complete but checker schema incomplete", 7, []graphQLRetryStep{preview(1), incomplete}, 2, 2, false, false, 2, false},
		{"dense nodes no partial receipt", 7, []graphQLRetryStep{preview(1), dense}, 2, 2, false, true, 2, false},
		{"routing client shares meter", 7, []graphQLRetryStep{preview(1), data(1), preview(1), {Output: json.RawMessage(sourceCostResponse(t, 1, true))}}, 4, 0, true, false, 4, false},
		{"routing allowance exactly spent", 2, []graphQLRetryStep{preview(1), data(1)}, 2, 75, false, false, 2, false},
		{"queue churn keeps charged work", 9, []graphQLRetryStep{preview(1), pageOne, preview(1), changedPage, preview(1), data(1)}, 6, 0, true, false, 6, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			folder := t.TempDir()
			path := filepath.Join(folder, "steps.json")
			raw, err := json.Marshal(test.steps)
			if err != nil || os.WriteFile(path, raw, 0o600) != nil {
				t.Fatal("write isolated command fixture")
			}
			args := []string{"-transport", "graphql", "-json", "-source-graphql-budget", strconv.Itoa(test.allowance), "-source-graphql-nonce", sourceTestNonce}
			if strings.HasPrefix(test.name, "routing ") {
				args = append(args, "-technical-handoff")
			}
			contract := filepath.Join(root, ".claude", "skills", "review-queue", "SKILL.md")
			if test.redReport {
				contract = filepath.Join(folder, "old-contract.md")
				if err := os.WriteFile(contract, []byte("outdated contract"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-contract", contract)
			}
			//nolint:gosec // Executable is built from this test's public CLI package.
			command := exec.Command(binary, args...)
			command.Dir = root
			command.Env = append(os.Environ(), "GH_EXE="+helper, "GHSTUB_GRAPHQL_RETRY_FIXTURE="+path)
			output, runErr := command.Output()
			exit := 0
			if runErr != nil {
				var value *exec.ExitError
				if !errors.As(runErr, &value) {
					t.Fatal(runErr)
				}
				exit = value.ExitCode()
			}
			var result map[string]json.RawMessage
			if exit != test.exit || json.Unmarshal(output, &result) != nil {
				t.Fatalf("wrong public outcome: exit=%d want=%d output=%s err=%v", exit, test.exit, output, runErr)
			}
			var usage sourceGraphQLUsage
			if json.Unmarshal(result["source_graphql_usage"], &usage) != nil {
				t.Fatalf("no typed invocation receipt: %s", output)
			}
			contractRaw, err := os.ReadFile(contract)
			if err != nil {
				t.Fatal(err)
			}
			if usage.Protocol != sourceGraphQLCostProtocol || usage.Nonce != sourceTestNonce || usage.Allowance != int64(test.allowance) ||
				usage.Repo != "ivanarama/onebase" || usage.TrustedAccount != "ivanarama" || usage.ContractSHA256 != sourceSHA(contractRaw) ||
				usage.Complete != test.complete || usage.UncertainCost != test.uncertain || usage.ChargedUpperBound != test.charged ||
				usage.PhysicalAttempts != int64(test.calls) {
				t.Fatalf("unbound/wrong receipt: %+v", usage)
			}
			if !test.complete && len(result) != 1 {
				t.Fatalf("failed cost/full-report proof leaked partial queue data: %s", output)
			}
			counter, err := os.ReadFile(path + ".index")
			if err != nil || string(counter) != strconv.Itoa(test.calls) {
				t.Fatalf("wrong physical attempts: counter=%s err=%v", counter, err)
			}
			for i := 0; i < test.calls; i++ {
				raw, err := os.ReadFile(path + ".request-" + strconv.Itoa(i))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "dryRun: true") != (i%2 == 0) {
					t.Fatalf("preview/data protocol order changed: %s", raw)
				}
			}
		})
	}
}

func TestSourceCostPublicCLIInvalidOptInNeverCallsTransport(t *testing.T) {
	root, binary := repoRoot(t), pipelinehealthCLI(t)
	for _, args := range [][]string{
		{"-source-graphql-budget", "0"},
		{"-source-graphql-budget", "2", "-source-graphql-nonce", "wrong", "-json"},
		{"-source-graphql-budget", "2", "-source-graphql-nonce", sourceTestNonce},
		{"-source-graphql-budget", "2", "-source-graphql-nonce", sourceTestNonce, "-json", "-transport", "rest"},
		{"-source-graphql-budget", "2", "-source-graphql-nonce", sourceTestNonce, "-json", "-prs", "fixture.json"},
		{"-source-graphql-budget", "2", "-source-graphql-nonce", sourceTestNonce, "-json", "-routing-inspect", "1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			//nolint:gosec // Public CLI and deliberately nonexistent inert GH_EXE.
			command := exec.Command(binary, args...)
			command.Dir = root
			command.Env = append(os.Environ(), "GH_EXE="+filepath.Join(t.TempDir(), "must-not-run"))
			output, err := command.Output()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || len(output) != 0 || strings.Contains(string(exit.Stderr), "must-not-run") {
				t.Fatalf("invalid opt-in reached transport or produced proof: output=%s err=%v", output, err)
			}
		})
	}
}
