// pipelinehealth provides a fast, read-only operational check of the
// maintenance queue. It deliberately does not replace the mutation-time
// GraphQL proof in the pipeline skills.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

var (
	completionLine    = regexp.MustCompile(`(?m)^<!-- pp:head-reviewed ([0-9a-f]{40}) review-comment=([0-9]+) claim=([0-9]+) epoch-sha256=([0-9a-f]{64}) -->$`)
	claimLine         = regexp.MustCompile(`(?m)^<!-- pp:review-claim ([0-9a-f]{40}) review-comment=([0-9]+) epoch-sha256=([0-9a-f]{64}) -->$`)
	reviewedSHALine   = regexp.MustCompile(`(?m)^Reviewed-SHA: ([0-9a-f]{40})$`)
	reviewMarkerLine  = regexp.MustCompile(`(?m)^<!-- pp:review pp:tail=[0-9]+ -->$`)
	reviewAgain       = regexp.MustCompile(`(?m)^pp:review-again$`)
	displayRepair     = regexp.MustCompile(`(?m)^<!-- pp:display-repair comment=([0-9]+) -->$`)
	baseSyncIntent    = regexp.MustCompile(`(?m)^<!-- pp:base-sync-intent from=([0-9a-f]{40}) base=([0-9a-f]{40}) review-comment=([0-9]+) claim=([0-9]+) completion=([0-9]+) ship-event=([A-Za-z0-9_=-]+) previous=([0-9]+|none) -->$`)
	baseSyncDone      = regexp.MustCompile(`(?m)^<!-- pp:base-sync-done intent=([0-9]+) from=([0-9a-f]{40}) to=([0-9a-f]{40}) base=([0-9a-f]{40}) previous=([0-9]+|none) ship-event=([A-Za-z0-9_=-]+) -->$`)
	preReviewIntent   = regexp.MustCompile(`(?m)^<!-- pp:pre-review-sync-intent from=([0-9a-f]{40}) base=([0-9a-f]{40}) identity-sha256=([0-9a-f]{64}) -->$`)
	preReviewDone     = regexp.MustCompile(`(?m)^<!-- pp:pre-review-sync-done intent=([0-9]+) from=([0-9a-f]{40}) to=([0-9a-f]{40}) base=([0-9a-f]{40}) identity-sha256=([0-9a-f]{64}) -->$`)
	preReviewBlocked  = regexp.MustCompile(`(?m)^<!-- pp:pre-review-sync-recovery-blocked intent=([0-9]+) head=([0-9a-f]{40}) reason=(?:post-intent-event|push-denied) -->$`)
	preReviewResume   = regexp.MustCompile(`(?m)^<!-- pp:pre-review-sync-resume intent=([0-9]+) head=([0-9a-f]{40}) -->$`)
	triageRouteClaim  = regexp.MustCompile(`(?m)^<!-- pp:triage-route-claim fingerprint-sha256=([0-9a-f]{64}) owner=[0-9a-fA-F-]{36} -->$`)
	triageRouteRecord = regexp.MustCompile(`(?m)(^pp-triage-route-v1\nissue=([0-9]+)\nissue-updated=[^\n]+\ntitle-sha256=[0-9a-f]{64}\nbody-sha256=[0-9a-f]{64}\nanalysis-sha256=[0-9a-f]{64}\ncomments-sha256=[0-9a-f]{64}\nlabels-sha256=[0-9a-f]{64}\nevents-watermark=(?:[0-9]+|none)\nclass=(?:bug|enhancement|question|documentation)\nroute=(ready-fix|needs-decision)\nmanual=(?:true|false)\nreply=(required|none)\n)`)
	triageRouteLabels = regexp.MustCompile(`(?m)^<!-- pp:triage-route-labels claim=([0-9]+) fingerprint-sha256=([0-9a-f]{64}) .+ -->$`)
	triageAuthorReply = regexp.MustCompile(`(?m)^<!-- pp:triage-author-reply claim=([0-9]+) fingerprint-sha256=([0-9a-f]{64}) -->$`)
	triageRouteDone   = regexp.MustCompile(`(?m)^<!-- pp:triage-route-done claim=([0-9]+) fingerprint-sha256=([0-9a-f]{64}) -->$`)
)

type apiUser struct {
	Login string `json:"login"`
}

type apiLabel struct {
	Name string `json:"name"`
}

type apiComment struct {
	ID        int64   `json:"id"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	User      apiUser `json:"user"`
	Body      string  `json:"body"`
}

type apiPull struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	HTMLURL   string `json:"html_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	State     string `json:"state"`
	Draft     bool   `json:"draft"`
	Head      struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		OID string `json:"oid,omitempty"`
	} `json:"base"`
	MaintainerCanModify bool              `json:"maintainer_can_modify"`
	Mergeable           string            `json:"mergeable,omitempty"`
	MergeStateStatus    string            `json:"merge_state_status,omitempty"`
	AdmissionKnown      bool              `json:"admission_known,omitempty"`
	AdmissionError      string            `json:"admission_error,omitempty"`
	ChecksTruncated     bool              `json:"checks_truncated,omitempty"`
	CheckContexts       []apiCheckContext `json:"check_contexts,omitempty"`
	Labels              []apiLabel        `json:"labels"`
	Comments            []apiComment      `json:"-"`
	// HeadParents holds the parent SHAs of the head commit. An automatic
	// base-sync always leaves a merge commit; an ordinary FIX push leaves a
	// single-parent commit. Without this the integration lane cannot be told
	// apart from a normal review round.
	HeadParents []string `json:"head_parents,omitempty"`
}

