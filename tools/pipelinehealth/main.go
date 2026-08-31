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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	completionLine = regexp.MustCompile(`(?m)^<!-- pp:head-reviewed ([0-9a-f]{40}) review-comment=([0-9]+) claim=([0-9]+) epoch-sha256=([0-9a-f]{64}) -->$`)
	claimLine      = regexp.MustCompile(`(?m)^<!-- pp:review-claim ([0-9a-f]{40}) review-comment=([0-9]+) epoch-sha256=([0-9a-f]{64}) -->$`)
	reviewAgain    = regexp.MustCompile(`(?m)^pp:review-again$`)
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
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	Head    struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels   []apiLabel   `json:"labels"`
	Comments []apiComment `json:"-"`
}

type candidate struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Head   string `json:"head"`
	Depth  int    `json:"review_depth"`
}

type finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	PR       int    `json:"pr,omitempty"`
	Message  string `json:"message"`
}

type report struct {
	State            string      `json:"state"`
	Summary          string      `json:"summary"`
	Scope            string      `json:"scope"`
	Scheduler        string      `json:"scheduler"`
	Checked          int         `json:"checked"`
	ReviewCandidates []candidate `json:"review_candidates"`
	FixCandidates    []candidate `json:"fix_candidates"`
	HumanWaiting     []candidate `json:"human_waiting"`
	Findings         []finding   `json:"findings"`
}

func main() {
	repo := flag.String("repo", "ivanarama/onebase", "GitHub repository")
	owner := flag.String("owner", "ivanarama", "trusted pipeline account")
	contract := flag.String("contract", ".claude/skills/review-queue/SKILL.md", "active REVIEW contract")
	fixture := flag.String("prs", "", "read a JSON fixture instead of GitHub")
	asJSON := flag.Bool("json", false, "print machine-readable JSON")
	flag.Parse()

	prs, err := loadPulls(*repo, *fixture)
	if err != nil {
		fail(err)
	}
	result := analyze(prs, *owner)
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

func ghJSONLines(gh string, destination any, args ...string) error {
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
		Scheduler: "review-depth-then-number", Checked: len(prs),
		ReviewCandidates: []candidate{}, FixCandidates: []candidate{},
		HumanWaiting: []candidate{}, Findings: []finding{},
	}
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
		item := candidate{Number: pr.Number, Title: pr.Title, URL: pr.HTMLURL, Head: pr.Head.SHA, Depth: depth}
		currentCompletions, latestCompletion, latestOverride := currentProtocolState(pr.Comments, owner, pr.Head.SHA)

		if labels["changes-requested"] && labels["needs-decision"] {
			result.add("yellow", "route_transition_open", pr.Number,
				"одновременно стоят changes-requested и needs-decision; допустимо только во время handoff")
		}
		if labels["ship"] && (labels["changes-requested"] || labels["needs-decision"]) {
			result.add("red", "ship_with_blocking_route", pr.Number,
				"ship конфликтует с блокирующим маршрутом")
		}
		if duplicateCompletionEpoch(pr.Comments, owner, pr.Head.SHA) {
			result.add("red", "same_head_reviewed_twice", pr.Number,
				"у текущего HEAD два разных committed-review без разделяющего pp:review-again")
		}
		if currentCompletions == 0 && currentClaimCount(pr.Comments, owner, pr.Head.SHA) > 0 {
			result.add("yellow", "unfinished_review_transaction", pr.Number,
				"на текущем HEAD есть claim без committed completion; нужен recovery")
		}

		if pr.Draft || labels["ship"] || labels["hold"] {
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
		case currentCompletions > 0 && !overrideOpen:
			result.add("yellow", "review_without_route", pr.Number,
				"committed-review текущего HEAD есть, но маршрутная метка отсутствует")
			result.HumanWaiting = append(result.HumanWaiting, item)
		default:
			result.ReviewCandidates = append(result.ReviewCandidates, item)
		}
	}
	sortCandidates(result.ReviewCandidates)
	sortCandidates(result.FixCandidates)
	sortCandidates(result.HumanWaiting)
	return result
}

func checkContract(result *report, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		result.add("red", "contract_unreadable", 0, fmt.Sprintf("не удалось прочитать активный REVIEW contract: %v", err))
		return
	}
	text := string(data)
	for _, required := range []string{"(review-depth ASC, number ASC)", "Не сортируй очередь только по номеру PR"} {
		if !strings.Contains(text, required) {
			result.add("red", "unfair_review_contract", 0,
				"активный REVIEW contract не гарантирует breadth-first порядок")
			return
		}
	}
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
	result.Summary = fmt.Sprintf(
		"PR: %d; REVIEW: %d (следующие %s); FIX: %d; человек: %d; сигналов: %d",
		result.Checked, len(result.ReviewCandidates), next, len(result.FixCandidates),
		len(result.HumanWaiting), len(result.Findings))
}

func (result *report) add(severity, code string, pr int, message string) {
	result.Findings = append(result.Findings, finding{Severity: severity, Code: code, PR: pr, Message: message})
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
		if items[i].Depth == items[j].Depth {
			return items[i].Number < items[j].Number
		}
		return items[i].Depth < items[j].Depth
	})
}

func printReport(writer io.Writer, result report) {
	_, _ = fmt.Fprintf(writer, "pipeline: %s — %s\n", result.State, result.Summary)
	for _, item := range result.Findings {
		pr := ""
		if item.PR != 0 {
			pr = fmt.Sprintf(" PR #%d", item.PR)
		}
		_, _ = fmt.Fprintf(writer, "- %s %s:%s %s\n", item.Severity, item.Code, pr, item.Message)
	}
}
