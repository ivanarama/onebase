package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/reviewrouting"
)

const routingRepositoryQuery = `query PipelineRoutingRepository($owner:String!,$name:String!){repository(owner:$owner,name:$name){id nameWithOwner}}`

// Keep this server timeline projection aligned with PromptPilot's typed reader.
// Cursor order and lifecycle/deletion events cannot be replaced by comments API.
const routingTimelineQuery = `
query PipelineRoutingTimeline($owner:String!,$name:String!,$number:Int!,$cursor:String){
 repository(owner:$owner,name:$name){id nameWithOwner
  pullRequest(number:$number){id number
   baseRepository{id nameWithOwner} headRepository{id nameWithOwner}
   headRefName isCrossRepository author{login ... on Node{id}}
   headRefOid baseRefOid baseRefName state isDraft
   labels(first:100){nodes{name} pageInfo{hasNextPage}}
   timelineItems(first:100,after:$cursor,itemTypes:[PULL_REQUEST_COMMIT,HEAD_REF_FORCE_PUSHED_EVENT,HEAD_REF_DELETED_EVENT,HEAD_REF_RESTORED_EVENT,BASE_REF_CHANGED_EVENT,BASE_REF_FORCE_PUSHED_EVENT,BASE_REF_DELETED_EVENT,MERGED_EVENT,ISSUE_COMMENT,COMMENT_DELETED_EVENT,LABELED_EVENT,UNLABELED_EVENT]){
    updatedAt pageInfo{hasNextPage endCursor}
    edges{cursor node{__typename
     ... on PullRequestCommit{id commit{oid}}
     ... on HeadRefForcePushedEvent{id createdAt afterCommit{oid}}
     ... on HeadRefDeletedEvent{id createdAt}
     ... on HeadRefRestoredEvent{id createdAt}
     ... on BaseRefChangedEvent{id createdAt previousRefName currentRefName}
     ... on BaseRefForcePushedEvent{id createdAt beforeCommit{oid} afterCommit{oid}}
     ... on BaseRefDeletedEvent{id createdAt baseRefName}
     ... on MergedEvent{id createdAt commit{oid}}
     ... on IssueComment{id fullDatabaseId createdAt lastEditedAt author{login} body}
     ... on CommentDeletedEvent{id createdAt}
     ... on LabeledEvent{id createdAt actor{login} label{name}}
     ... on UnlabeledEvent{id createdAt actor{login} label{name}}
    }}
   }
  }
 }
}`

type routingSnapshot struct {
	value map[string]any
	err   error
}

func routingObject(value any) map[string]any { object, _ := value.(map[string]any); return object }
func routingText(value any) string           { text, _ := value.(string); return text }

func routingQuery(client pipelineGraphQLClient, query string, variables map[string]any) (map[string]any, error) {
	var raw json.RawMessage
	if err := client.Query(query, variables, &raw); err != nil {
		return nil, err
	}
	value, err := reviewrouting.Decode(raw, 8<<20)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("typed GraphQL data is not an object")
	}
	return object, nil
}

func routingPass(client pipelineGraphQLClient, repository, repositoryID string, number int) (map[string]any, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid routing repository")
	}
	variables := map[string]any{"owner": parts[0], "name": parts[1], "number": number, "cursor": nil}
	var header map[string]any
	var watermark any
	values := []any{}
	cursors, edgeIDs, nodeIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		data, err := routingQuery(client, routingTimelineQuery, variables)
		if err != nil {
			return nil, err
		}
		page, err := reviewrouting.NormalizePage(data, repository, repositoryID, int64(number))
		if err != nil {
			return nil, err
		}
		if header != nil && !reviewrouting.Equal(header, page.Header) {
			return nil, fmt.Errorf("typed PR identity/state changed during pagination")
		}
		if header != nil && !reviewrouting.Equal(watermark, page.UpdatedAt) {
			return nil, fmt.Errorf("typed timeline watermark changed during pagination")
		}
		header = page.Header
		watermark = page.UpdatedAt
		for _, raw := range page.Edges {
			edge := routingObject(raw)
			cursor := routingText(edge["cursor"])
			id := routingText(routingObject(edge["node"])["id"])
			if edgeIDs[cursor] || nodeIDs[id] {
				return nil, fmt.Errorf("typed timeline duplicate edge/node identity")
			}
			edgeIDs[cursor], nodeIDs[id] = true, true
			values = append(values, raw)
		}
		snapshot := map[string]any{}
		for key, value := range header {
			snapshot[key] = value
		}
		snapshot["edges"], snapshot["updatedAt"] = values, page.UpdatedAt
		if _, err := reviewrouting.Canonical(snapshot); err != nil {
			return nil, err
		}
		if !page.HasNext {
			return snapshot, nil
		}
		if cursors[page.EndCursor] {
			return nil, fmt.Errorf("typed timeline pagination did not advance")
		}
		cursors[page.EndCursor] = true
		variables["cursor"] = page.EndCursor
	}
	return nil, fmt.Errorf("typed timeline exceeds bounded complete history")
}