type apiCheckContext struct {
	Name       string `json:"name"`
	Status     string `json:"status,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
}

type apiIssue struct {
	Number       int          `json:"number"`
	Title        string       `json:"title"`
	HTMLURL      string       `json:"html_url"`
	CreatedAt    string       `json:"created_at"`
	UpdatedAt    string       `json:"updated_at"`
	State        string       `json:"state"`
	PullRequest  any          `json:"pull_request"`
	CommentCount int          `json:"comments"`
	Labels       []apiLabel   `json:"labels"`
	Thread       []apiComment `json:"thread,omitempty"`
}

type candidate struct {
	Number                  int                      `json:"number"`
	Title                   string                   `json:"title"`
	URL                     string                   `json:"url"`
	Head                    string                   `json:"head"`
	Depth                   int                      `json:"review_depth"`
	Stage                   string                   `json:"stage"`
	Priority                int                      `json:"priority"`
	PrioritySource          string                   `json:"priority_source"`
	UpdatedAt               string                   `json:"updated_at"`
	PreReviewSyncValidation *preReviewSyncValidation `json:"pre_review_sync,omitempty"`
	IntegrationAt           string                   `json:"-"`
}

// preReviewSyncValidation is an immutable handoff descriptor, not proof by
// itself. REVIEW must bind these REST hints to two stable full GraphQL
// snapshots plus the exact commit parents, trailer and timestamps before it
// starts the ordinary full content audit.
type preReviewSyncValidation struct {
	IntentCommentID int64  `json:"intent_comment_id"`
	DoneCommentID   int64  `json:"done_comment_id"`
	From            string `json:"from"`
	To              string `json:"to"`
	Base            string `json:"base"`
	IdentitySHA256  string `json:"identity_sha256"`
	IntentCreatedAt string `json:"intent_created_at"`
	DoneCreatedAt   string `json:"done_created_at"`
}

type finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	PR       int    `json:"pr,omitempty"`
	Issue    int    `json:"issue,omitempty"`
	Message  string `json:"message"`
}

type report struct {
	State                   string      `json:"state"`
	Summary                 string      `json:"summary"`
	Scope                   string      `json:"scope"`
	Scheduler               string      `json:"scheduler"`
	Checked                 int         `json:"checked"`
	IssuesChecked           int         `json:"issues_checked"`
	ReviewCandidates        []candidate `json:"review_candidates"`
	ReviewBacklog           []candidate `json:"review_backlog"`
	ContentReviewCandidates []candidate `json:"content_review_candidates"`
	ReviewedWaitingShip     []candidate `json:"reviewed_waiting_ship"`
	IntegrationOwner        *candidate  `json:"integration_owner,omitempty"`
	MergeCandidates         []candidate `json:"merge_candidates"`
	MergeExecutable         []candidate `json:"merge_executable"`
	PlanCandidates          []candidate `json:"plan_candidates"`
	FixCandidates           []candidate `json:"fix_candidates"`
	PreReviewSyncCandidates []candidate `json:"pre_review_sync_candidates"`
	PreReviewWaitingCI      []candidate `json:"pre_review_waiting_ci"`
	HumanWaiting            []candidate `json:"human_waiting"`
	Findings                []finding   `json:"findings"`
}

func main() {
	repo := flag.String("repo", "ivanarama/onebase", "GitHub repository")
	owner := flag.String("owner", "ivanarama", "trusted pipeline account")
	contract := flag.String("contract", ".claude/skills/review-queue/SKILL.md", "active REVIEW contract")
	protection := flag.String("protection", ".github/branch-protection.json", "branch protection contract")
	fixture := flag.String("prs", "", "read a JSON fixture instead of GitHub")
	issueFixture := flag.String("issues", "", "read an issue JSON fixture instead of GitHub")
	asJSON := flag.Bool("json", false, "print machine-readable JSON")
	flag.Parse()

	prs, err := loadPulls(*repo, *fixture)
	if err != nil {
		fail(err)
	}
	issues, err := loadIssues(*repo, *issueFixture, *fixture != "")
	if err != nil {
		fail(err)
	}
	requiredChecks, err := loadRequiredChecks(*protection)
	if err != nil {
		fail(err)
	}
	result := analyzeWithRequiredChecks(prs, *owner, *repo, requiredChecks)
	analyzeIssues(&result, issues, prs, *owner)
	checkContract(&result, *contract)
	result.finish()

	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			fail(err)
		}
	} else {
		printReport(os.Stdout, result)
	}
	if result.State == "red" {
		os.Exit(1)
	}
}

func fail(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "pipelinehealth: %v\n", err)
	os.Exit(2)
}

func loadPulls(repo, fixture string) ([]apiPull, error) {
	if fixture != "" {
		data, err := os.ReadFile(fixture)
		if err != nil {
			return nil, err
		}
		var prs []apiPull
		if err := json.Unmarshal(data, &prs); err != nil {
			return nil, fmt.Errorf("decode fixture: %w", err)
		}
		return prs, nil
	}

	gh := os.Getenv("GH_EXE")
	if gh == "" {
		gh = "gh"
	}
	var prs []apiPull
	if err := ghJSONLines(gh, &prs, "api", "--paginate",
		"repos/"+repo+"/pulls?state=open&per_page=100&sort=created&direction=asc",
		"--jq", ".[]"); err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}

	// Comments are independent reads. A small pool keeps an interactive health
	// refresh fast while avoiding a burst of one request per PR.
	jobs := make(chan int)
	errs := make(chan error, len(prs))
	var wg sync.WaitGroup
	workers := 6
	if len(prs) < workers {
		workers = len(prs)
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				path := fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", repo, prs[index].Number)
				if err := ghJSONLines(gh, &prs[index].Comments, "api", "--paginate", path, "--jq", ".[]"); err != nil {
					errs <- fmt.Errorf("comments for PR #%d: %w", prs[index].Number, err)
					continue
				}
				if !needsHeadParents(prs[index]) {
					continue
				}
				var parents []struct {
					SHA string `json:"sha"`
				}
				commit := fmt.Sprintf("repos/%s/commits/%s", repo, prs[index].Head.SHA)
				if err := ghJSONLines(gh, &parents, "api", commit, "--jq", ".parents[]"); err != nil {
					errs <- fmt.Errorf("head parents for PR #%d: %w", prs[index].Number, err)
					continue
				}
				for _, parent := range parents {
					prs[index].HeadParents = append(prs[index].HeadParents, parent.SHA)
				}
			}
		}()
	}
	for index := range prs {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, err
		}
	}
	if err := loadPullAdmission(gh, repo, prs); err != nil {
		return nil, err
	}
	return prs, nil
}

type graphQLAdmissionResponse struct {
	Data struct {
		Repository map[string]json.RawMessage `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type graphQLAdmissionPull struct {
	Number           int    `json:"number"`
	HeadRefOID       string `json:"headRefOid"`
	BaseRefOID       string `json:"baseRefOid"`
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"mergeStateStatus"`
	Commits          struct {
		Nodes []struct {
			Commit struct {
				OID               string `json:"oid"`
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []struct {
							TypeName   string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							Context    string `json:"context"`
							State      string `json:"state"`
						} `json:"nodes"`
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// loadPullAdmission batches the two facts needed to break the conflict/CI
// deadlock. The REST pull list already supplies the immutable head identity;
// GraphQL adds mergeability and the exact-head status rollup without one API
// request per PR. The data is only an election hint: the FIX substage repeats
// the full identity, timeline, ref and CAS gates before mutating a branch.
func loadPullAdmission(gh, repo string, prs []apiPull) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("invalid repository %q", repo)
	}
	indexes := make([]int, 0, len(prs))
	for index := range prs {
		labels := labelSet(prs[index].Labels)
		if prs[index].State == "open" && prs[index].Base.Ref == "main" && !prs[index].Draft &&
			!labels["hold"] && !labels["needs-decision"] && prs[index].Head.SHA != "" {
			indexes = append(indexes, index)
		}
	}
	const batchSize = 20
	for start := 0; start < len(indexes); start += batchSize {
		end := start + batchSize
		if end > len(indexes) {
			end = len(indexes)
		}
		batch := indexes[start:end]
		var selection strings.Builder
		aliases := make(map[string]int, len(batch))
		for offset, index := range batch {
			alias := fmt.Sprintf("pr%d", offset)
			aliases[alias] = index
			_, _ = fmt.Fprintf(&selection, `%s:pullRequest(number:%d){number headRefOid baseRefOid mergeable mergeStateStatus commits(last:1){nodes{commit{oid statusCheckRollup{contexts(first:100){nodes{__typename ... on CheckRun{name status conclusion} ... on StatusContext{context state}} pageInfo{hasNextPage}}}}}}}`,
				alias, prs[index].Number)
		}
		query := `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){` + selection.String() + `}}`
		//nolint:gosec // The executable is trusted configuration and all arguments bypass a shell.
		cmd := exec.Command(gh, "api", "graphql", "-f", "query="+query, "-F", "owner="+owner, "-F", "name="+name)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("read pull admission batch: %w: %s", err, strings.TrimSpace(string(output)))
		}
		var response graphQLAdmissionResponse
		if err := json.Unmarshal(output, &response); err != nil {
			return fmt.Errorf("decode pull admission batch: %w", err)
		}
		if len(response.Errors) > 0 {
			messages := make([]string, 0, len(response.Errors))
			for _, item := range response.Errors {
				messages = append(messages, item.Message)
			}
			return fmt.Errorf("pull admission GraphQL: %s", strings.Join(messages, "; "))
		}
		if response.Data.Repository == nil {
			return fmt.Errorf("pull admission GraphQL returned no repository")
		}
		applyPullAdmissionBatch(prs, aliases, response.Data.Repository)
	}
	return nil
}

// applyPullAdmissionBatch quarantines a PR whose head changed or disappeared
// between the paginated REST snapshot and this GraphQL batch. That expected
// concurrent activity must not turn one contributor PR into a global outage of
// every pipeline lane; the next health refresh will read it again from scratch.
func applyPullAdmissionBatch(prs []apiPull, aliases map[string]int, repository map[string]json.RawMessage) {
	for alias, index := range aliases {
		raw, exists := repository[alias]
		if !exists || string(raw) == "null" {
			prs[index].AdmissionError = fmt.Sprintf("GraphQL snapshot no longer contains PR #%d", prs[index].Number)
			continue
		}
		var observed graphQLAdmissionPull
		if err := json.Unmarshal(raw, &observed); err != nil {
			prs[index].AdmissionError = fmt.Sprintf("decode GraphQL admission: %v", err)
			continue
		}
		if observed.Number != prs[index].Number || observed.HeadRefOID != prs[index].Head.SHA {
			prs[index].AdmissionError = fmt.Sprintf("head changed during snapshot: REST=%s GraphQL=%s",
				prs[index].Head.SHA, observed.HeadRefOID)
			continue
		}
		if len(observed.Commits.Nodes) == 0 || observed.Commits.Nodes[0].Commit.OID != observed.HeadRefOID {
			prs[index].AdmissionError = "status rollup is not bound to exact head"
			continue
		}
		prs[index].AdmissionKnown = true
		prs[index].Mergeable = observed.Mergeable
		prs[index].MergeStateStatus = observed.MergeStateStatus
		prs[index].Base.OID = observed.BaseRefOID
		rollup := observed.Commits.Nodes[0].Commit.StatusCheckRollup
		if rollup == nil {
			continue
		}
		prs[index].ChecksTruncated = rollup.Contexts.PageInfo.HasNextPage
		for _, context := range rollup.Contexts.Nodes {
			switch context.TypeName {
			case "CheckRun":
				prs[index].CheckContexts = append(prs[index].CheckContexts, apiCheckContext{
					Name: context.Name, Status: context.Status, Conclusion: context.Conclusion,
				})
			case "StatusContext":
				prs[index].CheckContexts = append(prs[index].CheckContexts, apiCheckContext{
					Name: context.Context, Status: context.State,
				})
			}
		}
	}
}

func loadRequiredChecks(path string) ([]string, error) {
	resolved, err := findRepositoryFile(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	var contract struct {
		RequiredStatusChecks struct {
			Contexts []string `json:"contexts"`
		} `json:"required_status_checks"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		return nil, fmt.Errorf("decode branch protection: %w", err)
	}
	if len(contract.RequiredStatusChecks.Contexts) == 0 {
		return nil, fmt.Errorf("branch protection has no required status contexts")
	}
	return contract.RequiredStatusChecks.Contexts, nil
}

func findRepositoryFile(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(directory, path)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return "", fmt.Errorf("repository file %q not found", path)
}

func loadIssues(repo, fixture string, skipLive bool) ([]apiIssue, error) {
	if fixture != "" {
		data, err := os.ReadFile(fixture)
		if err != nil {
			return nil, err
		}
		var issues []apiIssue
		if err := json.Unmarshal(data, &issues); err != nil {
			return nil, fmt.Errorf("decode issue fixture: %w", err)
		}
		return issues, nil
	}
	// A PR fixture must remain a fully offline diagnostic input. Callers that
	// want both fixture kinds pass -prs and -issues together.
	if skipLive {
		return []apiIssue{}, nil
	}

	gh := os.Getenv("GH_EXE")
	if gh == "" {
		gh = "gh"
	}
	var all []apiIssue
	if err := ghJSONLines(gh, &all, "api", "--paginate",
		"repos/"+repo+"/issues?state=open&per_page=100&sort=created&direction=asc",
		"--jq", ".[]"); err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	issues := make([]apiIssue, 0, len(all))
	for _, issue := range all {
		if issue.PullRequest == nil && issue.CommentCount > 0 {
			issues = append(issues, issue)
		}
	}

	jobs := make(chan int)
	errs := make(chan error, len(issues))
	var wg sync.WaitGroup
	workers := 6
	if len(issues) < workers {
		workers = len(issues)
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				path := fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", repo, issues[index].Number)
				if err := ghJSONLines(gh, &issues[index].Thread, "api", "--paginate", path, "--jq", ".[]"); err != nil {
					errs <- fmt.Errorf("comments for issue #%d: %w", issues[index].Number, err)
				}
			}
		}()
	}
	for index := range issues {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return issues, nil
}

