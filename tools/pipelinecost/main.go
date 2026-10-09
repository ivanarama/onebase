// pipelinecost builds a read-only monthly maintenance report. A threshold is
// a discussion signal, never a CI gate or permission to change the protocol.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

type pull struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	MergedAt  time.Time `json:"merged_at"`
	Base      struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	ChangedFiles int    `json:"changed_files"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Files        []file `json:"files"`
}
type file struct {
	Name     string `json:"filename"`
	Previous string `json:"previous_filename,omitempty"`
}
type snapshot struct {
	Version     int       `json:"version"`
	Repository  string    `json:"repository"`
	Month       string    `json:"month"`
	CollectedAt time.Time `json:"collected_at"`
	Pulls       []pull    `json:"pulls"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pipelinecost:", err)
		os.Exit(1)
	}
}

func run(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("pipelinecost", flag.ContinueOnError)
	fs.SetOutput(errOut)
	month := fs.String("month", "", "месяц UTC, YYYY-MM (обязательно)")
	repo := fs.String("repo", "ivanarama/onebase", "owner/repository")
	input := fs.String("input", "", "повторить отчёт из сохранённого JSON без GitHub")
	save := fs.String("snapshot", "", "сохранить входной JSON для повторения отчёта")
	asJSON := fs.Bool("json", false, "отчёт в JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("неожиданные позиционные аргументы")
	}
	start, err := time.Parse("2006-01", *month)
	if err != nil {
		return errors.New("-month должен иметь формат YYYY-MM")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(*repo) {
		return errors.New("-repo должен иметь формат owner/repository")
	}
	end := start.AddDate(0, 1, 0)
	var s snapshot
	if *input != "" {
		data, err := os.ReadFile(*input)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
	} else {
		if end.After(time.Now().UTC()) {
			return errors.New("живой отчёт доступен только за завершённый месяц UTC")
		}
		s, err = collect(*repo, *month, start, end)
		if err != nil {
			return err
		}
	}
	if s.Version != 1 || s.Repository != *repo || s.Month != *month || s.CollectedAt.Before(end) || s.Pulls == nil {
		return errors.New("неполный snapshot или другой месяц/repository/version")
	}
	seen := map[int]bool{}
	for _, p := range s.Pulls {
		if p.Number <= 0 || seen[p.Number] || p.Base.Ref != "main" || p.MergedAt.Before(start) || !p.MergedAt.Before(end) || p.CreatedAt.IsZero() || p.CreatedAt.After(p.MergedAt) || p.ChangedFiles <= 0 || len(p.Files) != p.ChangedFiles || p.Additions < 0 || p.Deletions < 0 {
			return fmt.Errorf("неполные или несовместимые данные PR #%d", p.Number)
		}
		seen[p.Number] = true
		names := map[string]bool{}
		for _, f := range p.Files {
			if f.Name == "" || names[f.Name] {
				return fmt.Errorf("неполный список файлов PR #%d", p.Number)
			}
			names[f.Name] = true
		}
	}
	sort.Slice(s.Pulls, func(i, j int) bool { return s.Pulls[i].Number < s.Pulls[j].Number })
	if *save != "" {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*save, append(data, '\n'), 0600); err != nil {
			return err
		}
	}
	r := summarize(s)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	if _, err := fmt.Fprintf(out, "Месяц UTC: %s; repository: %s; snapshot: %s\n", s.Month, s.Repository, s.CollectedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Категория\tPR\t+строк\t-строк"); err != nil {
		return err
	}
	for _, name := range []string{"pipeline", "mixed", "plan", "other"} {
		b := r.Buckets[name]
		if _, err := fmt.Fprintf(out, "%s\t%d\t%d\t%d\n", name, b.Count, b.Additions, b.Deletions); err != nil {
			return err
		}
	}
	if r.NonPlan == 0 {
		if _, err := fmt.Fprintln(out, "Доля конвейера без plan-PR: нет данных (знаменатель 0)."); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "Доля конвейера без plan-PR: %d/%d = %.1f%%; порог обсуждения >=25%%: %t\n", r.Pipeline, r.NonPlan, *r.SharePercent, r.Discuss); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out, "Номер\tКатегория\tОснование\tОт создания до merge, ч\tURL"); err != nil {
		return err
	}
	for _, item := range r.Items {
		if _, err := fmt.Fprintf(out, "#%d\t%s\t%s\t%.1f\t%s\n", item.Number, item.Category, item.Basis, item.LeadHours, item.URL); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out, "Деньги, время человека, аварии и очередь ship: нет данных; дополнить по журналам. Порог не блокирует CI/merge."); err != nil {
		return err
	}
	return nil
}

func ghJSON(endpoint string, target any) error {
	// G204: fixed executable and separate API argument; repo/number are validated, no shell.
	cmd := exec.Command("gh", "api", endpoint) //nolint:gosec
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh api: %w: %s", err, stderr.String())
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("gh JSON: %w", err)
	}
	return nil
}

