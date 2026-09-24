package pipelinecontract

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type trustedContributorWorkflowTrigger struct {
	Types []string `yaml:"types"`
}

type trustedContributorWorkflowStep struct {
	Uses  string            `yaml:"uses"`
	Env   map[string]string `yaml:"env"`
	Shell string            `yaml:"shell"`
	Run   string            `yaml:"run"`
}

type trustedContributorWorkflowJob struct {
	If             string                           `yaml:"if"`
	RunsOn         string                           `yaml:"runs-on"`
	TimeoutMinutes int                              `yaml:"timeout-minutes"`
	Permissions    map[string]string                `yaml:"permissions"`
	Steps          []trustedContributorWorkflowStep `yaml:"steps"`
}

type trustedContributorWorkflow struct {
	On          map[string]trustedContributorWorkflowTrigger `yaml:"on"`
	Permissions map[string]string                            `yaml:"permissions"`
	Jobs        map[string]trustedContributorWorkflowJob     `yaml:"jobs"`
}

func TestTrustedContributorPriorityWorkflowIsNarrowAndTextIndependent(t *testing.T) {
	raw := repositoryFile(t, ".github", "workflows", "trusted-contributor-priority.yml")
	var workflow trustedContributorWorkflow
	if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
		t.Fatal(err)
	}

	if len(workflow.On) != 1 {
		t.Fatalf("workflow triggers = %#v, want only issues", workflow.On)
	}
	issues, ok := workflow.On["issues"]
	if !ok || len(issues.Types) != 1 || issues.Types[0] != "opened" {
		t.Fatalf("issues trigger = %#v, want only issues.opened", issues)
	}
	if len(workflow.Permissions) != 2 || workflow.Permissions["contents"] != "read" ||
		workflow.Permissions["issues"] != "write" {
		t.Fatalf("workflow permissions = %#v, want only contents: read and issues: write", workflow.Permissions)
	}
	if len(workflow.Jobs) != 1 {
		t.Fatalf("workflow jobs = %#v, want one narrow mutation job", workflow.Jobs)
	}
	job, ok := workflow.Jobs["prioritize"]
	if !ok {
		t.Fatal("workflow must define the prioritize job")
	}
	if strings.TrimSpace(job.If) != "github.repository == 'ivanarama/onebase'" {
		t.Fatalf("workflow repository gate = %q, want only the exact upstream repository", job.If)
	}
	if job.RunsOn != "ubuntu-latest" || job.TimeoutMinutes != 2 {
		t.Fatalf("workflow runner budget = %q/%d, want ubuntu-latest/2 minutes", job.RunsOn, job.TimeoutMinutes)
	}
	if len(job.Permissions) != 0 {
		t.Fatalf("job must not widen top-level permissions: %#v", job.Permissions)
	}
	if len(job.Steps) != 1 {
		t.Fatalf("workflow steps = %d, want one REST mutation", len(job.Steps))
	}
	step := job.Steps[0]
	if step.Uses != "" {
		t.Fatalf("workflow must not execute a third-party action: %q", step.Uses)
	}
	if len(step.Env) != 3 || step.Env["GH_TOKEN"] != "${{ github.token }}" ||
		step.Env["AUTHOR_ID"] != "${{ github.event.issue.user.id }}" ||
		step.Env["ISSUE_NUMBER"] != "${{ github.event.issue.number }}" {
		t.Fatalf("workflow environment = %#v, want only token, immutable author id and numeric issue id", step.Env)
	}
	if step.Shell != "bash" {
		t.Fatalf("workflow shell = %q, want bash", step.Shell)
	}
	const expectedScript = `set -euo pipefail
if [[ ! "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]; then
  exit 1
fi
if [[ ! "$AUTHOR_ID" =~ ^[1-9][0-9]*$ ]]; then
  exit 1
fi
if [[ ! "$ISSUE_NUMBER" =~ ^[1-9][0-9]*$ ]]; then
  exit 1
fi

config_file="$(mktemp)"
trap 'rm -f "$config_file"' EXIT
gh api --method GET \
  -H 'Accept: application/vnd.github.raw+json' \
  "repos/ivanarama/onebase/contents/.github/trusted-priority-contributors.json?ref=${GITHUB_SHA}" \
  >"$config_file"

jq -e -s '
  def positive_decimal:
    if type != "string" then false
    else
      explode as $chars
      | ($chars | length) > 0
        and ($chars[0] >= 49 and $chars[0] <= 57)
        and all($chars[]; . >= 48 and . <= 57)
    end;

  length == 1
  and (.[0] |
    type == "object"
    and (keys == ["contributors", "version"])
    and (.version | type == "number")
    and (.version == 1)
    and (.contributors | type == "array")
    and all(.contributors[];
      type == "object"
      and (keys == ["github_id", "login_hint"])
      and (.github_id | positive_decimal)
      and (.login_hint | type == "string" and length > 0)
    )
    and (
      ([.contributors[].github_id] | length)
      == ([.contributors[].github_id] | unique | length)
    )
  )
' "$config_file" >/dev/null

if ! jq -e -s --arg author_id "$AUTHOR_ID" \
  'length == 1 and (.[0].contributors | any(.github_id == $author_id))' \
  "$config_file" >/dev/null; then
  exit 0
fi

gh api --method POST \
  "repos/ivanarama/onebase/issues/${ISSUE_NUMBER}/labels" \
  --input - <<'JSON'
{"labels":["queue:p0"]}
JSON
`
	if step.Run != expectedScript {
		t.Fatalf("workflow script must remain the single exact label mutation\ngot:\n%s\nwant:\n%s", step.Run, expectedScript)
	}
	if strings.Count(step.Run, "queue:p0") != 1 {
		t.Fatalf("queue:p0 must be the one literal label payload, got %d occurrences", strings.Count(step.Run, "queue:p0"))
	}
	if strings.Count(step.Run, "gh api --method GET") != 1 || strings.Count(step.Run, "gh api --method POST") != 1 {
		t.Fatal("workflow must perform exactly one immutable config read and one conditional label mutation")
	}
	remainingEventContext := strings.Replace(raw, "github.event.issue.user.id", "", 1)
	remainingEventContext = strings.Replace(remainingEventContext, "github.event.issue.number", "", 1)
	if strings.Contains(remainingEventContext, "github.event") {
		t.Fatal("workflow must not consume any event data beyond immutable author id and numeric issue number")
	}
	rejectAll(t, raw,
		"330018641",
		"AnnaSIceberg",
		"pull_request_target",
		"workflow_dispatch",
		"github.event.issue.title",
		"github.event.issue.body",
		"github.event.comment",
		"actions/checkout",
		"approved",
		"ready-fix",
		"reviewed",
		"ship",
		"changes-requested",
		"needs-decision",
		"hold",
		"manual",
		"in-work",
		"plan-needed",
		"plan-in-review",
	)
	docs := repositoryFile(t, "docs", "maintenance-pipeline.md")
	requireAllCompact(t, docs,
		"Список хранится в `.github/trusted-priority-contributors.json` и меняется обычным PR",
		"Корневой `version: 1` фиксирует версию схемы",
		"Логин служит только подсказкой при чтении файла: workflow никогда не авторизует по нему",
		"Добавьте отдельный объект в массив `contributors`, не повторяя `github_id`",
		"ровно с default-branch SHA события (`GITHUB_SHA`)",
		"неизвестной версии или неоднозначный список завершает job ошибкой до постановки метки (fail closed)",
		"отсутствие автора в валидном списке — штатный успешный выход без метки",
		"Текст, заголовок и комментарии issue не читаются",
	)
}

