package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #2439: a fresh full content review and a new trusted current-HEAD ship must
// not be mistaken for carry of an unproved historical source-parent ship.
func TestFullCurrentStickyWithoutHistoricalShipPublicCLI(t *testing.T) {
	snapshot := fullCurrentReviewHistory(t)
	edges := snapshot["edges"].([]any)
	// No source ship exists. A previous current-HEAD revocation remains audit
	// history, while the final trusted transition is the new current-epoch label.
	edges = append(edges, map[string]any{"cursor": "C_OLD_UNSHIP", "node": map[string]any{
		"__typename": "UnlabeledEvent", "id": "OLD_UNSHIP", "createdAt": "2026-10-29T00:24:00Z",
		"actor": map[string]any{"login": "owner"}, "label": map[string]any{"name": "ship"}}})
	edges = append(edges, map[string]any{"cursor": "C_CURRENT_SHIP", "node": map[string]any{
		"__typename": "LabeledEvent", "id": "CURRENT_SHIP", "createdAt": "2026-10-29T00:25:00Z",
		"actor": map[string]any{"login": "owner"}, "label": map[string]any{"name": "ship"}}})
	snapshot["edges"] = edges
	got := fullCurrentReviewCLI(t, snapshot, 5)
	if len(got.MergeExecutable) != 1 || got.MergeExecutable[0].Stage != "merge" ||
		got.IntegrationOwner != nil || len(got.ReviewCandidates) != 0 || hasFinding(got, "legacy_source_review_missing") {
		t.Fatalf("full current sticky was sent back to historical legacy authorization: %+v", got)
	}
	// This is election evidence only; it does not execute MERGE or bypass its
	// independent current ship, complete Source, CI8, owner, budget and CAS gates.
}

func currentShipContractCLI(t *testing.T, skillsRoot string) report {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "pulls.json")
	if err := os.WriteFile(fixture, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // Owned executable, fixed flags and test-owned fixture paths.
	command := exec.Command(pipelinehealthCLI(t), "-transport", "rest", "-prs", fixture, "-contract",
		filepath.Join(skillsRoot, "review-queue", "SKILL.md"), "-json")
	command.Dir = repoRoot(t)
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); err != nil && (!ok || exit.ExitCode() != 1 || !json.Valid(output)) {
		t.Fatalf("public contract CLI failed: %v\n%s", err, output)
	}
	var got report
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode public contract report: %v\n%s", err, output)
	}
	return got
}

func TestCurrentStickyConsumerContractPublicCLI(t *testing.T) {
	// Both the active contract and a frozen contract missing one guarantee are
	// read by the actual public CLI, rather than invoking checkContract directly.
	got := currentShipContractCLI(t, fixtureSkillsRoot(t))
	if got.State != "green" || hasFinding(got, "unsafe_current_ship_contract") {
		t.Fatalf("current canonical contract refused: %+v", got)
	}
	for _, guarantee := range []string{
		"Полное содержательное REVIEW текущего HEAD не является legacy reauthorization",
		"исторический ship первого родителя не требуется",
		"Это правило не применяется к одному лишь интеграционному REVIEW",
	} {
		t.Run(guarantee, func(t *testing.T) {
			root := fixtureSkillsRoot(t)
			path := filepath.Join(root, "merge-shepherd", "references", "legacy-protocol.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			broken := strings.ReplaceAll(string(data), guarantee, "missing-current-ship-guarantee")
			if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
				t.Fatal(err)
			}
			got := currentShipContractCLI(t, root)
			if got.State != "red" || !hasFinding(got, "unsafe_current_ship_contract") {
				t.Fatalf("incompatible frozen MERGE consumer was accepted: %+v", got)
			}
		})
	}
}
