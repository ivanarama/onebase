package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

func routingGolden(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, filename := range []string{"review-routing-history-v1.json", "review-routing-operational-history-v1.json", "review-routing-retained-audit-v1.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "reviewrouting", "testdata", filename))
		if err != nil {
			t.Fatal(err)
		}
		value, err := reviewrouting.Decode(data, 8<<20)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range value.(map[string]any)["cases"].([]any) {
			item := raw.(map[string]any)
			if item["name"] == name {
				return item["snapshot"].(map[string]any)
			}
		}
	}
	t.Fatal("shared routing case missing: " + name)
	return nil
}

func routingClone(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := reviewrouting.Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := reviewrouting.Decode(data, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	return copy.(map[string]any)
}

func routingPageData(t *testing.T, snapshot map[string]any, values []any, next bool, cursor string) map[string]any {
	t.Helper()
	identity := routingObject(snapshot["identity"])
	return map[string]any{"repository": map[string]any{"id": identity["base_repository_id"], "nameWithOwner": identity["base_repository"],
		"pullRequest": map[string]any{"id": identity["pr_id"], "number": identity["pr_number"],
			"baseRepository": map[string]any{"id": identity["base_repository_id"], "nameWithOwner": identity["base_repository"]},
			"headRepository": map[string]any{"id": identity["head_repository_id"], "nameWithOwner": identity["head_repository"]},
			"headRefName":    identity["head_ref"], "isCrossRepository": identity["cross_repository"], "author": snapshot["author"],
			"headRefOid": snapshot["headRefOid"], "baseRefOid": snapshot["baseRefOid"], "baseRefName": snapshot["baseRefName"],
			"state": snapshot["state"], "isDraft": snapshot["isDraft"], "labels": map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}},
			"timelineItems": map[string]any{"updatedAt": snapshot["updatedAt"], "edges": values, "pageInfo": map[string]any{"hasNextPage": next, "endCursor": cursor}}}}}
}

func TestRoutingSnapshotRequiresTwoIdenticalCompletePasses(t *testing.T) {
	snapshot := routingGolden(t, "third-typed-round")
	values := snapshot["edges"].([]any)
	first := routingPageData(t, snapshot, values[:4], true, "PAGE1")
	last := routingPageData(t, snapshot, values[4:], false, "PAGE2")
	client := &scriptedGraphQLClient{t: t, results: []any{first, last, first, last}}
	got, err := stableRoutingSnapshot(client, "owner/repo", "R_BASE", 42)
	if err != nil {
		t.Fatal(err)
	}
	if !reviewrouting.Equal(got, snapshot) || len(client.calls) != 4 {
		t.Fatal("stable normalized timeline differs from Python shared snapshot")
	}
	for _, call := range client.calls {
		if !strings.HasPrefix(strings.TrimSpace(call.query), "query ") || strings.Contains(call.query, "mutation") {
			t.Fatal("routing checker attempted a mutation")
		}
	}
	verified, err := reviewrouting.VerifiedDepth(got, "owner")
	if err != nil || verified.Depth != 3 {
		t.Fatalf("public transport lost canonical history: %+v %v", verified, err)
	}
}

func TestRoutingSnapshotRefusesDriftPartialIdentityAndRepeatedHistory(t *testing.T) {
	snapshot := routingGolden(t, "third-typed-round")
	values := snapshot["edges"].([]any)
	for _, fault := range []string{"author", "head", "base", "fork", "label-completeness", "duplicate-edge", "between-passes"} {
		t.Run(fault, func(t *testing.T) {
			first := routingPageData(t, snapshot, values[:4], true, "PAGE1")
			last := routingPageData(t, snapshot, values[4:], false, "PAGE2")
			pr := routingObject(routingObject(last["repository"])["pullRequest"])
			switch fault {
			case "author":
				pr["author"] = map[string]any{"id": "CHANGED", "login": "other"}
			case "head":
				pr["headRefOid"] = strings.Repeat("b", 40)
			case "base":
				pr["baseRefOid"] = strings.Repeat("c", 40)
			case "fork":
				pr["headRepository"] = nil
			case "label-completeness":
				routingObject(pr["labels"])["pageInfo"] = map[string]any{"hasNextPage": nil}
			case "duplicate-edge":
				routingObject(pr["timelineItems"])["edges"] = values[:1]
			}
			responses := []any{first, last}
			if fault == "between-passes" {
				changed := routingClone(t, snapshot)
				changed["updatedAt"] = "2026-10-29T00:20:00Z"
				responses = []any{routingPageData(t, snapshot, values, false, "END"), routingPageData(t, changed, values, false, "END")}
			}
			client := &scriptedGraphQLClient{t: t, results: responses}
			if _, err := stableRoutingSnapshot(client, "owner/repo", "R_BASE", 42); err == nil {
				t.Fatal("partial/changed timeline accepted")
			}
		})
	}
}