type modeledTrustedPriorityContributor struct {
	githubID  string
	loginHint string
}

func positiveDecimal(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func modeledTrustedPriorityConfig(data []byte) ([]modeledTrustedPriorityContributor, error) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	root, ok := decoded.(map[string]any)
	if !ok || len(root) != 2 {
		return nil, fmt.Errorf("root must contain exactly version and contributors")
	}
	version, ok := root["version"].(float64)
	if !ok || version != 1 {
		return nil, fmt.Errorf("unsupported config version")
	}
	rawContributors, ok := root["contributors"].([]any)
	if !ok {
		return nil, fmt.Errorf("contributors must be an array")
	}

	contributors := make([]modeledTrustedPriorityContributor, 0, len(rawContributors))
	seen := make(map[string]bool, len(rawContributors))
	for _, rawContributor := range rawContributors {
		object, ok := rawContributor.(map[string]any)
		if !ok || len(object) != 2 {
			return nil, fmt.Errorf("contributor must contain exactly github_id and login_hint")
		}
		githubID, idOK := object["github_id"].(string)
		loginHint, loginOK := object["login_hint"].(string)
		if !idOK || !loginOK || !positiveDecimal(githubID) || loginHint == "" {
			return nil, fmt.Errorf("contributor fields have invalid types or values")
		}
		if seen[githubID] {
			return nil, fmt.Errorf("duplicate github_id %s", githubID)
		}
		seen[githubID] = true
		contributors = append(contributors, modeledTrustedPriorityContributor{
			githubID:  githubID,
			loginHint: loginHint,
		})
	}
	return contributors, nil
}