func ghJSONLines(gh string, destination any, args ...string) error {
	// GH_EXE is an explicit operator setting, and arguments are passed without a shell.
	//nolint:gosec // The executable path is trusted configuration, not GitHub data.
	cmd := exec.Command(gh, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	// gh --paginate --jq '.[]' emits one JSON object per line. Decode into a
	// temporary generic slice, then marshal once into the typed destination.
	var values []json.RawMessage
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			values = append(values, json.RawMessage(line))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}

func analyze(prs []apiPull, owner string) report {
	return analyzeWithRequiredChecks(prs, owner, owner+"/onebase", []string{
		"build", "lint", "postgres-integration", "vuln", "smoke", "e2e", "test-windows", "launcher-webview-build",
	})
}

func analyzeWithRequiredChecks(prs []apiPull, owner, repository string, requiredChecks []string) report {
	result := report{
		State: "green", Scope: "REST + batched GraphQL admission; mutation gates remain full GraphQL",
		Scheduler: "three-lane-pre-review-safety-priority-aging-depth-number", Checked: len(prs),
		ReviewCandidates: []candidate{}, ContentReviewCandidates: []candidate{},
		ReviewBacklog: []candidate{}, ReviewedWaitingShip: []candidate{}, MergeCandidates: []candidate{}, MergeExecutable: []candidate{}, PlanCandidates: []candidate{}, FixCandidates: []candidate{},
		PreReviewSyncCandidates: []candidate{}, PreReviewWaitingCI: []candidate{},
		HumanWaiting: []candidate{}, Findings: []finding{},
	}
	now := time.Now().UTC()
	for _, pr := range prs {
		if pr.State != "open" || pr.Base.Ref != "main" {
			continue
		}
		sort.Slice(pr.Comments, func(i, j int) bool {
			if pr.Comments[i].CreatedAt == pr.Comments[j].CreatedAt {
				return pr.Comments[i].ID < pr.Comments[j].ID
			}
			return pr.Comments[i].CreatedAt < pr.Comments[j].CreatedAt
		})
		labels := labelSet(pr.Labels)
		depth := reviewDepth(pr.Comments, owner)
		priority, prioritySource := queuePriority(labels, pr.CreatedAt, now)
		item := candidate{Number: pr.Number, Title: pr.Title, URL: pr.HTMLURL, Head: pr.Head.SHA, Depth: depth, Stage: "review", Priority: priority, PrioritySource: prioritySource, UpdatedAt: pr.UpdatedAt}
		currentCompletions, latestCompletion, latestOverride := currentProtocolState(pr.Comments, owner, pr.Head.SHA)
		carryDone, carryIntentOpen, baseAdvanced, protocolHistory, integrationAt := baseSyncRESTState(pr.Comments, owner, pr.Head.SHA, pr.HeadParents)
		prep := preReviewSyncRESTState(pr.Comments, owner, pr.Head.SHA, pr.HeadParents)
		item.IntegrationAt = integrationAt
		if baseAdvanced {
			result.add("yellow", "base_sync_base_advanced", pr.Number,
				"base сдвинулся между intent и done; GraphQL gate должен проверить actual parent и ancestry")
		}

		if labels["changes-requested"] && labels["needs-decision"] {
			result.add("yellow", "route_transition_open", pr.Number,
				"одновременно стоят changes-requested и needs-decision; допустимо только во время handoff")
		}
		if labels["ship"] && labels["changes-requested"] {
			result.add("red", "ship_with_blocking_route", pr.Number,
				"ship конфликтует с changes-requested; интеграционный REVIEW должен был снять разрешение")
		}
		if duplicateCompletionEpoch(pr.Comments, owner, pr.Head.SHA) {
			result.add("red", "same_head_reviewed_twice", pr.Number,
				"у текущего HEAD два разных committed-review без разделяющего pp:review-again")
		}
		if currentCompletions == 0 && currentClaimCount(pr.Comments, owner, pr.Head.SHA) > 0 {
			result.add("yellow", "unfinished_review_transaction", pr.Number,
				"на текущем HEAD есть claim без committed completion; нужен recovery")
		}
		hasReviewActivity := currentCompletions > 0 || latestOverride > latestCompletion ||
			currentClaimCount(pr.Comments, owner, pr.Head.SHA) > 0 ||
			hasReviewComment(pr.Comments, owner, pr.Head.SHA)
		if prep.resumed {
			hasReviewActivity = hasReviewActivityAfter(
				pr.Comments, owner, pr.Head.SHA, prep.resumeCommentID, true,
			)
		}

		if pr.Draft || labels["hold"] {
			continue
		}
		if pr.AdmissionError != "" {
			result.add("yellow", "pull_admission_stale", pr.Number,
				"PR изменился между REST и GraphQL snapshot; только он отложен до следующего refresh: "+pr.AdmissionError)
			continue
		}
		if prep.blocked && !labels["needs-decision"] {
			item.Stage = "pre-review-sync-recovery"
			result.PreReviewSyncCandidates = append(result.PreReviewSyncCandidates, item)
			result.FixCandidates = append(result.FixCandidates, item)
			result.add("yellow", "pre_review_sync_handoff_recovery", pr.Number,
				"blocked marker уже видим, но needs-decision ещё не поставлена; FIX завершает только fail-safe handoff")
			continue
		}
		if prep.malformed || prep.orphaned || prep.cycled || prep.blocked {
			item.Stage = "human-decision"
			result.HumanWaiting = append(result.HumanWaiting, item)
			if prep.malformed {
				result.add("red", "pre_review_sync_malformed", pr.Number,
					"pp:pre-review-sync-done текущего HEAD не совпадает с earliest intent или exact parents; автоматическая работа запрещена")
			} else if prep.orphaned {
				result.add("yellow", "pre_review_sync_orphaned", pr.Number,
					"после earliest open pp:pre-review-sync-intent появился посторонний HEAD; требуется решение человека")
			} else if prep.cycled {
				result.add("yellow", "pre_review_sync_cycle", pr.Number,
					"current HEAD вернулся к from уже завершённого pre-review-sync hop; автоматический новый цикл запрещён")
			} else {
				result.add("yellow", "pre_review_sync_recovery_blocked", pr.Number,
					"stable post-intent event передал recovery человеку; PR больше не исполняемый FIX target")
			}
			continue
		}
		if prep.intentOpen || (currentCompletions == 0 && prep.validation != nil) {
			expectedIdentity := ""
			if prep.validation != nil {
				expectedIdentity = prep.validation.IdentitySHA256
			}
			if prep.intentOpen {
				expectedIdentity = prep.openIdentity
			}
			if reason := preReviewIdentityBlocker(pr, repository, expectedIdentity); reason != "" {
				item.Stage = "human-decision"
				result.HumanWaiting = append(result.HumanWaiting, item)
				result.add("yellow", "pre_review_sync_identity_changed", pr.Number, reason)
				continue
			}
		}
		if prep.validation != nil && currentCompletions > 0 &&
			!hasCanonicalCompletionAfter(pr.Comments, owner, pr.Head.SHA, prep.validation.DoneCommentID) {
			item.Stage = "human-decision"
			result.HumanWaiting = append(result.HumanWaiting, item)
			result.add("red", "pre_review_validation_out_of_order", pr.Number,
				"review proof текущего pre-review-sync HEAD создан до matching done и не доказывает provenance-validation")
			continue
		}
		if prep.intentOpen {
			if (labels["needs-decision"] && !prep.resumed) || labels["changes-requested"] || hasReviewActivity {
				item.Stage = "human-decision"
				result.HumanWaiting = append(result.HumanWaiting, item)
				result.add("yellow", "pre_review_sync_recovery_blocked", pr.Number,
					"earliest open pre-review-sync intent пересечён blocking route или REVIEW event; автоматический recovery запрещён")
				continue
			}
			item.Stage = "pre-review-sync-recovery"
			result.PreReviewSyncCandidates = append(result.PreReviewSyncCandidates, item)
			result.FixCandidates = append(result.FixCandidates, item)
			result.add("yellow", "pre_review_sync_recovery", pr.Number,
				"есть pp:pre-review-sync-intent без done; FIX должен восстановить exact транзакцию до новой работы")
			continue
		}
		prepForCurrentBase := prep.validation != nil && prep.validation.Base == pr.Base.OID && pr.Base.OID != ""
		newPrepEligible := !hasReviewActivity && !labels["changes-requested"] &&
			preReviewSyncAdmission(pr, requiredChecks, prepForCurrentBase)
		if prep.validation != nil && currentCompletions == 0 && !newPrepEligible {
			if labels["needs-decision"] || labels["changes-requested"] || hasReviewActivity {
				item.Stage = "human-decision"
				result.HumanWaiting = append(result.HumanWaiting, item)
				result.add("yellow", "pre_review_validation_blocked", pr.Number,
					"completed pre-review-sync пересечён blocking route или незавершённой REVIEW-транзакцией; автоматическая validation запрещена")
				continue
			}
			switch requiredChecksState(pr, requiredChecks) {
			case checksPending:
				item.Stage = "pre-review-ci-wait"
				result.PreReviewWaitingCI = append(result.PreReviewWaitingCI, item)
				if preReviewCINeedsAttention(pr, prep.validation, requiredChecks, now) {
					attention := item
					attention.Stage = "ci-needs-attention"
					result.HumanWaiting = append(result.HumanWaiting, attention)
					result.add("yellow", "pre_review_sync_ci_needs_attention", pr.Number,
						"после pre-review-sync required CI не появился за 30 минут; проверь approval workflow для fork или GitHub Actions")
				} else {
					result.add("yellow", "pre_review_sync_waiting_ci", pr.Number,
						"pre-review-sync завершён; обязательный CI exact HEAD ещё не сформирован или выполняется")
				}
				continue
			case checksUnknown:
				result.add("yellow", "pre_review_sync_ci_unknown", pr.Number,
					"pre-review-sync завершён, но status rollup exact HEAD неполон; мутация запрещена")
				continue
			case checksReady, checksFailed:
				item.Stage = "pre-review-validation"
				item.PreReviewSyncValidation = prep.validation
				result.ContentReviewCandidates = append(result.ContentReviewCandidates, item)
				result.add("yellow", "pre_review_sync_waiting_review", pr.Number,
					"pre-review-sync завершён; exact handoff должен пройти validation, затем полное содержательное REVIEW без carry")
				continue
			}
		}
		if newPrepEligible {
			item.Stage = "pre-review-sync"
			if reason := preReviewSourceBlocker(pr, repository); reason != "" {
				item.Stage = "human-decision"
				result.HumanWaiting = append(result.HumanWaiting, item)
				result.add("yellow", "pre_review_sync_source_blocked", pr.Number, reason)
				continue
			}
			result.PreReviewSyncCandidates = append(result.PreReviewSyncCandidates, item)
			result.FixCandidates = append(result.FixCandidates, item)
			result.add("yellow", "pre_review_sync_required", pr.Number,
				"DIRTY/CONFLICTING HEAD не получил обязательный CI; FIX должен выполнить безопасный pre-review-sync")
			continue
		}
		if !hasReviewActivity && !labels["changes-requested"] && preReviewAdmissionPending(pr, requiredChecks) {
			result.add("yellow", "pre_review_sync_admission_pending", pr.Number,
				"mergeability/status rollup exact HEAD ещё не дают стабильного решения; PR временно исключён из REVIEW и FIX")
			continue
		}
		if labels["ship"] {
			switch {
			case labels["needs-decision"]:
				result.HumanWaiting = append(result.HumanWaiting, item)
			case carryIntentOpen:
				item.Stage = "integration-merge-recovery"
				result.ReviewCandidates = append(result.ReviewCandidates, item)
				result.MergeCandidates = append(result.MergeCandidates, item)
				result.add("yellow", "base_sync_recovery", pr.Number,
					"есть pp:base-sync-intent без done; MERGE должен восстановить транзакцию")
			case carryDone && currentCompletions > 0:
				item.Stage = "integration-merge-ready"
				result.ReviewCandidates = append(result.ReviewCandidates, item)
				result.MergeCandidates = append(result.MergeCandidates, item)
				result.add("yellow", "base_sync_waiting_merge", pr.Number,
					"интеграционное REVIEW готово; барьер остаётся у PR до фактического merge")
			case prep.validation != nil && currentCompletions > 0:
				// A pre-review-sync merge is content preparation, not a MERGE-owned
				// integration commit. Once the new HEAD has its own full review and
				// a fresh ship decision it must use the ordinary merge lane even
				// though its two-parent shape and older review depth resemble a
				// legacy base-sync.
				item.Stage = "merge"
				result.MergeCandidates = append(result.MergeCandidates, item)
			case currentCompletions > 0 && depth > currentCompletions && headIsBaseSyncMerge(pr):
				item.Stage = "legacy-integration-merge-ready"
				result.ReviewCandidates = append(result.ReviewCandidates, item)
				result.MergeCandidates = append(result.MergeCandidates, item)
				result.add("yellow", "legacy_ship_waiting_merge", pr.Number,
					"legacy-интеграционное REVIEW готово; следующий ход принадлежит MERGE")
			case carryDone && currentCompletions == 0:
				item.Stage = "integration-review"
				result.ReviewCandidates = append(result.ReviewCandidates, item)
				result.add("yellow", "base_sync_waiting_review", pr.Number,
					"ship сохранён; текущий HEAD ожидает интеграционное REVIEW")
			case currentCompletions == 0 && depth > 0 && headIsBaseSyncMerge(pr):
				// REST cannot prove legacy timeline edge order, but the merge shape of
				// the head commit is a fact. Expose this as a priority candidate;
				// REVIEW still performs the full GraphQL gate.
				item.Stage = "legacy-integration-review"
				result.ReviewCandidates = append(result.ReviewCandidates, item)
				result.add("yellow", "legacy_ship_waiting_review_validation", pr.Number,
					"повторный ship после старого base-sync: REVIEW должен проверить GraphQL lineage")
			case currentCompletions == 0 && depth == 0 && !protocolHistory:
				// ship is sticky intent for this exact HEAD, not proof that review has
				// already happened. Keep a first-time PR executable in the content lane.
				result.ContentReviewCandidates = append(result.ContentReviewCandidates, item)
				result.add("yellow", "ship_waiting_initial_review", pr.Number,
					"ship сохранён как разрешение слить этот HEAD после успешного REVIEW")
			case currentCompletions == 0 && !protocolHistory:
				// Ordinary next FIX round: earlier HEADs were reviewed, this one is a
				// plain push. It belongs to the content lane, not the integration lane.
				result.ContentReviewCandidates = append(result.ContentReviewCandidates, item)
				result.add("yellow", "ship_waiting_next_round_review", pr.Number,
					"ship сохранён; новый HEAD после доработки ожидает обычное REVIEW")
			case currentCompletions > 0:
				item.Stage = "merge"
				result.MergeCandidates = append(result.MergeCandidates, item)
			default:
				result.HumanWaiting = append(result.HumanWaiting, item)
				result.add("yellow", "ship_without_current_review_proof", pr.Number,
					"ship есть, но доказательств ревью текущего HEAD нет при существующей истории протокола; нужен человек")
			}
			continue
		}
		overrideOpen := latestOverride > latestCompletion
		switch {
		case labels["needs-decision"] && !overrideOpen:
			result.HumanWaiting = append(result.HumanWaiting, item)
		case labels["changes-requested"] && !overrideOpen:
			item.Stage = "fix-review"
			result.FixCandidates = append(result.FixCandidates, item)
		case labels["reviewed"] && currentCompletions > 0 && !overrideOpen:
			// Valid-looking current review is waiting for the human ship decision.
			result.ReviewedWaitingShip = append(result.ReviewedWaitingShip, item)
		case currentCompletions > 0 && !overrideOpen:
			result.add("yellow", "review_without_route", pr.Number,
				"committed-review текущего HEAD есть, но маршрутная метка отсутствует")
			result.HumanWaiting = append(result.HumanWaiting, item)
		default:
			result.ContentReviewCandidates = append(result.ContentReviewCandidates, item)
		}
	}
	sortCandidates(result.ContentReviewCandidates)
	sortCandidates(result.ReviewCandidates)
	applySingleFlight(&result)
	setMergeExecutable(&result)
	result.ReviewBacklog = append(result.ReviewBacklog, result.ContentReviewCandidates...)
	if result.IntegrationOwner != nil && candidatePriority(result.IntegrationOwner.Stage) == 1 {
		result.ReviewBacklog = append(result.ReviewBacklog, *result.IntegrationOwner)
	}
	sortCandidates(result.ReviewBacklog)
	sortCandidates(result.ReviewedWaitingShip)
	sortMergeCandidates(result.MergeCandidates)
	sortMergeCandidates(result.MergeExecutable)
	sortFixCandidates(result.FixCandidates)
	sortFixCandidates(result.PreReviewSyncCandidates)
	sortCandidates(result.PreReviewWaitingCI)
	sortCandidates(result.HumanWaiting)
	return result
}

func analyzeIssues(result *report, issues []apiIssue, prs []apiPull, owner string) {
	result.IssuesChecked = len(issues)
	now := time.Now().UTC()
	for _, issue := range issues {
		if issue.State != "open" {
			continue
		}
		repaired := map[int64]bool{}
		for _, comment := range issue.Thread {
			if !trustedUnedited(comment, owner) {
				continue
			}
			for _, match := range displayRepair.FindAllStringSubmatch(comment.Body, -1) {
				id, err := strconv.ParseInt(match[1], 10, 64)
				if err == nil && id < comment.ID {
					repaired[id] = true
				}
			}
		}
		for _, comment := range issue.Thread {
			if !trustedUnedited(comment, owner) || repaired[comment.ID] {
				continue
			}
			visible, ok := triageVisibleText(comment.Body)
			if ok && looksLikeUTF8DecodedAsWindows1251(visible) {
				result.addIssue("red", "triage_text_mojibake", issue.Number,
					fmt.Sprintf("TRIAGE comment %d повреждён кодировкой и не имеет pp:display-repair", comment.ID))
			}
		}

		labels := labelSet(issue.Labels)
		route := inspectTriageRoute(issue, owner)
		routeFinding := false
		routeMismatch := false
		if route.hasClaim && !route.ready {
			result.addIssue("yellow", "fix_issue_not_executable", issue.Number, route.reason)
			routeFinding = true
		}
		if route.hasClaim && route.ready {
			switch {
			case route.route == "ready-fix" && labels["needs-decision"] && !labels["approved"]:
				routeMismatch = true
				result.addIssue("yellow", "triage_route_label_mismatch", issue.Number,
					"TRIAGE route=ready-fix, но issue помечена needs-decision без последующего approved")
			case route.route == "needs-decision" && labels["ready-fix"] && !labels["approved"]:
				routeMismatch = true
				result.addIssue("yellow", "triage_route_label_mismatch", issue.Number,
					"TRIAGE route=needs-decision, но issue помечена ready-fix без следов решения человека")
			}
		}
		priority, prioritySource := queuePriority(labels, issue.CreatedAt, now)
		item := candidate{
			Number: issue.Number, Title: issue.Title, URL: issue.HTMLURL,
			Stage: "fix-issue", Priority: priority, PrioritySource: prioritySource,
			UpdatedAt: issue.UpdatedAt,
		}
		if labels["hold"] || labels["manual"] {
			continue
		}
		if routeMismatch {
			item.Stage = "human-decision"
			result.HumanWaiting = append(result.HumanWaiting, item)
			continue
		}
		switch {
		case labels["plan-needed"] && labels["approved"]:
			item.Stage = "plan"
			result.PlanCandidates = append(result.PlanCandidates, item)
		case labels["plan-needed"]:
			item.Stage = "plan-needs-approval"
			result.HumanWaiting = append(result.HumanWaiting, item)
		case labels["plan-in-review"]:
			// The plan PR is visible in REVIEW; product FIX must wait for its merge.
		case labels["approved"] || labels["ready-fix"] && !labels["needs-decision"]:
			if labels["in-work"] || issueReferencedByOpenPull(issue.Number, prs) {
				continue
			}
			if !route.ready {
				if !routeFinding {
					result.addIssue("yellow", "fix_issue_not_executable", issue.Number, route.reason)
				}
				continue
			}
			result.FixCandidates = append(result.FixCandidates, item)
		case labels["needs-decision"]:
			item.Stage = "human-decision"
			result.HumanWaiting = append(result.HumanWaiting, item)
		}
	}
	sortCandidates(result.PlanCandidates)
	sortFixCandidates(result.FixCandidates)
	sortCandidates(result.HumanWaiting)
}

func issueReferencedByOpenPull(number int, prs []apiPull) bool {
	pattern := regexp.MustCompile(fmt.Sprintf(`(^|[^0-9])#%d([^0-9]|$)`, number))
	for _, pr := range prs {
		if pr.State == "open" && pattern.MatchString(pr.Title+"\n"+pr.Body) {
			return true
		}
	}
	return false
}

type triageRouteState struct {
	hasClaim bool
	ready    bool
	route    string
	reason   string
}

func inspectTriageRoute(issue apiIssue, owner string) triageRouteState {
	thread := append([]apiComment(nil), issue.Thread...)
	sort.SliceStable(thread, func(i, j int) bool {
		if thread[i].CreatedAt == thread[j].CreatedAt {
			return thread[i].ID < thread[j].ID
		}
		return thread[i].CreatedAt < thread[j].CreatedAt
	})
	var root *apiComment
	for index := range thread {
		comment := &thread[index]
		if !trustedUnedited(*comment, owner) || !hasExactLine(comment.Body, "<!-- pp:triage -->") {
			continue
		}
		if root == nil || comment.CreatedAt < root.CreatedAt ||
			(comment.CreatedAt == root.CreatedAt && comment.ID < root.ID) {
			root = comment
		}
	}
	if root == nil {
		return triageRouteState{reason: "eligible FIX issue has no canonical trusted triage"}
	}
	if !strings.Contains(root.Body, "pp:triage-route-claim") {
		return triageRouteState{ready: true}
	}
	state := triageRouteState{hasClaim: true}
	normalized := strings.ReplaceAll(root.Body, "\r\n", "\n")
	claims := triageRouteClaim.FindAllStringSubmatch(root.Body, -1)
	if len(claims) != 1 {
		state.reason = "canonical triage has a malformed route claim"
		return state
	}
	fingerprint := claims[0][1]
	records := triageRouteRecord.FindAllStringSubmatch(normalized, -1)
	if len(records) != 1 || fmt.Sprintf("%x", sha256.Sum256([]byte(records[0][1]))) != fingerprint {
		state.reason = "canonical triage route record is malformed or its fingerprint does not match"
		return state
	}
	recordIssue, err := strconv.Atoi(records[0][2])
	if err != nil || recordIssue != issue.Number {
		state.reason = "canonical triage route record names another issue"
		return state
	}
	state.route = records[0][3]
	claimID := strconv.FormatInt(root.ID, 10)
	labelsCommitted, replyCommitted, done := false, false, false
	replyRequired := records[0][4] == "required"
	for _, comment := range thread {
		if !trustedUnedited(comment, owner) || comment.CreatedAt < root.CreatedAt ||
			(comment.CreatedAt == root.CreatedAt && comment.ID <= root.ID) {
			continue
		}
		for _, match := range triageRouteLabels.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] == claimID && match[2] == fingerprint {
				labelsCommitted = true
			}
		}
		for _, match := range triageAuthorReply.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] == claimID && match[2] == fingerprint {
				replyCommitted = true
			}
		}
		for _, match := range triageRouteDone.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] == claimID && match[2] == fingerprint && labelsCommitted && (!replyRequired || replyCommitted) {
				done = true
			}
		}
	}
	if !done {
		state.reason = "TRIAGE route claim is unfinished; FIX must wait for matching labels/reply/done markers"
		return state
	}
	state.ready = true
	return state
}