func routingSkillFixture(t *testing.T, marked bool) string {
	t.Helper()
	root := fixtureSkillsRoot(t)
	if marked {
		for role, name := range map[string]string{"review": "review-queue", "fix": "fix-approved", "merge": "merge-shepherd", "tail": "tail-issues"} {
			path := filepath.Join(root, name, "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, []byte("\n<!-- pp:review-routing-v1-consumer stage="+role+" -->\n")...)
			if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // G703: fixed role names below fixtureSkillsRoot's t.TempDir.
				t.Fatal(err)
			}
		}
	}
	// Synthetic version markers qualify only this parser test. They do not
	// establish actual OneBase consumer acceptance or execution capability.
	return filepath.Join(root, "review-queue", "SKILL.md")
}

func routingPullFixture(t *testing.T, snapshot map[string]any, number int) map[string]any {
	t.Helper()
	comments := []any{}
	for _, raw := range snapshot["edges"].([]any) {
		node := routingObject(routingObject(raw)["node"])
		if node["__typename"] != "IssueComment" {
			continue
		}
		id, err := strconv.ParseInt(routingText(node["fullDatabaseId"]), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		comments = append(comments, map[string]any{"id": id, "created_at": node["createdAt"], "updated_at": node["createdAt"], "user": node["author"], "body": node["body"]})
	}
	labels := []any{}
	for _, name := range snapshot["labels"].([]any) {
		labels = append(labels, map[string]any{"name": name})
	}
	return map[string]any{"number": number, "title": "private routing fixture", "html_url": "https://github.com/owner/repo/pull/" + strconv.Itoa(number),
		"created_at": "2026-10-29T00:00:00Z", "updated_at": "2026-10-29T00:01:00Z", "state": "open", "draft": false,
		"head": map[string]any{"sha": snapshot["headRefOid"]}, "base": map[string]any{"ref": "main"},
		"labels": labels, "comments": comments, "routing_snapshot": snapshot}
}

func routingCLI(t *testing.T, pulls []any, args ...string) (map[string]any, error) {
	t.Helper()
	directory := t.TempDir()
	prs, issues := filepath.Join(directory, "prs.json"), filepath.Join(directory, "issues.json")
	data, err := json.Marshal(pulls)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prs, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issues, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: fixed public test CLI, constant test flags and private t.TempDir paths; no shell or external input.
	command := exec.Command("go", append([]string{"run", ".", "-json", "-repo", "owner/repo", "-owner", "owner", "-prs", prs, "-issues", issues}, args...)...)
	preparePipelineGraphQLCommand(command)
	command.Env = append(os.Environ(), "GH_EXE="+filepath.Join(directory, "no-live-github"))
	output, runErr := command.Output()
	var value map[string]any
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("public CLI JSON failed: %v; command %v; output %s", err, runErr, output)
	}
	return value, runErr
}

func TestPublicRoutingInspectionDoesNotElectOrMutate(t *testing.T) {
	for _, test := range []struct {
		name     string
		accepted bool
		depth    float64
	}{{"third-typed-round", true, 3}, {"missing-claim", false, 0}, {"deletion", false, 0}} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := routingGolden(t, test.name)
			value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-routing-inspect", "42")
			if (err == nil) != test.accepted {
				t.Fatalf("public acceptance differs: %v, %v", err, value["error"])
			}
			if value["mutation_authorized"] != false || value["authority"] != "unverified-offline-input" || value["purpose"] != "read-only-routing-inspection" {
				t.Fatal("offline data acquired authority")
			}
			if test.accepted && value["verified_depth"] != test.depth {
				t.Fatal("public depth differs from shared oracle")
			}
			if _, ok := value["review_candidates"]; ok {
				t.Fatal("inspection ran queue election")
			}
		})
	}
}