func modeledTrustedPriorityAuthorized(data []byte, authorID string) (bool, error) {
	if !positiveDecimal(authorID) {
		return false, fmt.Errorf("author id must be a positive decimal string")
	}
	contributors, err := modeledTrustedPriorityConfig(data)
	if err != nil {
		return false, err
	}
	for _, contributor := range contributors {
		if contributor.githubID == authorID {
			return true, nil
		}
	}
	return false, nil
}

func TestTrustedPriorityConfigSupportsManyIDsAndNeverAuthorizesByLogin(t *testing.T) {
	configured := []byte(`{
		"version": 1,
		"contributors": [
			{"github_id":"101", "login_hint":"FirstUser"},
			{"github_id":"202", "login_hint":"SameLoginAsEvent"}
		]
	}`)
	contributors, err := modeledTrustedPriorityConfig(configured)
	if err != nil {
		t.Fatal(err)
	}
	if len(contributors) != 2 {
		t.Fatalf("contributors = %d, want two independently configured users", len(contributors))
	}
	authorized, err := modeledTrustedPriorityAuthorized(configured, "202")
	if err != nil || !authorized {
		t.Fatalf("second configured immutable id must authorize: authorized=%v err=%v", authorized, err)
	}
	authorized, err = modeledTrustedPriorityAuthorized(configured, "303")
	if err != nil || authorized {
		t.Fatalf("an unlisted id must not authorize even when a login hint could match: authorized=%v err=%v", authorized, err)
	}

	renamed := []byte(`{"version":1,"contributors":[{"github_id":"202","login_hint":"CompletelyDifferentLogin"}]}`)
	authorized, err = modeledTrustedPriorityAuthorized(renamed, "202")
	if err != nil || !authorized {
		t.Fatalf("login_hint must not affect authorization: authorized=%v err=%v", authorized, err)
	}
}