func hasExactLine(body, line string) bool {
	for _, value := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if value == line {
			return true
		}
	}
	return false
}

func triageVisibleText(body string) (string, bool) {
	const marker = "<!-- pp:triage -->"
	for offset := 0; offset <= len(body); {
		next := strings.Index(body[offset:], marker)
		if next < 0 {
			return "", false
		}
		index := offset + next
		lineStart := index == 0 || body[index-1] == '\n'
		lineEnd := index+len(marker) == len(body) || body[index+len(marker)] == '\n'
		if lineStart && lineEnd {
			return strings.TrimSuffix(body[:index], "\n"), true
		}
		offset = index + len(marker)
	}
	return "", false
}

func looksLikeUTF8DecodedAsWindows1251(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(line))
		if err != nil || !utf8.Valid(encoded) || string(encoded) == line {
			continue
		}
		// Reversible conversion alone can match a short, unusual but legitimate
		// string. These artifacts are characteristic of UTF-8 Russian decoded as
		// Windows-1251 and keep the health check conservative.
		if strings.Contains(line, "вЂ") || strings.Contains(line, "В«") ||
			strings.Contains(line, "В»") || strings.Count(line, "Р")+strings.Count(line, "С") >= 3 {
			return true
		}
	}
	return false
}

func checkContract(result *report, path string) {
	data, err := readContract(path)
	if err != nil {
		result.add("red", "contract_unreadable", 0, fmt.Sprintf("не удалось прочитать активный REVIEW contract: %v", err))
		return
	}
	text := string(data)
	for _, required := range []string{
		"(priority ASC, review-depth ASC, number ASC)",
		"Не сортируй очередь только по номеру PR",
		"single_flight_barrier` защищает только интеграционную полосу",
		"Интеграционное REVIEW не повторяет содержательный аудит",
	} {
		if !strings.Contains(text, required) {
			result.add("red", "unfair_review_contract", 0,
				"активный REVIEW contract не гарантирует breadth-first порядок")
			return
		}
	}
	for _, required := range []string{
		"Полный health-election выполняется один раз в `next review`",
		"обычная `stage=review` либо специальная `stage=pre-review-validation` цель",
		"review_completion_gate=target-v1",
		"номер/HEAD цели, open/base/draft,",
		"routing labels, review-depth и стабильную server timeline/epoch",
		"HMAC",
		"expires_at",
		"Для integration-stage и любого fallback-протокола повторная глобальная",
		"проверка перед мутацией остаётся обязательной",
	} {
		if !strings.Contains(text, required) {
			result.add("red", "unsafe_target_review_contract", 0,
				"активный REVIEW contract не связывает быстрый target-gate с выданной целью")
			return
		}
	}
	skillsRoot := filepath.Dir(filepath.Dir(path))
	mergeData, err := readContract(filepath.Join(skillsRoot, "merge-shepherd", "SKILL.md"))
	if err != nil || !strings.Contains(text, "pp:base-sync-done") ||
		!strings.Contains(text, "single-flight-барьер") ||
		!strings.Contains(string(mergeData), "pp:base-sync-intent") ||
		!strings.Contains(string(mergeData), "pp:merge-cleanup-intent") ||
		!strings.Contains(string(mergeData), "complete merge-cleanup") ||
		!strings.Contains(string(mergeData), "повторный человеческий `ship` при валидной") ||
		!strings.Contains(string(mergeData), "single-flight-барьер") {
		result.add("red", "unsafe_base_sync_contract", 0,
			"активные REVIEW/MERGE contracts не гарантируют перенос ship и single-flight через доказанный base-sync")
		return
	}
	fixData, err := readContract(filepath.Join(skillsRoot, "fix-approved", "SKILL.md"))
	if err != nil ||
		!strings.Contains(string(fixData), "pre_review_sync_candidates") ||
		!strings.Contains(string(fixData), "git merge --no-commit --no-ff <exact base SHA>") ||
		!strings.Contains(string(fixData), "PP-Pre-Review-Sync: intent=<id>") ||
		!strings.Contains(string(fixData), "pp:pre-review-sync-recovery-blocked") ||
		!strings.Contains(string(fixData), "Ни один") ||
		!strings.Contains(string(fixData), "файл из head fork нельзя запускать") ||
		!strings.Contains(text, "pre_review_sync_candidates") ||
		!strings.Contains(text, "target.stage=pre-review-validation") ||
		!strings.Contains(text, "объектом **ровно** из восьми полей") ||
		!strings.Contains(text, "содержательное REVIEW") ||
		!strings.Contains(string(mergeData), "никогда не становится integration owner MERGE") ||
		!strings.Contains(string(mergeData), "последний trusted ship-transition строго после") {
		result.add("red", "unsafe_pre_review_sync_contract", 0,
			"FIX/REVIEW/MERGE contracts не разделяют безопасную подготовку конфликтующего PR и человеческое разрешение merge")
		return
	}
	for _, name := range []string{"triage-issues", "plan-approved", "fix-approved", "review-queue", "merge-shepherd", "tail-issues"} {
		data, err := readContract(filepath.Join(skillsRoot, name, "SKILL.md"))
		if err != nil {
			result.add("red", "utf8_contract_unreadable", 0,
				fmt.Sprintf("не удалось прочитать %s contract: %v", name, err))
			return
		}
		for _, required := range []string{"$OutputEncoding = $utf8", "-Encoding UTF8 -Raw", "`@base64`", "байт-в-байт"} {
			if !strings.Contains(string(data), required) {
				result.add("red", "unsafe_utf8_contract", 0,
					fmt.Sprintf("активный %s contract не гарантирует UTF-8 до GitHub-мутаций", name))
				return
			}
		}
	}
}