func stableRoutingSnapshot(client pipelineGraphQLClient, repository, repositoryID string, number int) (map[string]any, error) {
	first, err := routingPass(client, repository, repositoryID, number)
	if err != nil {
		return nil, err
	}
	second, err := routingPass(client, repository, repositoryID, number)
	if err != nil {
		return nil, err
	}
	if !reviewrouting.Equal(first, second) {
		return nil, fmt.Errorf("typed timeline changed between complete stable reads")
	}
	return first, nil
}

func loadRoutingSnapshots(client pipelineGraphQLClient, prs []apiPull, repository, fixture string) map[int]routingSnapshot {
	result := map[int]routingSnapshot{}
	if fixture != "" {
		data, err := os.ReadFile(fixture)
		var value any
		if err == nil {
			value, err = reviewrouting.Decode(data, 8<<20)
		}
		array, ok := value.([]any)
		if err == nil && !ok {
			err = fmt.Errorf("typed PR fixture must be an array")
		}
		if err != nil {
			for _, pr := range prs {
				result[pr.Number] = routingSnapshot{err: err}
			}
			return result
		}
		if len(array) != len(prs) {
			for _, pr := range prs {
				result[pr.Number] = routingSnapshot{err: fmt.Errorf("typed fixture and PR queue length differ")}
			}
			return result
		}
		for i, pr := range prs {
			if i >= len(array) {
				result[pr.Number] = routingSnapshot{err: fmt.Errorf("offline typed fixture target is missing")}
				continue
			}
			raw := array[i]
			number, ok := routingObject(raw)["number"].(json.Number)
			if !ok || string(number) != strconv.Itoa(pr.Number) {
				result[pr.Number] = routingSnapshot{err: fmt.Errorf("offline fixture outer PR number differs from target")}
				continue
			}
			snapshot, ok := routingObject(raw)["routing_snapshot"].(map[string]any)
			if !ok {
				result[pr.Number] = routingSnapshot{err: fmt.Errorf("offline typed fixture lacks complete routing_snapshot")}
				continue
			}
			result[pr.Number] = routingSnapshot{value: snapshot}
		}
		return result
	}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		for _, pr := range prs {
			result[pr.Number] = routingSnapshot{err: fmt.Errorf("invalid routing repository")}
		}
		return result
	}
	bounded := limitPipelineGraphQLClient(client)
	data, err := routingQuery(bounded, routingRepositoryQuery, map[string]any{"owner": parts[0], "name": parts[1]})
	repo := routingObject(data["repository"])
	id := routingText(repo["id"])
	if err == nil && (repo["nameWithOwner"] != repository || id == "") {
		err = fmt.Errorf("routing repository identity is unavailable")
	}
	jobs := make(chan apiPull, len(prs))
	for _, pr := range prs {
		if pr.State != "open" || pr.Base.Ref != "main" {
			continue
		}
		if err != nil {
			result[pr.Number] = routingSnapshot{err: err}
			continue
		}
		jobs <- pr
	}
	close(jobs)
	type observed struct {
		number   int
		snapshot routingSnapshot
	}
	completed := make(chan observed)
	var workers sync.WaitGroup
	// Keep both ordered complete passes within each PR. Only independent PRs
	// overlap, sharing this snapshot's physical-query budget across all workers.
	for range min(6, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for pr := range jobs {
				value, readErr := stableRoutingSnapshot(bounded, repository, id, pr.Number)
				completed <- observed{pr.Number, routingSnapshot{value, readErr}}
			}
		}()
	}
	go func() { workers.Wait(); close(completed) }()
	for item := range completed {
		result[item.number] = item.snapshot
	}
	return result
}