func TestTrustedPriorityConfigFailsClosedOnMalformedOrDuplicateEntries(t *testing.T) {
	testCases := map[string]string{
		"malformed json":        `{`,
		"wrong root type":       `[]`,
		"missing version":       `{"contributors":[]}`,
		"wrong version":         `{"version":2,"contributors":[]}`,
		"string version":        `{"version":"1","contributors":[]}`,
		"extra root key":        `{"version":1,"contributors":[],"other":true}`,
		"contributors null":     `{"version":1,"contributors":null}`,
		"numeric id":            `{"version":1,"contributors":[{"github_id":101,"login_hint":"User"}]}`,
		"zero id":               `{"version":1,"contributors":[{"github_id":"0","login_hint":"User"}]}`,
		"non-decimal id":        `{"version":1,"contributors":[{"github_id":"10x","login_hint":"User"}]}`,
		"empty login hint":      `{"version":1,"contributors":[{"github_id":"101","login_hint":""}]}`,
		"missing login hint":    `{"version":1,"contributors":[{"github_id":"101"}]}`,
		"extra contributor key": `{"version":1,"contributors":[{"github_id":"101","login_hint":"User","trusted":true}]}`,
		"duplicate id":          `{"version":1,"contributors":[{"github_id":"101","login_hint":"First"},{"github_id":"101","login_hint":"Second"}]}`,
	}
	for name, raw := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := modeledTrustedPriorityConfig([]byte(raw)); err == nil {
				t.Fatal("invalid trusted contributor config must fail closed")
			}
		})
	}

	actual := []byte(repositoryFile(t, ".github", "trusted-priority-contributors.json"))
	contributors, err := modeledTrustedPriorityConfig(actual)
	if err != nil {
		t.Fatalf("repository trusted contributor config is invalid: %v", err)
	}
	if len(contributors) == 0 {
		t.Fatal("repository trusted contributor config must contain the confirmed contributor")
	}
}

func TestTrustedPriorityWorkflowScriptFailsClosedOnAmbiguousJSON(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable; the exact workflow harness runs on ubuntu CI")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is unavailable; the exact workflow harness runs on ubuntu CI")
	}

	raw := repositoryFile(t, ".github", "workflows", "trusted-contributor-priority.yml")
	var workflow trustedContributorWorkflow
	if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
		t.Fatal(err)
	}
	job, ok := workflow.Jobs["prioritize"]
	if !ok || len(job.Steps) != 1 {
		t.Fatalf("prioritize workflow must contain exactly one step: %#v", job)
	}
	script := job.Steps[0].Run

	testCases := []struct {
		name     string
		config   string
		wantExit bool
		wantPost bool
	}{
		{
			name:     "one valid document author listed",
			config:   `{"version":1,"contributors":[{"github_id":"101","login_hint":"User"}]}`,
			wantExit: true,
			wantPost: true,
		},
		{
			name:     "one valid document author absent",
			config:   `{"version":1,"contributors":[{"github_id":"202","login_hint":"Other"}]}`,
			wantExit: true,
		},
		{
			name:   "two valid root documents",
			config: "{\"version\":1,\"contributors\":[]}\n{\"version\":1,\"contributors\":[{\"github_id\":\"101\",\"login_hint\":\"User\"}]}",
		},
		{
			name:   "empty object before valid document",
			config: "{}\n{\"version\":1,\"contributors\":[{\"github_id\":\"101\",\"login_hint\":\"User\"}]}",
		},
		{
			name:   "null before valid document",
			config: "null\n{\"version\":1,\"contributors\":[{\"github_id\":\"101\",\"login_hint\":\"User\"}]}",
		},
		{
			name:   "newline tainted id before valid author",
			config: "{\"version\":1,\"contributors\":[{\"github_id\":\"999\\n\",\"login_hint\":\"Invalid\"},{\"github_id\":\"101\",\"login_hint\":\"User\"}]}",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tempDir := t.TempDir()
			binDir := filepath.Join(tempDir, "bin")
			if err := os.Mkdir(binDir, 0o700); err != nil {
				t.Fatal(err)
			}
			postLog := filepath.Join(tempDir, "post.log")
			postBody := filepath.Join(tempDir, "post-body.json")
			stub := `#!/usr/bin/env bash
set -euo pipefail
case " $* " in
  *" --method GET "*)
    printf '%s' "$STUB_CONFIG"
    ;;
  *" --method POST "*)
    cat >"$STUB_POST_BODY"
    printf '%s\n' "$*" >>"$STUB_POST_LOG"
    ;;
  *)
    printf 'unexpected gh invocation: %s\n' "$*" >&2
    exit 90
    ;;
esac
`
			if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(stub), 0o700); err != nil {
				t.Fatal(err)
			}

			// bash is a test-environment executable, while script is the exact
			// repository-owned workflow body and gh is replaced by the local stub.
			cmd := exec.Command(bash, "-c", script) //nolint:gosec // G204: intentional execution of the production workflow in an isolated harness.
			cmd.Dir = tempDir
			cmd.Env = workflowHarnessEnv(map[string]string{
				"PATH":           binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"GH_TOKEN":       "test-token",
				"GITHUB_SHA":     strings.Repeat("a", 40),
				"AUTHOR_ID":      "101",
				"ISSUE_NUMBER":   "17",
				"STUB_CONFIG":    testCase.config,
				"STUB_POST_LOG":  postLog,
				"STUB_POST_BODY": postBody,
			})
			output, err := cmd.CombinedOutput()
			if testCase.wantExit && err != nil {
				t.Fatalf("workflow exited with error: %v\n%s", err, output)
			}
			if !testCase.wantExit && err == nil {
				t.Fatalf("invalid config must fail closed\n%s", output)
			}

			log, readErr := os.ReadFile(postLog)
			posted := readErr == nil && len(log) > 0
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if posted != testCase.wantPost {
				t.Fatalf("label POST = %v, want %v; workflow error=%v\n%s", posted, testCase.wantPost, err, output)
			}
			if testCase.wantPost {
				body, err := os.ReadFile(postBody)
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(body)) != `{"labels":["queue:p0"]}` {
					t.Fatalf("label body = %q, want only literal queue:p0", body)
				}
			}
		})
	}
}