func readContract(path string) ([]byte, error) {
	entry, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	legacyPath := filepath.Join(filepath.Dir(path), "references", "legacy-protocol.md")
	legacy, legacyErr := os.ReadFile(legacyPath)
	if legacyErr == nil {
		return append(append(entry, '\n'), legacy...), nil
	}
	if !os.IsNotExist(legacyErr) {
		return nil, legacyErr
	}
	return entry, nil
}

func (result *report) finish() {
	red, yellow := 0, 0
	for _, item := range result.Findings {
		switch item.Severity {
		case "red":
			red++
		case "yellow":
			yellow++
		}
	}
	if red > 0 {
		result.State = "red"
	} else if yellow > 0 || len(result.HumanWaiting) > 0 {
		result.State = "yellow"
	}
	next := "нет"
	if len(result.ReviewCandidates) > 0 {
		limit := 2
		if len(result.ReviewCandidates) < limit {
			limit = len(result.ReviewCandidates)
		}
		parts := make([]string, 0, limit)
		for _, item := range result.ReviewCandidates[:limit] {
			parts = append(parts, fmt.Sprintf("#%d(d=%d)", item.Number, item.Depth))
		}
		next = strings.Join(parts, ", ")
	}
	owner := "нет"
	if result.IntegrationOwner != nil {
		owner = fmt.Sprintf("#%d(%s)", result.IntegrationOwner.Number, result.IntegrationOwner.Stage)
	}
	result.Summary = fmt.Sprintf(
		"PR: %d; issues: %d; REVIEW исполняемо: %d (следующие %s); всего ждут REVIEW: %d; содержательное: %d; pre-review-sync: %d; ждут CI после sync: %d; интеграционный владелец: %s; ждут ship: %d; MERGE исполняемо: %d; всего MERGE: %d; PLAN: %d; FIX: %d; человек: %d; сигналов: %d",
		result.Checked, result.IssuesChecked, len(result.ReviewCandidates), next,
		len(result.ReviewBacklog), len(result.ContentReviewCandidates), len(result.PreReviewSyncCandidates), len(result.PreReviewWaitingCI), owner, len(result.ReviewedWaitingShip), len(result.MergeExecutable), len(result.MergeCandidates), len(result.PlanCandidates), len(result.FixCandidates),
		len(result.HumanWaiting), len(result.Findings))
}

