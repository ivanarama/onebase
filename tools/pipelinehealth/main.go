// pipelinehealth provides a fast, read-only operational check of the
// maintenance queue. It deliberately does not replace the mutation-time
// GraphQL proof in the pipeline skills.
package main

import (
	"bufio"
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
	reviewAgain       = regexp.MustCompile(`(?m)^pp:review-again$`)
	displayRepair     = regexp.MustCompile(`(?m)^<!-- pp:display-repair comment=([0-9]+) -->$`)
	baseSyncIntent    = regexp.MustCompile(`(?m)^<!-- pp:base-sync-intent from=([0-9a-f]{40}) base=([0-9a-f]{40}) review-comment=([0-9]+) claim=([0-9]+) completion=([0-9]+) ship-event=([A-Za-z0-9_=-]+) previous=([0-9]+|none) -->$`)
	baseSyncDone      = regexp.MustCompile(`(?m)^<!-- pp:base-sync-done intent=([0-9]+) from=([0-9a-f]{40}) to=([0-9a-f]{40}) base=([0-9a-f]{40}) previous=([0-9]+|none) ship-event=([A-Za-z0-9_=-]+) -->$`)
	triageRouteClaim  = regexp.MustCompile(`(?m)^<!-- pp:triage-route-claim fingerprint-sha256=([0-9a-f]{64}) owner=[0-9a-fA-F-]{36} -->$`)
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
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels   []apiLabel   `json:"labels"`
	Comments []apiComment `json:"-"`
	// HeadParents holds the parent SHAs of the head commit. An automatic
	// base-sync always leaves a merge commit; an ordinary FIX push leaves a
	// single-parent commit. Without this the integration lane cannot be told
	// apart from a normal review round.
	HeadParents []string `json:"head_parents,omitempty"`
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
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	Head           string `json:"head"`
	Depth          int    `json:"review_depth"`
	Stage          string `json:"stage"`
	Priority       int    `json:"priority"`
	PrioritySource string `json:"priority_source"`
	UpdatedAt      string `json:"updated_at"`
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
	HumanWaiting            []candidate `json:"human_waiting"`
	Findings                []finding   `json:"findings"`
}