func collect(repo, month string, start, end time.Time) (snapshot, error) {
	s := snapshot{Version: 1, Repository: repo, Month: month, CollectedAt: time.Now().UTC(), Pulls: []pull{}}
	query := fmt.Sprintf("repo:%s is:pr is:merged base:main merged:%s..%s", repo, start.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02"))
	numbers := []int{}
	total := -1
	seen := map[int]bool{}
	for page := 1; ; page++ {
		var result struct {
			Total      *int  `json:"total_count"`
			Incomplete *bool `json:"incomplete_results"`
			Items      []struct {
				Number int `json:"number"`
			} `json:"items"`
		}
		endpoint := fmt.Sprintf("search/issues?q=%s&per_page=100&page=%d&sort=created&order=asc", url.QueryEscape(query), page)
		if err := ghJSON(endpoint, &result); err != nil {
			return s, err
		}
		if result.Total == nil || result.Incomplete == nil || *result.Incomplete || *result.Total < 0 || *result.Total > 1000 || result.Items == nil {
			return s, errors.New("поиск GitHub неполон или превышает 1000 PR; отчёт не построен")
		}
		if total < 0 {
			total = *result.Total
		}
		if total != *result.Total {
			return s, errors.New("выдача поиска изменилась во время прогона")
		}
		for _, item := range result.Items {
			if item.Number <= 0 || seen[item.Number] {
				return s, errors.New("повтор или неверный номер в поиске")
			}
			seen[item.Number] = true
			numbers = append(numbers, item.Number)
		}
		if len(numbers) == total {
			break
		}
		if len(result.Items) < 100 || len(numbers) > total {
			return s, errors.New("поиск GitHub обрезан")
		}
	}
	for _, n := range numbers {
		var p pull
		endpoint := fmt.Sprintf("repos/%s/pulls/%d", repo, n)
		if err := ghJSON(endpoint, &p); err != nil {
			return s, err
		}
		if p.Number != n {
			return s, errors.New("GitHub вернул другой номер PR")
		}
		// REST lists at most 3000 changed files. Refuse a truncated classification.
		if p.ChangedFiles > 3000 {
			return s, fmt.Errorf("PR #%d: более 3000 файлов", n)
		}
		for page := 1; ; page++ {
			var files []file
			if err := ghJSON(fmt.Sprintf("%s/files?per_page=100&page=%d", endpoint, page), &files); err != nil {
				return s, err
			}
			p.Files = append(p.Files, files...)
			if len(files) < 100 || len(p.Files) >= p.ChangedFiles {
				break
			}
		}
		s.Pulls = append(s.Pulls, p)
	}
	return s, nil
}

type bucket struct {
	Count     int `json:"count"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}
type item struct {
	Number    int     `json:"number"`
	Category  string  `json:"category"`
	Basis     string  `json:"basis"`
	LeadHours float64 `json:"lead_hours"`
	URL       string  `json:"url"`
}
type report struct {
	Month        string            `json:"month"`
	Repository   string            `json:"repository"`
	CollectedAt  time.Time         `json:"collected_at"`
	Buckets      map[string]bucket `json:"buckets"`
	Total        int               `json:"total"`
	NonPlan      int               `json:"non_plan"`
	Pipeline     int               `json:"pipeline_including_mixed"`
	SharePercent *float64          `json:"share_percent"`
	Discuss      bool              `json:"discussion_signal"`
	Items        []item            `json:"items"`
}

func summarize(s snapshot) report {
	r := report{Month: s.Month, Repository: s.Repository, CollectedAt: s.CollectedAt, Buckets: map[string]bucket{}, Total: len(s.Pulls), Items: []item{}}
	for _, name := range []string{"pipeline", "mixed", "plan", "other"} {
		r.Buckets[name] = bucket{}
	}
	for _, p := range s.Pulls {
		category, basis := classify(p)
		b := r.Buckets[category]
		b.Count++
		b.Additions += p.Additions
		b.Deletions += p.Deletions
		r.Buckets[category] = b
		r.Items = append(r.Items, item{p.Number, category, basis, p.MergedAt.Sub(p.CreatedAt).Hours(), p.URL})
	}
	r.NonPlan = r.Total - r.Buckets["plan"].Count
	r.Pipeline = r.Buckets["pipeline"].Count + r.Buckets["mixed"].Count
	if r.NonPlan > 0 {
		share := 100 * float64(r.Pipeline) / float64(r.NonPlan)
		r.SharePercent = &share
		r.Discuss = share >= 25
	}
	return r
}

func classify(p pull) (string, string) {
	allPlan, hasPipeline, hasOther := true, false, false
	for _, f := range p.Files {
		for _, path := range []string{f.Name, f.Previous} {
			if path == "" {
				continue
			}
			if strings.HasPrefix(path, "Plans/") {
				continue
			}
			allPlan = false
			if pipelinePath(path) {
				hasPipeline = true
			} else {
				hasOther = true
			}
		}
	}
	if allPlan {
		return "plan", "Plans/ only"
	}
	for _, l := range p.Labels {
		if l.Name == "area:pipeline" {
			return "pipeline", "area:pipeline"
		}
	}
	if hasPipeline && hasOther {
		return "mixed", "files (check scope)"
	}
	if hasPipeline {
		return "pipeline", "files"
	}
	return "other", "files (check scope)"
}

func pipelinePath(path string) bool {
	for _, exact := range []string{"pipelinectl.json", "docs/maintenance-pipeline.md"} {
		if path == exact {
			return true
		}
	}
	for _, prefix := range []string{"tools/pipelinehealth/", "tools/pipelinecost/", "tools/issuetail/", "tools/backlogsweep/", "internal/pipelinecontract/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	for _, stage := range []string{"triage-issues", "plan-approved", "fix-approved", "review-queue", "merge-shepherd", "tail-issues", "discussions-watch"} {
		if strings.HasPrefix(path, ".claude/skills/"+stage+"/") || strings.HasPrefix(path, ".agents/skills/"+stage+"/") {
			return true
		}
	}
	return false
}