func workflowHarnessEnv(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, overridden := overrides[name]; !overridden {
			env = append(env, entry)
		}
	}
	for name, value := range overrides {
		env = append(env, name+"="+value)
	}
	return env
}

type modeledTriagePriorityItem struct {
	number    int
	createdAt time.Time
	recovery  bool
	labels    map[string]bool
}

func modeledTriageEffectivePriority(item modeledTriagePriorityItem, now time.Time) int {
	priority := 2
	found := false
	for value := 0; value <= 3; value++ {
		if item.labels["queue:p"+strconv.Itoa(value)] {
			priority = value
			found = true
			break
		}
	}
	if !found {
		for value := 0; value <= 3; value++ {
			if item.labels["queue:auto:p"+strconv.Itoa(value)] {
				priority = value
				found = true
				break
			}
		}
	}
	if !found {
		switch {
		case item.labels["security"], item.labels["severity:critical"], item.labels["blocker"], item.labels["data-loss"]:
			priority = 0
		case item.labels["bug"]:
			priority = 1
		case item.labels["enhancement"], item.labels["documentation"]:
			priority = 2
		case item.labels["question"]:
			priority = 3
		}
	}

	if priority > 1 && now.After(item.createdAt) {
		boost := int(now.Sub(item.createdAt) / (7 * 24 * time.Hour))
		if boost > priority-1 {
			boost = priority - 1
		}
		priority -= boost
	}
	return priority
}

func modeledTriagePriorityQueue(items []modeledTriagePriorityItem, now time.Time) []int {
	recovery := make([]modeledTriagePriorityItem, 0, len(items))
	ordinary := make([]modeledTriagePriorityItem, 0, len(items))
	for _, item := range items {
		if item.recovery {
			recovery = append(recovery, item)
		} else {
			ordinary = append(ordinary, item)
		}
	}
	oldestFirst := func(left, right modeledTriagePriorityItem) bool {
		if !left.createdAt.Equal(right.createdAt) {
			return left.createdAt.Before(right.createdAt)
		}
		return left.number < right.number
	}
	sort.Slice(recovery, func(i, j int) bool { return oldestFirst(recovery[i], recovery[j]) })
	sort.Slice(ordinary, func(i, j int) bool {
		leftPriority := modeledTriageEffectivePriority(ordinary[i], now)
		rightPriority := modeledTriageEffectivePriority(ordinary[j], now)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return oldestFirst(ordinary[i], ordinary[j])
	})

	ordered := append(recovery, ordinary...)
	if len(ordered) > 5 {
		ordered = ordered[:5]
	}
	numbers := make([]int, 0, len(ordered))
	for _, item := range ordered {
		numbers = append(numbers, item.number)
	}
	return numbers
}