func main() {
	repo := flag.String("repo", "ivanarama/onebase", "GitHub repository")
	owner := flag.String("owner", "ivanarama", "trusted pipeline account")
	contract := flag.String("contract", ".claude/skills/review-queue/SKILL.md", "active REVIEW contract")
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
	result := analyze(prs, *owner)
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
	return prs, nil
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
	result := report{
		State: "green", Scope: "fast REST snapshot; mutation gates remain GraphQL",
		Scheduler: "two-lane-safety-priority-aging-depth-number", Checked: len(prs),
		ReviewCandidates: []candidate{}, ContentReviewCandidates: []candidate{},
		ReviewBacklog: []candidate{}, ReviewedWaitingShip: []candidate{}, MergeCandidates: []candidate{}, MergeExecutable: []candidate{}, PlanCandidates: []candidate{}, FixCandidates: []candidate{},
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
		carryDone, carryIntentOpen, baseAdvanced, protocolHistory := baseSyncRESTState(pr.Comments, owner, pr.Head.SHA)
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

		if pr.Draft || labels["hold"] {
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
			}
			continue
		}
		overrideOpen := latestOverride > latestCompletion
		switch {
		case labels["needs-decision"] && !overrideOpen:
			result.HumanWaiting = append(result.HumanWaiting, item)
		case labels["changes-requested"] && !overrideOpen:
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
	sortCandidates(result.MergeCandidates)
	sortCandidates(result.MergeExecutable)
	sortCandidates(result.FixCandidates)
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
		priority, prioritySource := queuePriority(labels, issue.CreatedAt, now)
		item := candidate{
			Number: issue.Number, Title: issue.Title, URL: issue.HTMLURL,
			Stage: "fix-issue", Priority: priority, PrioritySource: prioritySource,
			UpdatedAt: issue.UpdatedAt,
		}
		if labels["hold"] || labels["manual"] {
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
			ready, reason := triageHandoffReady(issue, owner)
			if !ready {
				result.addIssue("yellow", "fix_issue_not_executable", issue.Number, reason)
				continue
			}
			result.FixCandidates = append(result.FixCandidates, item)
		case labels["needs-decision"]:
			item.Stage = "human-decision"
			result.HumanWaiting = append(result.HumanWaiting, item)
		}
	}
	sortCandidates(result.PlanCandidates)
	sortCandidates(result.FixCandidates)
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

func triageHandoffReady(issue apiIssue, owner string) (bool, string) {
	var root *apiComment
	for index := range issue.Thread {
		comment := &issue.Thread[index]
		if !trustedUnedited(*comment, owner) || !hasExactLine(comment.Body, "<!-- pp:triage -->") {
			continue
		}
		if root == nil || comment.CreatedAt < root.CreatedAt ||
			(comment.CreatedAt == root.CreatedAt && comment.ID < root.ID) {
			root = comment
		}
	}
	if root == nil {
		return false, "eligible FIX issue has no canonical trusted triage"
	}
	if !strings.Contains(root.Body, "pp:triage-route-claim") {
		return true, ""
	}
	claims := triageRouteClaim.FindAllStringSubmatch(root.Body, -1)
	if len(claims) != 1 {
		return false, "canonical triage has a malformed route claim"
	}
	fingerprint := claims[0][1]
	claimID := strconv.FormatInt(root.ID, 10)
	labelsCommitted, replyCommitted, done := false, false, false
	replyRequired := hasExactLine(root.Body, "reply=required")
	for _, comment := range issue.Thread {
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
		return false, "TRIAGE route claim is unfinished; FIX must wait for matching labels/reply/done markers"
	}
	return true, ""
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
		"Для обычного аудита он обязан входить в `content_review_candidates`",
	} {
		if !strings.Contains(text, required) {
			result.add("red", "unfair_review_contract", 0,
				"активный REVIEW contract не гарантирует breadth-first порядок")
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
		"PR: %d; issues: %d; REVIEW исполняемо: %d (следующие %s); всего ждут REVIEW: %d; содержательное: %d; интеграционный владелец: %s; ждут ship: %d; MERGE исполняемо: %d; всего MERGE: %d; PLAN: %d; FIX: %d; человек: %d; сигналов: %d",
		result.Checked, result.IssuesChecked, len(result.ReviewCandidates), next,
		len(result.ReviewBacklog), len(result.ContentReviewCandidates), owner, len(result.ReviewedWaitingShip), len(result.MergeExecutable), len(result.MergeCandidates), len(result.PlanCandidates), len(result.FixCandidates),
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
	return labelSet(pr.Labels)["ship"]
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

type baseSyncIntentShape struct {
	from, base, previous, shipEvent string
}

// baseSyncRESTState is deliberately only an operational hint. The mutation
// contracts still prove comment nodes, timeline edges and commit parents with
// two stable GraphQL snapshots before changing GitHub state.
func baseSyncRESTState(comments []apiComment, owner, head string) (doneCurrent, intentOpen, baseAdvanced, protocolHistory bool) {
	intents := map[int64]baseSyncIntentShape{}
	doneIntents := map[int64]bool{}
	for _, comment := range comments {
		if !trustedUnedited(comment, owner) {
			continue
		}
		if match := baseSyncIntent.FindStringSubmatch(comment.Body); match != nil {
			protocolHistory = true
			intents[comment.ID] = baseSyncIntentShape{from: match[1], base: match[2], previous: match[7], shipEvent: match[6]}
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
			if match[3] == head {
				doneCurrent = true
				baseAdvanced = intent.base != match[4]
			}
		}
	}
	for id := range intents {
		if !doneIntents[id] {
			intentOpen = true
			break
		}
	}
	return doneCurrent, intentOpen, baseAdvanced, protocolHistory
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
	owner := result.ReviewCandidates[0]
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