func (result *report) add(severity, code string, pr int, message string) {
	result.Findings = append(result.Findings, finding{Severity: severity, Code: code, PR: pr, Message: message})
}

func (result *report) addIssue(severity, code string, issue int, message string) {
	result.Findings = append(result.Findings, finding{Severity: severity, Code: code, Issue: issue, Message: message})
}

// needsHeadParents limits the extra commit read to pull requests whose stage
// can depend on it: an open ship candidate targeting main.
func needsHeadParents(pr apiPull) bool {
	if pr.State != "open" || pr.Base.Ref != "main" || pr.Draft || pr.Head.SHA == "" {
		return false
	}
	if labelSet(pr.Labels)["ship"] {
		return true
	}
	for _, comment := range pr.Comments {
		if preReviewIntent.MatchString(comment.Body) || preReviewDone.MatchString(comment.Body) {
			return true
		}
	}
	return false
}

// headIsBaseSyncMerge reports whether the head commit has the shape every
// base-sync leaves behind: a merge of the reviewed head with the base branch.
// Review history alone never proves this, and an ordinary FIX round —
// changes-requested, push, green review — produces exactly the same comment
// trail as a legacy base-sync. Unknown parents count as "not a merge": a false
// integration owner hides the real one from REVIEW and deadlocks the lane,
// while a missed one only falls back to the full GraphQL gate in MERGE.
func headIsBaseSyncMerge(pr apiPull) bool {
	return len(pr.HeadParents) == 2
}

func labelSet(labels []apiLabel) map[string]bool {
	set := make(map[string]bool, len(labels))
	for _, item := range labels {
		set[item.Name] = true
	}
	return set
}

func trustedUnedited(comment apiComment, owner string) bool {
	return comment.User.Login == owner && comment.CreatedAt != "" && comment.UpdatedAt == comment.CreatedAt
}

func reviewDepth(comments []apiComment, owner string) int {
	ids := map[string]bool{}
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		for _, match := range completionLine.FindAllStringSubmatch(comment.Body, -1) {
			ids[match[2]] = true
		}
	}
	return len(ids)
}

func currentProtocolState(comments []apiComment, owner, head string) (count int, latestCompletion, latestOverride int64) {
	ids := map[string]bool{}
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		if reviewAgain.MatchString(comment.Body) {
			latestOverride = comment.ID
		}
		for _, match := range completionLine.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] != head {
				continue
			}
			ids[match[2]] = true
			if comment.ID > latestCompletion {
				latestCompletion = comment.ID
			}
		}
	}
	return len(ids), latestCompletion, latestOverride
}

func currentClaimCount(comments []apiComment, owner, head string) int {
	count := 0
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		for _, match := range claimLine.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] == head {
				count++
			}
		}
	}
	return count
}

func hasReviewComment(comments []apiComment, owner, head string) bool {
	return hasReviewActivityAfter(comments, owner, head, 0, false)
}

func hasReviewActivityAfter(comments []apiComment, owner, head string, afterID int64, includeCompletions bool) bool {
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) || comment.ID <= afterID {
			continue
		}
		if reviewAgain.MatchString(comment.Body) {
			return true
		}
		for _, match := range claimLine.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] == head {
				return true
			}
		}
		if includeCompletions {
			for _, match := range completionLine.FindAllStringSubmatch(comment.Body, -1) {
				if match[1] == head {
					return true
				}
			}
		}
		if reviewMarkerLine.MatchString(comment.Body) {
			for _, match := range reviewedSHALine.FindAllStringSubmatch(comment.Body, -1) {
				if match[1] == head {
					return true
				}
			}
		}
	}
	return false
}

func hasCanonicalCompletionAfter(comments []apiComment, owner, head string, afterID int64) bool {
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) || comment.ID <= afterID {
			continue
		}
		for _, match := range completionLine.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] != head {
				continue
			}
			reviewID, reviewErr := strconv.ParseInt(match[2], 10, 64)
			claimID, claimErr := strconv.ParseInt(match[3], 10, 64)
			if reviewErr == nil && claimErr == nil && reviewID > afterID && claimID > afterID {
				return true
			}
		}
	}
	return false
}

type preReviewIntentShape struct {
	from, base, identity string
}

type preReviewSyncState struct {
	intentOpen      bool
	openIdentity    string
	malformed       bool
	orphaned        bool
	cycled          bool
	blocked         bool
	resumed         bool
	resumeCommentID int64
	validation      *preReviewSyncValidation
}

type preReviewTransitionMarker struct {
	commentID int64
	head      string
}