func TestTriageRecoveryPrecedesPriorityAndNewIssuesUseStablePriorityOrder(t *testing.T) {
	triage := skill(t, "triage-issues")
	docs := repositoryFile(t, "docs", "maintenance-pipeline.md")
	requireAllCompact(t, triage,
		"Recovery — абсолютная первая очередь независимо от любых priority labels",
		"Ни одна новая issue, включая P0, не обходит исполнимую recovery-транзакцию",
		"(effective priority ASC, created_at ASC, number ASC)",
		"ручная `queue:p0`…`queue:p3` имеет приоритет над `queue:auto:p0`…`queue:auto:p3`",
		"применяй class labels в строгом порядке",
		"иначе `enhancement`/`documentation` → P2, иначе `question` → P3, иначе P2",
		"За каждые полные 168 часов с `created_at` уменьши числовой уровень на один, но не ниже P1",
		"P0 остаётся отдельной полосой срочной работы",
	)
	requireAllCompact(t, docs,
		"незавершённое recovery всегда идёт первым",
		"новые заявки сортируются по effective priority, времени создания и номеру",
		"она обгоняет старые новые заявки, но не незавершённую recovery-транзакцию",
	)
	adapter := repositoryFile(t, ".agents", "skills", "triage-issues", "SKILL.md")
	requireAllCompact(t, adapter, "Логика конвейера живёт только в канонической процедуре")
	rejectAll(t, adapter, "effective priority ASC", "queue:p0")

	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	at := func(daysAgo int) time.Time { return now.Add(-time.Duration(daysAgo) * 24 * time.Hour) }
	items := []modeledTriagePriorityItem{
		{number: 90, createdAt: at(1), recovery: true, labels: map[string]bool{"question": true}},
		{number: 61, createdAt: at(0), labels: map[string]bool{"queue:p0": true}},
		{number: 45, createdAt: at(20), labels: map[string]bool{}},
		{number: 80, createdAt: at(2), recovery: true, labels: map[string]bool{"queue:p3": true}},
		{number: 51, createdAt: at(1), labels: map[string]bool{"bug": true}},
		{number: 42, createdAt: at(0), labels: map[string]bool{"enhancement": true}},
		{number: 41, createdAt: at(0), labels: map[string]bool{"documentation": true}},
	}
	want := []int{80, 90, 61, 45, 51}
	got := modeledTriagePriorityQueue(items, now)
	if len(got) != len(want) {
		t.Fatalf("ordered queue length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ordered queue = %#v, want %#v", got, want)
		}
	}
	if tieOrder := modeledTriagePriorityQueue(items[5:], now); len(tieOrder) != 2 || tieOrder[0] != 41 || tieOrder[1] != 42 {
		t.Fatalf("same-priority same-time issues must use number as the final tie-breaker, got %#v", tieOrder)
	}

	manualWins := modeledTriagePriorityItem{
		createdAt: now,
		labels:    map[string]bool{"queue:p3": true, "queue:auto:p0": true},
	}
	if got := modeledTriageEffectivePriority(manualWins, now); got != 3 {
		t.Fatalf("manual priority must win over automatic priority, got P%d", got)
	}
	mixedClasses := modeledTriagePriorityItem{
		createdAt: now,
		labels:    map[string]bool{"enhancement": true, "question": true},
	}
	if got := modeledTriageEffectivePriority(mixedClasses, now); got != 2 {
		t.Fatalf("class priority must follow the shared precedence, got P%d for enhancement + question", got)
	}
	urgentOld := modeledTriagePriorityItem{createdAt: at(100), labels: map[string]bool{"queue:p0": true}}
	if got := modeledTriageEffectivePriority(urgentOld, now); got != 0 {
		t.Fatalf("P0 must remain outside aging, got P%d", got)
	}
}
