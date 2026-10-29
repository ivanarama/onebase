package main

import (
	"encoding/json"
	"fmt"
	"github.com/ivantit66/onebase/internal/nextslice"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type slicePullEvidence struct {
	Complete     bool                `json:"complete"`
	Merged       bool                `json:"merged"`
	MergeSHA     string              `json:"merge_sha"`
	MergeEvents  int                 `json:"merge_events"`
	Declarations []nextslice.Comment `json:"declarations"`
}

var (
	slicePlanReady    = regexp.MustCompile(`(?m)^<!-- pp:plan-ready issue=([1-9][0-9]*) pr=[1-9][0-9]* path=(Plans/[0-9]+-[a-z0-9][a-z0-9-]*\.md) -->$`)
	sliceLegacyActive = regexp.MustCompile(`^Взято в работу: #([1-9][0-9]*)\. <!-- pp:in-work -->\n?$`)
)

// sliceSource only trusts exact, unedited owner records. A legacy active marker
// is a lookup hint, never authorization to continue a slice.
func sliceSource(issue apiIssue, owner string) (int, string, bool) {
	number := 0
	path := ""
	protocol := false
	for _, c := range issue.Thread {
		if !trustedUnedited(c, owner) {
			continue
		}
		if m := slicePlanReady.FindStringSubmatch(c.Body); m != nil && m[1] == strconv.Itoa(issue.Number) {
			path = m[2]
		}
		if m := sliceLegacyActive.FindStringSubmatch(c.Body); m != nil {
			number, _ = strconv.Atoi(m[1])
		}
		if strings.Contains(c.Body, "pp:next-slice-") || strings.HasPrefix(c.Body, "pp-next-slice-v1") {
			protocol = true
			if root, err := nextslice.ParseRoot(c.Body); err == nil && root.Issue == strconv.Itoa(issue.Number) {
				number, _ = strconv.Atoi(root.PR)
			}
		}
	}
	return number, path, protocol
}
func inspectNextSlice(result *report, issue apiIssue, prs []apiPull, owner, planDir string, item candidate) bool {
	labels := labelSet(issue.Labels)
	number, path, protocol := sliceSource(issue, owner)
	if (labels["hold"] || labels["manual"]) && number == 0 && !protocol {
		return false
	}
	if !labels["in-work"] && !protocol {
		return false
	}
	if len(openPullsReferencingIssue(issue.Number, prs)) > 0 {
		return false
	}
	state := nextslice.Result{State: "human", Reason: "in-work without open PR: immutable active/plan evidence missing"}
	var pull *apiPull
	for i := range prs {
		if prs[i].Number == number {
			pull = &prs[i]
			break
		}
	}
	if path != "" && pull != nil && pull.SliceEvidence != nil {
		root, err := os.OpenRoot(planDir)
		if err == nil {
			data, readErr := root.ReadFile(strings.TrimPrefix(path, "Plans/"))
			_ = root.Close()
			if readErr == nil {
				manifest, parseErr := nextslice.ParseManifest(path, data)
				if parseErr == nil {
					evidence := pull.SliceEvidence
					observation := nextslice.Observation{Issue: strconv.Itoa(issue.Number), PR: strconv.Itoa(pull.Number), Head: pull.Head.SHA, Merge: evidence.MergeSHA, Body: pull.Body, Previous: "none", Complete: evidence.Complete && issue.CommentCount <= len(issue.Thread), Open: pull.State == "open", Merged: evidence.Merged, MergeEvents: evidence.MergeEvents, Held: labels["hold"] || labels["manual"], Declarations: evidence.Declarations}
					for _, c := range issue.Thread {
						observation.Comments = append(observation.Comments, nextslice.Comment{ID: strconv.FormatInt(c.ID, 10), Author: c.User.Login, CreatedAt: c.CreatedAt, Edited: c.LastEditedAt != nil || c.CreatedAt != c.UpdatedAt, Body: c.Body})
					}
					// The bootstrap does not derive mutation authority or a decision digest
					// from editable issue text. A modern chain without that proof fails closed.
					partial := regexp.MustCompile(`(?im)^Part of #` + strconv.Itoa(issue.Number) + `$`).MatchString(pull.Body)
					if len(evidence.Declarations) > 0 || partial {
						state = nextslice.Reduce(manifest, observation, owner, time.Now().UTC())
					} else {
						state.Reason = "merged PR has no immutable slice declaration or explicit partial intent"
					}
				} else {
					state.Reason = "invalid multi-slice manifest: " + parseErr.Error()
				}
			} else {
				state.Reason = "selected plan unavailable: " + readErr.Error()
			}
		} else {
			state.Reason = "plan directory unavailable: " + err.Error()
		}
	}
	item.Stage = state.State
	if state.State == "active" {
		return true
	}
	if state.State == "recovery" {
		result.NextSliceRecovery = append(result.NextSliceRecovery, item)
	} else {
		result.NextSliceHumanWaiting = append(result.NextSliceHumanWaiting, item)
		result.HumanWaiting = append(result.HumanWaiting, item)
	}
	result.addIssue("yellow", "next_slice_"+strings.ReplaceAll(state.State, "-", "_"), issue.Number, state.Reason+"; observation only, next-slice mutations disabled")
	return true
}

const nextSlicePullQuery = `query($owner:String!,$name:String!,$number:Int!,$cursor:String){repository(owner:$owner,name:$name){pullRequest(number:$number){number title body url state headRefOid merged mergeCommit{oid}comments(first:100,after:$cursor){totalCount nodes{fullDatabaseId createdAt updatedAt lastEditedAt body author{login}}pageInfo{hasNextPage endCursor}}timelineItems(first:100,itemTypes:[MERGED_EVENT]){totalCount nodes{...on MergedEvent{commit{oid}}}pageInfo{hasNextPage}}}}}`

type sliceAPIComment struct {
	ID           string `json:"fullDatabaseId"`
	CreatedAt    string
	UpdatedAt    string
	LastEditedAt *string
	Body         string
	Author       *struct{ Login string }
}
type sliceAPIPull struct {
	Number                              int
	Title, Body, URL, State, HeadRefOID string
	Merged                              bool
	MergeCommit                         *struct{ OID string }
	Comments                            struct {
		TotalCount int
		Nodes      []sliceAPIComment
		PageInfo   struct {
			HasNextPage bool
			EndCursor   *string
		}
	}
	TimelineItems struct {
		TotalCount int
		Nodes      []struct{ Commit *struct{ OID string } }
		PageInfo   struct{ HasNextPage bool }
	}
}

// Only exact active/root records trigger targeted closed-PR reads. This runs
// inside the existing complete/stable GraphQL snapshot and its query budget.
func loadNextSlicePulls(client pipelineGraphQLClient, repo, owner string, issues []apiIssue, prs []apiPull) ([]apiPull, error) {
	repositoryOwner, name, err := splitGitHubRepository(repo)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, p := range prs {
		seen[p.Number] = true
	}
	for _, issue := range issues {
		n, selected, protocol := sliceSource(issue, owner)
		if selected == "" || n == 0 || seen[n] || (!labelSet(issue.Labels)["in-work"] && !protocol) {
			continue
		}
		var cursor *string
		var pull apiPull
		ev := slicePullEvidence{Complete: true}
		total := -1
		ids := map[string]bool{}
		cursors := map[string]bool{}
		for page := 0; ; page++ {
			if page >= maxPipelineGraphQLPages {
				return nil, fmt.Errorf("next-slice comment pagination exceeded limit")
			}
			var data struct {
				Repository *struct{ PullRequest *sliceAPIPull }
			}
			if err := client.Query(nextSlicePullQuery, map[string]any{"owner": repositoryOwner, "name": name, "number": n, "cursor": cursor}, &data); err != nil {
				return nil, fmt.Errorf("next-slice PR #%d: %w", n, err)
			}
			if data.Repository == nil || data.Repository.PullRequest == nil {
				return nil, fmt.Errorf("next-slice PR #%d unavailable", n)
			}
			p := data.Repository.PullRequest
			if p.Number != n {
				return nil, fmt.Errorf("next-slice lookup returned wrong PR")
			}
			// An OPEN result raced the complete OPEN-PR snapshot. Reload all normal
			// fields; this closed-PR observation cannot supply REVIEW candidates.
			if strings.EqualFold(p.State, "OPEN") {
				return nil, &pipelineSnapshotChurn{fmt.Sprintf("next-slice PR #%d reopened; reload complete snapshot", n)}
			}
			if total < 0 {
				total = p.Comments.TotalCount
				pull = apiPull{Number: n, Title: p.Title, Body: p.Body, HTMLURL: p.URL, State: strings.ToLower(p.State)}
				pull.Head.SHA = p.HeadRefOID
				pull.Base.Ref = "main"
				ev.Merged = p.Merged
				if p.MergeCommit != nil {
					ev.MergeSHA = p.MergeCommit.OID
				}
				ev.MergeEvents = p.TimelineItems.TotalCount
				if p.TimelineItems.PageInfo.HasNextPage || len(p.TimelineItems.Nodes) != p.TimelineItems.TotalCount {
					ev.Complete = false
				}
				for _, e := range p.TimelineItems.Nodes {
					if e.Commit == nil || e.Commit.OID != ev.MergeSHA {
						ev.Complete = false
					}
				}
			} else if p.Comments.TotalCount != total || p.Body != pull.Body || p.HeadRefOID != pull.Head.SHA || strings.ToLower(p.State) != pull.State {
				return nil, &pipelineSnapshotChurn{"next-slice PR changed during pagination"}
			}
			for _, c := range p.Comments.Nodes {
				if ids[c.ID] || c.ID == "" {
					return nil, fmt.Errorf("duplicate or missing next-slice comment id")
				}
				ids[c.ID] = true
				if !strings.Contains(c.Body, "pp:slice-declaration") && !strings.HasPrefix(c.Body, "pp-slice-declaration-v1") {
					continue
				}
				author := ""
				if c.Author != nil {
					author = c.Author.Login
				}
				ev.Declarations = append(ev.Declarations, nextslice.Comment{ID: c.ID, Author: author, CreatedAt: c.CreatedAt, Edited: c.LastEditedAt != nil || c.CreatedAt != c.UpdatedAt, Body: c.Body})
			}
			if !p.Comments.PageInfo.HasNextPage {
				break
			}
			cursor = p.Comments.PageInfo.EndCursor
			if cursor == nil || *cursor == "" || cursors[*cursor] {
				return nil, fmt.Errorf("invalid next-slice pagination cursor")
			}
			cursors[*cursor] = true
		}
		if len(ids) != total {
			return nil, fmt.Errorf("incomplete next-slice comments")
		}
		pull.SliceEvidence = &ev
		prs = append(prs, pull)
		seen[n] = true
	}
	return prs, nil
}

func checkNextSliceVectors(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count, err := nextslice.CheckVectors(data)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": "pp-next-slice-golden-v1", "vectors_passed": count, "authoritative": false, "mutations_enabled": false})
}