// preReviewSyncRESTState is an operational election hint only. The FIX skill
// reconstructs the full server-ordered timeline, exact identity, parents,
// trailer and timestamps before it changes a branch or publishes done.
func preReviewSyncRESTState(comments []apiComment, owner, head string, headParents []string) preReviewSyncState {
	intents := map[int64]preReviewIntentShape{}
	intentCreatedAt := map[int64]string{}
	canonicalIntent := map[preReviewIntentShape]int64{}
	canonicalOrder := make([]int64, 0)
	doneIntents := map[int64]bool{}
	blockedIntents := map[int64]preReviewTransitionMarker{}
	resumedIntents := map[int64]preReviewTransitionMarker{}
	state := preReviewSyncState{}
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		if match := preReviewIntent.FindStringSubmatch(comment.Body); match != nil {
			if comment.ID <= 0 {
				continue
			}
			intent := preReviewIntentShape{from: match[1], base: match[2], identity: match[3]}
			intents[comment.ID] = intent
			intentCreatedAt[comment.ID] = comment.CreatedAt
			if _, exists := canonicalIntent[intent]; !exists {
				canonicalIntent[intent] = comment.ID
				canonicalOrder = append(canonicalOrder, comment.ID)
			}
		}
		if match := preReviewBlocked.FindStringSubmatch(comment.Body); match != nil {
			intentID, err := strconv.ParseInt(match[1], 10, 64)
			if err == nil && intentID > 0 && comment.ID > intentID {
				blockedIntents[intentID] = preReviewTransitionMarker{commentID: comment.ID, head: match[2]}
			}
		}
		if match := preReviewResume.FindStringSubmatch(comment.Body); match != nil {
			intentID, err := strconv.ParseInt(match[1], 10, 64)
			if err == nil && intentID > 0 && comment.ID > intentID {
				resumedIntents[intentID] = preReviewTransitionMarker{commentID: comment.ID, head: match[2]}
			}
		}
	}
	var currentIntentID int64
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		if match := preReviewDone.FindStringSubmatch(comment.Body); match != nil {
			intentID, err := strconv.ParseInt(match[1], 10, 64)
			intent, ok := intents[intentID]
			intentTime, intentTimeErr := time.Parse(time.RFC3339, intentCreatedAt[intentID])
			doneTime, doneTimeErr := time.Parse(time.RFC3339, comment.CreatedAt)
			if err != nil || !ok || intentID <= 0 || comment.ID <= 0 || intentID >= comment.ID || intent.from != match[2] ||
				intent.base != match[4] || intent.identity != match[5] || canonicalIntent[intent] != intentID ||
				intentTimeErr != nil || doneTimeErr != nil || !doneTime.After(intentTime) {
				if match[3] == head {
					state.malformed = true
				}
				continue
			}
			doneIntents[intentID] = true
			if match[2] == head && match[3] != head {
				state.cycled = true
			}
			if match[3] == head {
				if len(headParents) != 2 || headParents[0] != match[2] || headParents[1] != match[4] ||
					state.validation != nil && (state.validation.Base != match[4] || currentIntentID != intentID) {
					state.malformed = true
					continue
				}
				if state.validation == nil {
					currentIntentID = intentID
					state.validation = &preReviewSyncValidation{
						IntentCommentID: intentID,
						DoneCommentID:   comment.ID,
						From:            match[2],
						To:              match[3],
						Base:            match[4],
						IdentitySHA256:  match[5],
						IntentCreatedAt: intentCreatedAt[intentID],
						DoneCreatedAt:   comment.CreatedAt,
					}
				}
			}
		}
	}
	if state.validation != nil {
		intent := intents[currentIntentID]
		if resumedIntents[currentIntentID].commentID != 0 && blockedIntents[currentIntentID].commentID == 0 {
			state.malformed = true
		}
		state.blocked, state.resumed = preReviewTransitionState(
			intent, head, headParents, blockedIntents[currentIntentID], resumedIntents[currentIntentID],
			state.validation.DoneCommentID,
		)
		if state.resumed {
			state.resumeCommentID = resumedIntents[currentIntentID].commentID
		}
	}
	for _, id := range canonicalOrder {
		intent := intents[id]
		if doneIntents[id] {
			continue
		}
		state.intentOpen = true
		state.openIdentity = intent.identity
		if resumedIntents[id].commentID != 0 && blockedIntents[id].commentID == 0 {
			state.malformed = true
		}
		blocked, resumed := preReviewTransitionState(
			intent, head, headParents, blockedIntents[id], resumedIntents[id], 0,
		)
		state.blocked = state.blocked || blocked
		state.resumed = resumed
		if resumed {
			state.resumeCommentID = resumedIntents[id].commentID
		}
		if state.validation != nil && intent.from == state.validation.From && intent.base == state.validation.Base {
			state.malformed = true
		}
		state.orphaned = head != intent.from &&
			(len(headParents) != 2 || headParents[0] != intent.from || headParents[1] != intent.base)
		break
	}
	return state
}

// preReviewTransitionState keeps the durable human handoff authoritative over
// a syntactically valid stale-worker done. A resume may name the original from
// immediately before the expected commit, or the exact current [from, base]
// merge after a crashed push. Full GraphQL provenance still proves the commit
// edge and absence of other events.
func preReviewTransitionState(intent preReviewIntentShape, head string, headParents []string,
	blocked, resumed preReviewTransitionMarker, doneCommentID int64,
) (isBlocked, isResumed bool) {
	if blocked.commentID == 0 {
		return false, false
	}
	if resumed.commentID <= blocked.commentID || doneCommentID > 0 && resumed.commentID >= doneCommentID {
		return true, false
	}
	resumeMatches := resumed.head == head
	if resumed.head == intent.from && len(headParents) == 2 &&
		headParents[0] == intent.from && headParents[1] == intent.base {
		resumeMatches = true
	}
	if !resumeMatches {
		return true, false
	}
	return false, true
}

type checkState int

const (
	checksUnknown checkState = iota
	checksPending
	checksReady
	checksFailed
)

func requiredChecksState(pr apiPull, required []string) checkState {
	if !pr.AdmissionKnown || pr.ChecksTruncated || len(required) == 0 {
		return checksUnknown
	}
	contexts := make(map[string][]apiCheckContext, len(pr.CheckContexts))
	for _, context := range pr.CheckContexts {
		contexts[context.Name] = append(contexts[context.Name], context)
	}
	pending := false
	failed := false
	for _, name := range required {
		observed := contexts[name]
		if len(observed) == 0 {
			pending = true
			continue
		}
		ready := false
		contextPending := false
		contextFailed := false
		for _, context := range observed {
			status := strings.ToUpper(context.Status)
			conclusion := strings.ToUpper(context.Conclusion)
			switch {
			case status == "SUCCESS" || status == "COMPLETED" && conclusion == "SUCCESS":
				ready = true
			case status == "PENDING" || status == "EXPECTED" || status == "QUEUED" || status == "IN_PROGRESS" || status == "WAITING" || status == "REQUESTED":
				contextPending = true
			case status == "COMPLETED" && conclusion == "":
				contextPending = true
			default:
				contextFailed = true
			}
		}
		if contextPending {
			pending = true
		} else if ready {
			continue
		} else if contextFailed {
			failed = true
		}
	}
	if pending {
		return checksPending
	}
	if failed {
		return checksFailed
	}
	return checksReady
}

func requiredChecksPresent(pr apiPull, required []string) int {
	set := make(map[string]bool, len(required))
	for _, name := range required {
		set[name] = true
	}
	present := map[string]bool{}
	for _, context := range pr.CheckContexts {
		if set[context.Name] {
			present[context.Name] = true
		}
	}
	return len(present)
}

func preReviewSyncAdmission(pr apiPull, required []string, doneForCurrentBase bool) bool {
	return pr.AdmissionKnown && !pr.ChecksTruncated && !doneForCurrentBase &&
		strings.EqualFold(pr.Mergeable, "CONFLICTING") &&
		strings.EqualFold(pr.MergeStateStatus, "DIRTY") &&
		requiredChecksPresent(pr, required) == 0
}

func preReviewAdmissionPending(pr apiPull, required []string) bool {
	if !pr.AdmissionKnown || requiredChecksPresent(pr, required) != 0 {
		return false
	}
	mergeableUnknown := pr.Mergeable == "" || strings.EqualFold(pr.Mergeable, "UNKNOWN")
	mergeStateUnknown := pr.MergeStateStatus == "" || strings.EqualFold(pr.MergeStateStatus, "UNKNOWN")
	conflictMismatch := strings.EqualFold(pr.Mergeable, "CONFLICTING") != strings.EqualFold(pr.MergeStateStatus, "DIRTY")
	return pr.ChecksTruncated || mergeableUnknown || mergeStateUnknown || conflictMismatch
}

func preReviewCINeedsAttention(pr apiPull, validation *preReviewSyncValidation, required []string, now time.Time) bool {
	if validation == nil || requiredChecksPresent(pr, required) >= len(required) {
		return false
	}
	doneAt, err := time.Parse(time.RFC3339, validation.DoneCreatedAt)
	return err == nil && now.Sub(doneAt) >= 30*time.Minute
}

func preReviewSourceBlocker(pr apiPull, repository string) string {
	if pr.Head.Repo == nil || pr.Head.Repo.FullName == "" || pr.Head.Ref == "" {
		return "head repository/ref отсутствует; безопасный pre-review-sync невозможен"
	}
	if !strings.EqualFold(pr.Head.Repo.FullName, repository) && !pr.MaintainerCanModify {
		return "fork не разрешает maintainer edits; автор должен включить разрешение или синхронизировать ветку сам"
	}
	return ""
}

func preReviewIdentityBlocker(pr apiPull, repository, expected string) string {
	if reason := preReviewSourceBlocker(pr, repository); reason != "" {
		return reason
	}
	actual, ok := preReviewIdentitySHA(pr)
	if !ok || actual != expected {
		return "head repository/ref либо maintainer permission изменились после pre-review-sync intent; требуется решение человека"
	}
	return ""
}

func preReviewIdentitySHA(pr apiPull) (string, bool) {
	if pr.Head.Repo == nil || pr.Head.Repo.FullName == "" || pr.Head.Ref == "" {
		return "", false
	}
	record := "pp-pre-review-sync-identity-v1\n" +
		"head-repository=" + pr.Head.Repo.FullName + "\n" +
		"head-ref=" + pr.Head.Ref + "\n" +
		"maintainer-can-modify=" + strconv.FormatBool(pr.MaintainerCanModify) + "\n"
	sum := sha256.Sum256([]byte(record))
	return fmt.Sprintf("%x", sum[:]), true
}

type baseSyncIntentShape struct {
	from, base, previous, shipEvent, createdAt string
}

type baseSyncDoneShape struct {
	intentID                      int64
	from, to, previous, shipEvent string
}

