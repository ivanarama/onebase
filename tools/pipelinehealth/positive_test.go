package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

func positiveVectors(t *testing.T) []any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "reviewrouting", "testdata", "positive-review-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := reviewrouting.Decode(data, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	originBytes, err := os.ReadFile(filepath.Join("..", "..", "internal", "reviewrouting", "testdata", "positive-review-origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	origin, err := reviewrouting.Decode(originBytes, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	metadata, golden := routingObject(origin), routingObject(value)
	if metadata["sha256"] != fmt.Sprintf("%x", sha256.Sum256(data)) || metadata["source_head"] != golden["source_head"] || golden["authority"] != "unverified-offline-input" {
		t.Fatal("positive shared-vector provenance or raw UTF-8/LF bytes changed")
	}
	cases := golden["cases"].([]any)
	if len(cases) != 21 {
		t.Fatal("positive shared-vector cases are incomplete")
	}
	return cases
}

func positiveSnapshot(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, raw := range positiveVectors(t) {
		value := routingObject(raw)
		if value["name"] == name {
			return routingClone(t, value["snapshot"])
		}
	}
	t.Fatal("positive vector missing: " + name)
	return nil
}

func samePositiveJSON(t *testing.T, left, right any) bool {
	t.Helper()
	a, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(a, b)
}

func positiveSkillFixture(t *testing.T, missing string) string {
	t.Helper()
	contract := routingSkillFixture(t, true)
	root := filepath.Dir(filepath.Dir(contract))
	for role, stage := range map[string]string{"review": "review-queue", "fix": "fix-approved", "merge": "merge-shepherd", "tail": "tail-issues"} {
		path := filepath.Join(root, stage, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		marker := "<!-- pp:positive-review-v1-consumer stage=" + role + " -->"
		lines := []string{}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSuffix(line, "\r") != marker {
				lines = append(lines, line)
			}
		}
		data = []byte(strings.Join(lines, "\n"))
		if role != missing && missing != "all" {
			data = append(data, []byte("\n"+marker+"\n")...)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return contract
}

func TestPublicPositiveProofMatchesIndependentPythonCLIVectors(t *testing.T) {
	for _, raw := range positiveVectors(t) {
		fixture := routingObject(raw)
		t.Run(routingText(fixture["name"]), func(t *testing.T) {
			snapshot := routingObject(fixture["snapshot"])
			value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-routing-inspect", "42", "-routing-positive-purpose", routingText(fixture["stage"]))
			positive := routingObject(value["positive_review"])
			accepted := err == nil && positive != nil
			if accepted != fixture["accepted"] {
				t.Fatalf("Go/Python public proof acceptance differs: %v, %v", value, err)
			}
			if value["mutation_authorized"] != false || value["authority"] != "unverified-offline-input" {
				t.Fatal("offline proof acquired mutation authority")
			}
			if accepted && (!samePositiveJSON(t, positive["descriptor"], fixture["source"]) || positive["merge_sha"] != fixture["merge_sha"]) {
				t.Fatalf("positive source/node identities or merge boundary differ: %v", positive)
			}
		})
	}
}

func TestPublicPositiveQueueNeedsExplicitReadersAndTrustedShip(t *testing.T) {
	contract := positiveSkillFixture(t, "")
	for _, fault := range []string{"none", "no-ship", "foreign-ship", "hold", "needs-decision", "flag-off", "old-A-readers", "review", "fix", "merge", "tail"} {
		t.Run(fault, func(t *testing.T) {
			state := positiveSnapshot(t, "positive-open")
			switch fault {
			case "no-ship":
				state["labels"] = []any{}
			case "foreign-ship":
				for _, raw := range state["edges"].([]any) {
					node := routingObject(routingObject(raw)["node"])
					if node["__typename"] == "LabeledEvent" && routingObject(node["label"])["name"] == "ship" {
						node["actor"] = map[string]any{"login": "someone"}
					}
				}
			case "hold", "needs-decision":
				state["labels"] = append(state["labels"].([]any), fault)
			}
			selectedContract := contract
			if fault == "old-A-readers" {
				selectedContract = positiveSkillFixture(t, "all")
			} else if fault == "review" || fault == "fix" || fault == "merge" || fault == "tail" {
				selectedContract = positiveSkillFixture(t, fault)
			}
			args := []string{"-contract", selectedContract}
			if fault != "flag-off" {
				args = append(args, "-technical-handoff")
			}
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, args...)
			if err != nil {
				t.Fatal(err)
			}
			merges := value["merge_executable"].([]any)
			if fault == "none" {
				if len(merges) != 1 || routingObject(merges[0])["number"] != float64(42) || len(value["positive_merge_candidates"].([]any)) != 1 {
					t.Fatalf("qualified positive proof lost native merge election: %v", value)
				}
			} else if len(merges) != 0 {
				t.Fatalf("unsafe positive target was elected: %v", value)
			}
			if fault == "old-A-readers" || fault == "review" || fault == "fix" || fault == "merge" || fault == "tail" {
				if value["positive_review_contract"] != nil || value["fix_execution_contract"] == nil {
					t.Fatal("old/incomplete B readers advertised positive compatibility or broke the accepted A contract")
				}
			}
			if value["integration_owner"] != nil || len(value["content_review_candidates"].([]any)) != 0 || fault != "flag-off" && len(value["technical_fix_candidates"].([]any)) != 0 {
				t.Fatal("positive proof became another audit, FIX or integration owner")
			}
		})
	}
}