func TestPublicBackdatedLaterCommitKeepsHistoricalLegacyDepth(t *testing.T) {
	snapshot := routingGolden(t, "two-legacy-rounds")
	values := snapshot["edges"].([]any)
	extra := map[string]any{"cursor": "LATE_OLD_DATE", "node": map[string]any{
		"__typename": "PullRequestCommit", "id": "LATE_OLD_DATE", "commit": map[string]any{"oid": strings.Repeat("9", 40)}}}
	withCommit := append([]any{}, values[:2]...)
	withCommit = append(withCommit, extra)
	snapshot["edges"] = append(withCommit, values[2:]...)
	value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-routing-inspect", "42")
	if err != nil || value["verified_depth"] != float64(2) {
		t.Fatalf("backdated commit erased historical transactions: %v %v", value, err)
	}
	queue, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-technical-handoff", "-contract", routingSkillFixture(t, true))
	if err != nil || len(queue["content_review_candidates"].([]any)) != 1 || len(queue["engineering_waiting"].([]any)) != 0 {
		t.Fatalf("backdated historical commit parked the current review: %v %v", queue, err)
	}
}

func TestPublicRetainedTypedAuditSurvivesLaterMetadataAndCommitDates(t *testing.T) {
	for _, name := range []string{"retained-clean", "retained-changed-labels", "retained-changed-state", "retained-changed-isDraft", "retained-backdated-before-audit", "retained-backdated-during-publication"} {
		t.Run(name, func(t *testing.T) {
			snapshot := routingGolden(t, name)
			value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-routing-inspect", "42")
			if err != nil || value["verified_depth"] != float64(3) || value["mutation_authorized"] != false {
				t.Fatalf("retained historical audit lost its immutable completion: %v %v", value, err)
			}
		})
	}
}

func TestPublicTechnicalSourceNeverBecomesLegacyMergeOrIntegrationOwner(t *testing.T) {
	contract := routingSkillFixture(t, true)
	for _, name := range []string{"third-typed-round", "missing-claim", "typed-invalid-epoch"} {
		t.Run(name, func(t *testing.T) {
			snapshot := routingGolden(t, name)
			value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-contract", contract, "-technical-handoff")
			if err != nil {
				t.Fatalf("diagnostic queue failed: %v", err)
			}
			if len(value["merge_candidates"].([]any)) != 0 || len(value["merge_executable"].([]any)) != 0 || value["integration_owner"] != nil {
				t.Fatal("typed technical/invalid source acquired MERGE or single-flight ownership")
			}
			fixes := value["technical_fix_candidates"].([]any)
			if (len(fixes) == 1) != (name == "third-typed-round") {
				t.Fatalf("canonical source allowlist differs: %+v", fixes)
			}
			if len(fixes) == 1 {
				candidate := fixes[0].(map[string]any)
				if len(candidate) != 4 || candidate["stage"] != "fix-pr" || candidate["number"] != float64(42) || candidate["head"] != snapshot["headRefOid"] || len(candidate["source"].(map[string]any)) != 7 {
					t.Fatal("FIX candidate lost its exact closed source binding")
				}
			}
			if value["fix_execution_contract"] != nil {
				t.Fatal("preparatory checker claimed complete native execution acceptance")
			}
		})
	}
}

func TestPublicMissingReaderNeverAdmitsTechnicalSource(t *testing.T) {
	snapshot := routingGolden(t, "third-typed-round")
	value, err := routingCLI(t, []any{routingPullFixture(t, snapshot, 42)}, "-contract", routingSkillFixture(t, false), "-technical-handoff")
	if err != nil {
		t.Fatal(err)
	}
	if value["review_routing_contract"] != nil || len(value["technical_fix_candidates"].([]any)) != 0 || len(value["engineering_waiting"].([]any)) != 1 {
		t.Fatal("missing paired reader version was admitted")
	}
}