// baseSyncRESTState is deliberately only an operational hint. The mutation
// contracts still prove comment nodes, timeline edges and commit parents with
// two stable GraphQL snapshots before changing GitHub state.
func baseSyncRESTState(comments []apiComment, owner, head string, headParents []string) (doneCurrent, intentOpen, baseAdvanced, protocolHistory bool, integrationAt string) {
	intents := map[int64]baseSyncIntentShape{}
	doneIntents := map[int64]bool{}
	dones := map[int64]baseSyncDoneShape{}
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		if match := baseSyncIntent.FindStringSubmatch(comment.Body); match != nil {
			protocolHistory = true
			intents[comment.ID] = baseSyncIntentShape{from: match[1], base: match[2], previous: match[7], shipEvent: match[6], createdAt: comment.CreatedAt}
		}
		if match := baseSyncDone.FindStringSubmatch(comment.Body); match != nil {
			protocolHistory = true
			intentID, err := strconv.ParseInt(match[1], 10, 64)
			intent, ok := intents[intentID]
			if err != nil || !ok || intentID >= comment.ID || intent.from != match[2] ||
				intent.previous != match[5] || intent.shipEvent != match[6] {
				continue
			}
			doneIntents[intentID] = true
			dones[comment.ID] = baseSyncDoneShape{
				intentID: intentID, from: match[2], to: match[3],
				previous: match[5], shipEvent: match[6],
			}
			if match[3] == head {
				doneCurrent = true
				baseAdvanced = intent.base != match[4]
				startedAt := baseSyncIntegrationStart(intent, intents, dones)
				if integrationAt == "" || startedAt < integrationAt {
					integrationAt = startedAt
				}
			}
		}
	}
	for id, intent := range intents {
		if doneIntents[id] || !intentCanDescribeCurrentHead(intent, head, headParents) {
			continue
		}
		// A valid done for the current merge commit completes this exact
		// parent-to-head transition. Any other unmatched intent from the same
		// first parent is a parallel/stale duplicate, not a new recovery owner.
		if doneCurrent && len(headParents) == 2 && headParents[0] == intent.from {
			continue
		}
		intentOpen = true
		startedAt := baseSyncIntegrationStart(intent, intents, dones)
		if integrationAt == "" || startedAt < integrationAt {
			integrationAt = startedAt
		}
	}
	return doneCurrent, intentOpen, baseAdvanced, protocolHistory, integrationAt
}

// baseSyncIntegrationStart preserves ownership across a multi-hop carry chain.
// An updated PR may need another base-sync while it waits for merge. Its new
// intent points at the previous done comment; using only the new comment time
// would let a later PR overtake an already active single-flight owner.
func baseSyncIntegrationStart(intent baseSyncIntentShape, intents map[int64]baseSyncIntentShape, dones map[int64]baseSyncDoneShape) string {
	startedAt := intent.createdAt
	seen := map[int64]bool{}
	current := intent
	for current.previous != "none" {
		doneID, err := strconv.ParseInt(current.previous, 10, 64)
		if err != nil || seen[doneID] {
			break
		}
		seen[doneID] = true
		done, ok := dones[doneID]
		previous, previousOK := intents[done.intentID]
		if !ok || !previousOK || done.to != current.from ||
			done.from != previous.from || done.previous != previous.previous ||
			done.shipEvent != current.shipEvent || previous.shipEvent != current.shipEvent {
			break
		}
		if previous.createdAt < startedAt {
			startedAt = previous.createdAt
		}
		current = previous
	}
	return startedAt
}

// intentCanDescribeCurrentHead limits recovery to a transaction that can still
// be completed without rewriting history: update-branch has either not moved
// the head yet, or it produced the current two-parent merge from intent.from.
// Intents from older heads remain audit history but must not own single-flight.
func intentCanDescribeCurrentHead(intent baseSyncIntentShape, head string, headParents []string) bool {
	return intent.from == head || (len(headParents) == 2 && headParents[0] == intent.from)
}

func duplicateCompletionEpoch(comments []apiComment, owner, head string) bool {
	ids := map[int64]bool{}
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		if reviewAgain.MatchString(comment.Body) {
			ids = map[int64]bool{}
		}
		for _, match := range completionLine.FindAllStringSubmatch(comment.Body, -1) {
			if match[1] != head {
				continue
			}
			id, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil {
				continue
			}
			ids[id] = true
			if len(ids) > 1 {
				return true
			}
		}
	}
	return false
}

func sortCandidates(items []candidate) {
	sort.Slice(items, func(i, j int) bool {
		if candidatePriority(items[i].Stage) != candidatePriority(items[j].Stage) {
			return candidatePriority(items[i].Stage) < candidatePriority(items[j].Stage)
		}
		if candidatePriority(items[i].Stage) <= 1 {
			return items[i].Number < items[j].Number
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		if items[i].Depth == items[j].Depth {
			return items[i].Number < items[j].Number
		}
		return items[i].Depth < items[j].Depth
	})
}

func sortMergeCandidates(items []candidate) {
	sort.Slice(items, func(i, j int) bool {
		if candidatePriority(items[i].Stage) != candidatePriority(items[j].Stage) {
			return candidatePriority(items[i].Stage) < candidatePriority(items[j].Stage)
		}
		if candidatePriority(items[i].Stage) <= 1 {
			return items[i].Number < items[j].Number
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].Number < items[j].Number
	})
}

func sortFixCandidates(items []candidate) {
	sort.Slice(items, func(i, j int) bool {
		left, right := fixStagePriority(items[i].Stage), fixStagePriority(items[j].Stage)
		if left != right {
			return left < right
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].Number < items[j].Number
	})
}

func fixStagePriority(stage string) int {
	switch stage {
	case "pre-review-sync-recovery":
		return 0
	case "fix-review":
		return 1
	case "pre-review-sync":
		return 2
	case "fix-issue":
		return 3
	default:
		return 4
	}
}

func queuePriority(labels map[string]bool, createdAt string, now time.Time) (int, string) {
	base, source := 2, "auto:default"
	for priority := 0; priority <= 3; priority++ {
		if labels[fmt.Sprintf("queue:p%d", priority)] {
			base, source = priority, fmt.Sprintf("manual:queue:p%d", priority)
			goto aging
		}
	}
	for priority := 0; priority <= 3; priority++ {
		if labels[fmt.Sprintf("queue:auto:p%d", priority)] {
			base, source = priority, fmt.Sprintf("auto:queue:auto:p%d", priority)
			goto aging
		}
	}
	switch {
	case labels["security"] || labels["severity:critical"] || labels["blocker"] || labels["data-loss"]:
		base, source = 0, "auto:critical-label"
	case labels["bug"]:
		base, source = 1, "auto:bug"
	case labels["enhancement"] || labels["documentation"]:
		base, source = 2, "auto:planned-change"
	case labels["question"]:
		base, source = 3, "auto:question"
	}

aging:
	if created, err := time.Parse(time.RFC3339, createdAt); err == nil && now.After(created) {
		boost := int(now.Sub(created) / (7 * 24 * time.Hour))
		maxBoost := base - 1
		if maxBoost < 0 {
			maxBoost = 0
		}
		if boost > maxBoost {
			boost = maxBoost
		}
		if boost > 0 {
			source += fmt.Sprintf("+aging:%d", boost)
			base -= boost
		}
	}
	return base, source
}

func candidatePriority(stage string) int {
	switch stage {
	case "integration-merge-recovery", "integration-merge-ready", "legacy-integration-merge-ready":
		return 0
	case "integration-review", "legacy-integration-review":
		return 1
	default:
		return 2
	}
}

func applySingleFlight(result *report) {
	if len(result.ReviewCandidates) == 0 {
		result.ReviewCandidates = append([]candidate{}, result.ContentReviewCandidates...)
		return
	}
	// Stage priority decides which worker can act, but it must not replace an
	// already visible owner. The earliest base-sync intent owns the lane across
	// review/merge transitions; legacy chains without an intent use PR number.
	owner := result.ReviewCandidates[0]
	for _, item := range result.ReviewCandidates[1:] {
		if integrationOwnerLess(item, owner) {
			owner = item
		}
	}
	result.IntegrationOwner = &owner
	deferredIntegration := len(result.ReviewCandidates) - 1
	if candidatePriority(owner.Stage) == 0 {
		result.ReviewCandidates = append([]candidate{}, result.ContentReviewCandidates...)
		result.add("yellow", "single_flight_barrier", owner.Number,
			fmt.Sprintf("владелец интеграционной полосы ждёт MERGE; содержательное REVIEW остаётся открытым (%d кандидатов), следующих интеграционных отложено: %d", len(result.ContentReviewCandidates), deferredIntegration))
		return
	}
	result.ReviewCandidates = []candidate{owner}
	result.add("yellow", "single_flight_barrier", owner.Number,
		fmt.Sprintf("владелец интеграционной полосы; REVIEW проверяет только интеграционную дельту этого PR, содержательных кандидатов отложено: %d, следующих интеграционных: %d", len(result.ContentReviewCandidates), deferredIntegration))
}

func integrationOwnerLess(left, right candidate) bool {
	if (left.IntegrationAt != "") != (right.IntegrationAt != "") {
		return left.IntegrationAt != ""
	}
	if left.IntegrationAt != "" && right.IntegrationAt != "" && left.IntegrationAt != right.IntegrationAt {
		return left.IntegrationAt < right.IntegrationAt
	}
	return left.Number < right.Number
}

func setMergeExecutable(result *report) {
	if result.IntegrationOwner == nil {
		result.MergeExecutable = append([]candidate{}, result.MergeCandidates...)
		return
	}
	if candidatePriority(result.IntegrationOwner.Stage) != 0 {
		return
	}
	for _, item := range result.MergeCandidates {
		if item.Number == result.IntegrationOwner.Number {
			result.MergeExecutable = []candidate{item}
			return
		}
	}
}

func printReport(writer io.Writer, result report) {
	_, _ = fmt.Fprintf(writer, "pipeline: %s — %s\n", result.State, result.Summary)
	for _, item := range result.Findings {
		target := ""
		if item.PR != 0 {
			target = fmt.Sprintf(" PR #%d", item.PR)
		} else if item.Issue != 0 {
			target = fmt.Sprintf(" issue #%d", item.Issue)
		}
		_, _ = fmt.Fprintf(writer, "- %s %s:%s %s\n", item.Severity, item.Code, target, item.Message)
	}
}