func TestPublicRoutingFiniteBudgetAndPostPush(t *testing.T) {
	contract := routingSkillFixture(t, true)
	for _, test := range []struct {
		name                                        string
		fix, review, settlement, engineering, human int
	}{
		{"fresh-technical-source", 1, 0, 0, 0, 0}, {"pipeline-start-spent-once", 1, 0, 0, 0, 0},
		{"changed-head-with-unsettled-pipeline", 0, 0, 1, 1, 0}, {"one-fix-done-needs-review", 0, 1, 0, 0, 0},
		{"second-technical-source-budget-one", 1, 0, 0, 0, 0}, {"two-attempts-engineering-parking", 0, 0, 0, 1, 0},
		{"settled-pipeline-budget-one", 0, 0, 0, 1, 0}, {"one-resume-with-spent-budget", 0, 1, 0, 0, 0},
		{"resumed-review-clears-park-without-refund", 1, 0, 0, 0, 0}, {"consumed-resume-cannot-be-reused", 0, 0, 0, 1, 0},
		{"explicit-report-case-1", 0, 0, 0, 0, 1}, {"explicit-report-case-2", 0, 0, 0, 0, 1}, {"explicit-report-case-3", 0, 0, 0, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := routingGolden(t, test.name)
			value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-contract", contract, "-technical-handoff")
			if err != nil {
				t.Fatalf("public routing failed: %v", err)
			}
			for field, want := range map[string]int{"technical_fix_candidates": test.fix, "content_review_candidates": test.review,
				"technical_fix_settlement_candidates": test.settlement, "engineering_waiting": test.engineering, "human_waiting": test.human} {
				if got := len(value[field].([]any)); got != want {
					t.Fatalf("%s=%d, want%d; findings=%v", field, got, want, value["findings"])
				}
			}
			if value["integration_owner"] != nil || len(value["merge_executable"].([]any)) != 0 {
				t.Fatal("technical/engineering history captured the integration lane")
			}
		})
	}
}

func TestPublicEngineeringTargetDoesNotBlockAnotherPR(t *testing.T) {
	contract := routingSkillFixture(t, true)
	plain := routingGolden(t, "two-legacy-rounds")
	values := plain["edges"].([]any)
	plain["edges"] = []any{values[len(values)-1]}
	identity := routingObject(plain["identity"])
	identity["pr_id"], identity["pr_number"] = "PR_PLAIN_99", json.Number("99")
	digest, err := reviewrouting.IdentityDigest(identity)
	if err != nil {
		t.Fatal(err)
	}
	plain["identity_sha256"] = digest
	key := routingObject(plain["pr_key"])
	key["pr_id"], key["pr_number"] = identity["pr_id"], identity["pr_number"]
	for _, name := range []string{"missing-claim", "two-attempts-engineering-parking", "changed-head-with-unsettled-pipeline"} {
		t.Run(name, func(t *testing.T) {
			bad := routingGolden(t, name)
			value, err := routingCLI(t, []any{routingPullFixture(t, bad, 42), routingPullFixture(t, plain, 99)}, "-contract", contract, "-technical-handoff")
			if err != nil {
				t.Fatal(err)
			}
			queue := value["review_candidates"].([]any)
			if len(queue) != 1 || queue[0].(map[string]any)["number"] != float64(99) || value["integration_owner"] != nil {
				t.Fatalf("one parked PR blocked independent work: %v", queue)
			}
		})
	}
}

func TestPublicReducedLimitNeverRefundsExistingAttempt(t *testing.T) {
	state := routingGolden(t, "second-technical-source-budget-one")
	value, err := routingCLI(t, []any{routingPullFixture(t, state, 42)}, "-contract", routingSkillFixture(t, true), "-technical-handoff", "-routing-max-attempts", "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(value["technical_fix_candidates"].([]any)) != 0 || len(value["engineering_waiting"].([]any)) != 1 {
		t.Fatal("lower current limit silently reset a spent slot")
	}
}
