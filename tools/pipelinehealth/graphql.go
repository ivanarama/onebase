package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const pipelineHealthSnapshotQuery = `
query PipelineHealthSnapshot(
  $owner: String!
  $name: String!
  $pullCursor: String
  $issueCursor: String
  $includePulls: Boolean!
  $includeIssues: Boolean!
) {
  repository(owner: $owner, name: $name) {
    pullRequests(
      states: OPEN
      first: 100
      after: $pullCursor
      orderBy: {field: CREATED_AT, direction: ASC}
    ) @include(if: $includePulls) {
      totalCount
      nodes {
        id
        number
        title
        body
        url
        createdAt
        updatedAt
        state
        isDraft
        headRefOid
        baseRefName
        labels(first: 100) {
          totalCount
          nodes { name }
          pageInfo { hasNextPage endCursor }
        }
        comments(first: 100) {
          totalCount
          nodes {
            fullDatabaseId
            createdAt
            updatedAt
            author { login }
            body
          }
          pageInfo { hasNextPage endCursor }
        }
        commits(last: 1) {
          nodes { commit { id oid } }
        }
      }
      pageInfo { hasNextPage endCursor }
    }
    issues(
      states: OPEN
      first: 100
      after: $issueCursor
      orderBy: {field: CREATED_AT, direction: ASC}
    ) @include(if: $includeIssues) {
      totalCount
      nodes {
        id
        number
        title
        url
        createdAt
        updatedAt
        state
        labels(first: 100) {
          totalCount
          nodes { name }
          pageInfo { hasNextPage endCursor }
        }
        comments(first: 100) {
          totalCount
          nodes {
            fullDatabaseId
            createdAt
            updatedAt
            author { login }
            body
          }
          pageInfo { hasNextPage endCursor }
        }
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const pipelineHealthNodePageQuery = `
query PipelineHealthNodePage(
  $id: ID!
  $labelCursor: String
  $commentCursor: String
  $includeLabels: Boolean!
  $includeComments: Boolean!
) {
  node(id: $id) {
    __typename
    ... on PullRequest {
      labels(first: 100, after: $labelCursor) @include(if: $includeLabels) {
        totalCount
        nodes { name }
        pageInfo { hasNextPage endCursor }
      }
      comments(first: 100, after: $commentCursor) @include(if: $includeComments) {
        totalCount
        nodes {
          fullDatabaseId
          createdAt
          updatedAt
          author { login }
          body
        }
        pageInfo { hasNextPage endCursor }
      }
    }
    ... on Issue {
      labels(first: 100, after: $labelCursor) @include(if: $includeLabels) {
        totalCount
        nodes { name }
        pageInfo { hasNextPage endCursor }
      }
      comments(first: 100, after: $commentCursor) @include(if: $includeComments) {
        totalCount
        nodes {
          fullDatabaseId
          createdAt
          updatedAt
          author { login }
          body
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

const pipelineHealthCommitParentsQuery = `
query PipelineHealthCommitParents($ids: [ID!]!) {
  nodes(ids: $ids) {
    __typename
    ... on Commit {
      id
      oid
      parents(first: 100) {
        totalCount
        nodes { oid }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

const pipelineHealthCommitParentsPageQuery = `
query PipelineHealthCommitParentsPage($id: ID!, $cursor: String!) {
  node(id: $id) {
    __typename
    ... on Commit {
      id
      oid
      parents(first: 100, after: $cursor) {
        totalCount
        nodes { oid }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

const pipelineHealthPullHeadsQuery = `
query PipelineHealthPullHeads($ids: [ID!]!) {
  nodes(ids: $ids) {
    __typename
    ... on PullRequest {
      id
      headRefOid
    }
  }
}`

type pipelineGraphQLClient interface {
	Query(query string, variables map[string]any, destination any) error
}

// ghPipelineGraphQLClient invokes the authenticated GitHub CLI without a
// shell. GraphQL errors are parsed even when gh exits non-zero: partial data is
// never accepted as a queue snapshot.
type ghPipelineGraphQLClient struct {
	executable string
	timeout    time.Duration
	run        pipelineGraphQLCommandRunner
}

type pipelineGraphQLCommandRunner func(ctx context.Context, executable string, input []byte, stdout, stderr io.Writer) error

type pipelineGraphQLAPIError struct {
	messages []string
}

func (err *pipelineGraphQLAPIError) Error() string {
	return truncatePipelineGraphQLDetail("GraphQL response errors: " + strings.Join(err.messages, "; "))
}

const (
	maxPipelineGraphQLResponseBytes = 64 << 20
	maxPipelineGraphQLStderrBytes   = 1 << 20
	maxPipelineGraphQLDetailBytes   = 8 << 10
	maxPipelineGraphQLPages         = 10_000
	maxPipelineGraphQLQueries       = 10_000
	defaultPipelineGraphQLTimeout   = 90 * time.Second
)

type boundedCapture struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func newBoundedCapture(limit int) *boundedCapture {
	return &boundedCapture{remaining: limit}
}

func (capture *boundedCapture) Write(data []byte) (int, error) {
	original := len(data)
	if len(data) > capture.remaining {
		data = data[:capture.remaining]
		capture.truncated = true
	}
	if len(data) > 0 {
		_, _ = capture.buffer.Write(data)
		capture.remaining -= len(data)
	}
	return original, nil
}

func (capture *boundedCapture) Bytes() []byte {
	return capture.buffer.Bytes()
}

func (capture *boundedCapture) String() string {
	return capture.buffer.String()
}

func newGHPipelineGraphQLClient() *ghPipelineGraphQLClient {
	executable := os.Getenv("GH_EXE")
	if executable == "" {
		executable = "gh"
	}
	return &ghPipelineGraphQLClient{executable: executable, timeout: defaultPipelineGraphQLTimeout}
}

func (client *ghPipelineGraphQLClient) Query(query string, variables map[string]any, destination any) error {
	payload, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("encode GraphQL request: %w", err)
	}

	timeout := client.timeout
	if timeout <= 0 {
		timeout = defaultPipelineGraphQLTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stdout := newBoundedCapture(maxPipelineGraphQLResponseBytes)
	stderr := newBoundedCapture(maxPipelineGraphQLStderrBytes)
	runner := client.run
	if runner == nil {
		runner = runPipelineGraphQLCommand
	}
	runErr := runner(ctx, client.executable, payload, stdout, stderr)
	if ctx.Err() != nil {
		return fmt.Errorf("gh api graphql timed out after %s", timeout)
	}
	if stdout.truncated {
		return fmt.Errorf("GraphQL response exceeds %d bytes", maxPipelineGraphQLResponseBytes)
	}
	if stderr.truncated {
		return fmt.Errorf("gh api graphql stderr exceeds %d bytes", maxPipelineGraphQLStderrBytes)
	}

	decodeErr := decodePipelineGraphQLResponse(stdout.Bytes(), destination)
	if runErr != nil {
		if _, ok := decodeErr.(*pipelineGraphQLAPIError); ok {
			return decodeErr
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		detail = truncatePipelineGraphQLDetail(detail)
		return fmt.Errorf("run gh api graphql: %w: %s", runErr, detail)
	}
	if decodeErr != nil {
		return decodeErr
	}
	return nil
}

func runPipelineGraphQLCommand(ctx context.Context, executable string, input []byte, stdout, stderr io.Writer) error {
	// GH_EXE is explicit operator configuration and no untrusted value is
	// interpreted by a shell.
	//nolint:gosec
	cmd := exec.CommandContext(ctx, executable, "api", "graphql", "--input", "-")
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func truncatePipelineGraphQLDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if len(detail) <= maxPipelineGraphQLDetailBytes {
		return detail
	}
	const marker = " ... [truncated]"
	limit := maxPipelineGraphQLDetailBytes - len(marker)
	for limit > 0 && !utf8.RuneStart(detail[limit]) {
		limit--
	}
	return detail[:limit] + marker
}

func decodePipelineGraphQLResponse(response []byte, destination any) error {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return fmt.Errorf("decode GraphQL envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, item.Message)
		}
		return &pipelineGraphQLAPIError{messages: messages}
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("GraphQL response contains no data")
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		return fmt.Errorf("decode GraphQL data: %w", err)
	}
	return nil
}

type gqlPageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

func (info *gqlPageInfo) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "pageInfo")
	if err != nil {
		return err
	}
	if err := decodeGQLRequired(fields, "hasNextPage", &info.HasNextPage); err != nil {
		return err
	}
	return decodeGQLNullable(fields, "endCursor", &info.EndCursor)
}

type gqlLabel struct {
	Name string `json:"name"`
}

func (label *gqlLabel) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "label")
	if err != nil {
		return err
	}
	return decodeGQLRequired(fields, "name", &label.Name)
}

type gqlConnection[T any] struct {
	TotalCount int         `json:"totalCount"`
	Nodes      []T         `json:"nodes"`
	PageInfo   gqlPageInfo `json:"pageInfo"`
}

func unmarshalGQLConnection[T any](data []byte, connection *gqlConnection[T]) error {
	fields, err := decodeGQLObject(data, "connection")
	if err != nil {
		return err
	}
	if err := decodeGQLRequired(fields, "totalCount", &connection.TotalCount); err != nil {
		return err
	}
	if connection.TotalCount < 0 {
		return fmt.Errorf("GraphQL field %q is negative", "totalCount")
	}
	rawNodes, ok := fields["nodes"]
	if !ok {
		return fmt.Errorf("GraphQL field %q is missing", "nodes")
	}
	if isJSONNull(rawNodes) {
		return fmt.Errorf("GraphQL field %q is null", "nodes")
	}
	if err := json.Unmarshal(rawNodes, &connection.Nodes); err != nil {
		return fmt.Errorf("decode GraphQL field %q: %w", "nodes", err)
	}
	if err := decodeGQLRequired(fields, "pageInfo", &connection.PageInfo); err != nil {
		return err
	}
	return nil
}

type gqlLabelConnection gqlConnection[gqlLabel]

func (connection *gqlLabelConnection) UnmarshalJSON(data []byte) error {
	return unmarshalGQLConnection(data, (*gqlConnection[gqlLabel])(connection))
}

type gqlActor struct {
	Login string `json:"login"`
}

func (actor *gqlActor) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "actor")
	if err != nil {
		return err
	}
	if err := decodeGQLRequired(fields, "login", &actor.Login); err != nil {
		return err
	}
	if actor.Login == "" {
		return fmt.Errorf("GraphQL actor login is empty")
	}
	return nil
}

type gqlComment struct {
	FullDatabaseID string    `json:"fullDatabaseId"`
	CreatedAt      string    `json:"createdAt"`
	UpdatedAt      string    `json:"updatedAt"`
	Author         *gqlActor `json:"author"`
	Body           string    `json:"body"`
}

func (comment *gqlComment) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "comment")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name        string
		destination any
	}{
		{"fullDatabaseId", &comment.FullDatabaseID},
		{"createdAt", &comment.CreatedAt},
		{"updatedAt", &comment.UpdatedAt},
		{"body", &comment.Body},
	} {
		if err := decodeGQLRequired(fields, field.name, field.destination); err != nil {
			return err
		}
	}
	return decodeGQLNullable(fields, "author", &comment.Author)
}

type gqlCommentConnection gqlConnection[gqlComment]

func (connection *gqlCommentConnection) UnmarshalJSON(data []byte) error {
	return unmarshalGQLConnection(data, (*gqlConnection[gqlComment])(connection))
}

type gqlHeadCommit struct {
	ID  string `json:"id"`
	OID string `json:"oid"`
}

func (commit *gqlHeadCommit) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "head commit")
	if err != nil {
		return err
	}
	if err := decodeGQLRequired(fields, "id", &commit.ID); err != nil {
		return err
	}
	return decodeGQLRequired(fields, "oid", &commit.OID)
}

type gqlPullCommitNode struct {
	Commit gqlHeadCommit `json:"commit"`
}

func (node *gqlPullCommitNode) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "pull commit node")
	if err != nil {
		return err
	}
	return decodeGQLRequired(fields, "commit", &node.Commit)
}

type gqlPullCommitConnection struct {
	Nodes []gqlPullCommitNode `json:"nodes"`
}

func (connection *gqlPullCommitConnection) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "pull commit connection")
	if err != nil {
		return err
	}
	return decodeGQLRequired(fields, "nodes", &connection.Nodes)
}

type gqlPull struct {
	NodeID      string                  `json:"id"`
	Number      int                     `json:"number"`
	Title       string                  `json:"title"`
	Body        string                  `json:"body"`
	URL         string                  `json:"url"`
	CreatedAt   string                  `json:"createdAt"`
	UpdatedAt   string                  `json:"updatedAt"`
	State       string                  `json:"state"`
	Draft       bool                    `json:"isDraft"`
	HeadRefOID  string                  `json:"headRefOid"`
	BaseRefName string                  `json:"baseRefName"`
	Labels      gqlLabelConnection      `json:"labels"`
	Comments    gqlCommentConnection    `json:"comments"`
	Commits     gqlPullCommitConnection `json:"commits"`
	HeadParents []string                `json:"-"`
}

func (pull *gqlPull) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "pull request")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name        string
		destination any
	}{
		{"id", &pull.NodeID},
		{"number", &pull.Number},
		{"title", &pull.Title},
		{"body", &pull.Body},
		{"url", &pull.URL},
		{"createdAt", &pull.CreatedAt},
		{"updatedAt", &pull.UpdatedAt},
		{"state", &pull.State},
		{"isDraft", &pull.Draft},
		{"headRefOid", &pull.HeadRefOID},
		{"baseRefName", &pull.BaseRefName},
		{"labels", &pull.Labels},
		{"comments", &pull.Comments},
		{"commits", &pull.Commits},
	} {
		if err := decodeGQLRequired(fields, field.name, field.destination); err != nil {
			return err
		}
	}
	if pull.NodeID == "" || pull.Number <= 0 || pull.State != "OPEN" || pull.HeadRefOID == "" || pull.BaseRefName == "" {
		return fmt.Errorf("GraphQL pull request has invalid identity or state")
	}
	if len(pull.Commits.Nodes) != 1 {
		return fmt.Errorf("GraphQL pull request head commit count is %d, want 1", len(pull.Commits.Nodes))
	}
	commit := pull.Commits.Nodes[0].Commit
	if commit.ID == "" || commit.OID == "" || commit.OID != pull.HeadRefOID {
		return fmt.Errorf("GraphQL pull request head commit does not match headRefOid")
	}
	return nil
}

type gqlIssue struct {
	NodeID    string               `json:"id"`
	Number    int                  `json:"number"`
	Title     string               `json:"title"`
	URL       string               `json:"url"`
	CreatedAt string               `json:"createdAt"`
	UpdatedAt string               `json:"updatedAt"`
	State     string               `json:"state"`
	Labels    gqlLabelConnection   `json:"labels"`
	Comments  gqlCommentConnection `json:"comments"`
}

func (issue *gqlIssue) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "issue")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name        string
		destination any
	}{
		{"id", &issue.NodeID},
		{"number", &issue.Number},
		{"title", &issue.Title},
		{"url", &issue.URL},
		{"createdAt", &issue.CreatedAt},
		{"updatedAt", &issue.UpdatedAt},
		{"state", &issue.State},
		{"labels", &issue.Labels},
		{"comments", &issue.Comments},
	} {
		if err := decodeGQLRequired(fields, field.name, field.destination); err != nil {
			return err
		}
	}
	if issue.NodeID == "" || issue.Number <= 0 || issue.State != "OPEN" {
		return fmt.Errorf("GraphQL issue has invalid identity or state")
	}
	return nil
}

type gqlPullConnection gqlConnection[gqlPull]

func (connection *gqlPullConnection) UnmarshalJSON(data []byte) error {
	return unmarshalGQLConnection(data, (*gqlConnection[gqlPull])(connection))
}

type gqlIssueConnection gqlConnection[gqlIssue]

func (connection *gqlIssueConnection) UnmarshalJSON(data []byte) error {
	return unmarshalGQLConnection(data, (*gqlConnection[gqlIssue])(connection))
}

type gqlSnapshotData struct {
	Repository *struct {
		PullRequests *gqlPullConnection  `json:"pullRequests"`
		Issues       *gqlIssueConnection `json:"issues"`
	} `json:"repository"`
}

type gqlNodePageData struct {
	Node *struct {
		TypeName string                `json:"__typename"`
		Labels   *gqlLabelConnection   `json:"labels"`
		Comments *gqlCommentConnection `json:"comments"`
	} `json:"node"`
}

type gqlParent struct {
	OID string `json:"oid"`
}

func (parent *gqlParent) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "commit parent")
	if err != nil {
		return err
	}
	return decodeGQLRequired(fields, "oid", &parent.OID)
}

type gqlParentConnection gqlConnection[gqlParent]

func (connection *gqlParentConnection) UnmarshalJSON(data []byte) error {
	return unmarshalGQLConnection(data, (*gqlConnection[gqlParent])(connection))
}

type gqlCommitParentsNode struct {
	TypeName string              `json:"__typename"`
	ID       string              `json:"id"`
	OID      string              `json:"oid"`
	Parents  gqlParentConnection `json:"parents"`
}

func (node *gqlCommitParentsNode) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "commit node")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name        string
		destination any
	}{
		{"__typename", &node.TypeName},
		{"id", &node.ID},
		{"oid", &node.OID},
		{"parents", &node.Parents},
	} {
		if err := decodeGQLRequired(fields, field.name, field.destination); err != nil {
			return err
		}
	}
	return nil
}

type gqlCommitParentsData struct {
	Nodes []*gqlCommitParentsNode `json:"nodes"`
}

type gqlCommitParentsPageData struct {
	Node *gqlCommitParentsNode `json:"node"`
}

type gqlPullHeadNode struct {
	TypeName   string `json:"__typename"`
	ID         string `json:"id"`
	HeadRefOID string `json:"headRefOid"`
}

func (node *gqlPullHeadNode) UnmarshalJSON(data []byte) error {
	fields, err := decodeGQLObject(data, "pull request head node")
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name        string
		destination any
	}{
		{"__typename", &node.TypeName},
		{"id", &node.ID},
		{"headRefOid", &node.HeadRefOID},
	} {
		if err := decodeGQLRequired(fields, field.name, field.destination); err != nil {
			return err
		}
	}
	return nil
}

type gqlPullHeadsData struct {
	Nodes []*gqlPullHeadNode `json:"nodes"`
}

func decodeGQLObject(data []byte, name string) (map[string]json.RawMessage, error) {
	if isJSONNull(data) {
		return nil, fmt.Errorf("GraphQL %s is null", name)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode GraphQL %s: %w", name, err)
	}
	if fields == nil {
		return nil, fmt.Errorf("GraphQL %s is not an object", name)
	}
	return fields, nil
}

func decodeGQLRequired(fields map[string]json.RawMessage, name string, destination any) error {
	raw, ok := fields[name]
	if !ok {
		return fmt.Errorf("GraphQL field %q is missing", name)
	}
	if isJSONNull(raw) {
		return fmt.Errorf("GraphQL field %q is null", name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("decode GraphQL field %q: %w", name, err)
	}
	return nil
}

func decodeGQLNullable(fields map[string]json.RawMessage, name string, destination any) error {
	raw, ok := fields[name]
	if !ok {
		return fmt.Errorf("GraphQL field %q is missing", name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("decode GraphQL field %q: %w", name, err)
	}
	return nil
}

func isJSONNull(data []byte) bool {
	return bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}

type limitedPipelineGraphQLClient struct {
	delegate pipelineGraphQLClient
	used     int
}

func (client *limitedPipelineGraphQLClient) Query(query string, variables map[string]any, destination any) error {
	if client.used >= maxPipelineGraphQLQueries {
		return fmt.Errorf("GraphQL snapshot exceeded %d queries", maxPipelineGraphQLQueries)
	}
	client.used++
	return client.delegate.Query(query, variables, destination)
}

// loadPipelineInputsGraphQL preserves the historical fixture matrix. In
// particular, -prs by itself is a completely offline input and intentionally
// yields no issues.
func loadPipelineInputsGraphQL(client pipelineGraphQLClient, repo, pullFixture, issueFixture string) ([]apiPull, []apiIssue, error) {
	if pullFixture != "" {
		pulls, err := readPullFixture(pullFixture)
		if err != nil {
			return nil, nil, err
		}
		if issueFixture == "" {
			return pulls, []apiIssue{}, nil
		}
		issues, err := readIssueFixture(issueFixture)
		if err != nil {
			return nil, nil, err
		}
		return pulls, issues, nil
	}

	if client == nil {
		return nil, nil, fmt.Errorf("GitHub GraphQL client is required outside pull fixture mode")
	}
	if issueFixture != "" {
		pulls, _, err := loadGraphQLSnapshot(client, repo, true, false)
		if err != nil {
			return nil, nil, err
		}
		issues, err := readIssueFixture(issueFixture)
		if err != nil {
			return nil, nil, err
		}
		return pulls, issues, nil
	}
	return loadGraphQLSnapshot(client, repo, true, true)
}

func readPullFixture(path string) ([]apiPull, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pulls []apiPull
	if err := json.Unmarshal(data, &pulls); err != nil {
		return nil, fmt.Errorf("decode pull fixture: %w", err)
	}
	return pulls, nil
}

func readIssueFixture(path string) ([]apiIssue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var issues []apiIssue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, fmt.Errorf("decode issue fixture: %w", err)
	}
	return issues, nil
}

func loadGraphQLSnapshot(client pipelineGraphQLClient, repo string, includePulls, includeIssues bool) ([]apiPull, []apiIssue, error) {
	if client == nil {
		return nil, nil, fmt.Errorf("GitHub GraphQL client is required")
	}
	client = &limitedPipelineGraphQLClient{delegate: client}
	owner, name, err := splitGitHubRepository(repo)
	if err != nil {
		return nil, nil, err
	}
	if !includePulls && !includeIssues {
		return []apiPull{}, []apiIssue{}, nil
	}

	pullsDone, issuesDone := !includePulls, !includeIssues
	var pullCursor, issueCursor *string
	pullCursors, issueCursors := map[string]bool{}, map[string]bool{}
	var rawPulls []gqlPull
	var rawIssues []gqlIssue
	pullTotal, issueTotal := -1, -1
	pullNodes, issueNodes := map[string]bool{}, map[string]bool{}

	for !pullsDone || !issuesDone {
		variables := map[string]any{
			"owner":         owner,
			"name":          name,
			"pullCursor":    pullCursor,
			"issueCursor":   issueCursor,
			"includePulls":  !pullsDone,
			"includeIssues": !issuesDone,
		}
		var data gqlSnapshotData
		if err := client.Query(pipelineHealthSnapshotQuery, variables, &data); err != nil {
			return nil, nil, fmt.Errorf("load GitHub queue snapshot: %w", err)
		}
		if data.Repository == nil {
			return nil, nil, fmt.Errorf("load GitHub queue snapshot: repository %s not found", repo)
		}

		if !pullsDone {
			connection := data.Repository.PullRequests
			if connection == nil {
				return nil, nil, fmt.Errorf("load pull requests: GraphQL connection is missing")
			}
			if pullTotal < 0 {
				pullTotal = connection.TotalCount
			} else if connection.TotalCount != pullTotal {
				return nil, nil, fmt.Errorf("load pull requests: totalCount changed from %d to %d", pullTotal, connection.TotalCount)
			}
			if connection.TotalCount < 0 {
				return nil, nil, fmt.Errorf("load pull requests: negative totalCount %d", connection.TotalCount)
			}
			if len(connection.Nodes) == 0 && (pullCursor != nil || connection.PageInfo.HasNextPage) {
				return nil, nil, fmt.Errorf("load pull requests: empty advancing page")
			}
			for _, pull := range connection.Nodes {
				if pull.NodeID == "" || pullNodes[pull.NodeID] {
					return nil, nil, fmt.Errorf("load pull requests: missing or duplicate node id for PR #%d", pull.Number)
				}
				pullNodes[pull.NodeID] = true
				rawPulls = append(rawPulls, pull)
			}
			if len(rawPulls) > pullTotal {
				return nil, nil, fmt.Errorf("load pull requests: accumulated %d nodes exceeds totalCount %d", len(rawPulls), pullTotal)
			}
			pullCursor, pullsDone, err = advanceGQLCursor("pull requests", connection.PageInfo, pullCursors)
			if err != nil {
				return nil, nil, err
			}
		}

		if !issuesDone {
			connection := data.Repository.Issues
			if connection == nil {
				return nil, nil, fmt.Errorf("load issues: GraphQL connection is missing")
			}
			if issueTotal < 0 {
				issueTotal = connection.TotalCount
			} else if connection.TotalCount != issueTotal {
				return nil, nil, fmt.Errorf("load issues: totalCount changed from %d to %d", issueTotal, connection.TotalCount)
			}
			if connection.TotalCount < 0 {
				return nil, nil, fmt.Errorf("load issues: negative totalCount %d", connection.TotalCount)
			}
			if len(connection.Nodes) == 0 && (issueCursor != nil || connection.PageInfo.HasNextPage) {
				return nil, nil, fmt.Errorf("load issues: empty advancing page")
			}
			for _, issue := range connection.Nodes {
				if issue.NodeID == "" || issueNodes[issue.NodeID] {
					return nil, nil, fmt.Errorf("load issues: missing or duplicate node id for issue #%d", issue.Number)
				}
				issueNodes[issue.NodeID] = true
				rawIssues = append(rawIssues, issue)
			}
			if len(rawIssues) > issueTotal {
				return nil, nil, fmt.Errorf("load issues: accumulated %d nodes exceeds totalCount %d", len(rawIssues), issueTotal)
			}
			issueCursor, issuesDone, err = advanceGQLCursor("issues", connection.PageInfo, issueCursors)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	if includePulls && len(rawPulls) != pullTotal {
		return nil, nil, fmt.Errorf("load pull requests: got %d nodes, want totalCount %d", len(rawPulls), pullTotal)
	}
	if includeIssues && len(rawIssues) != issueTotal {
		return nil, nil, fmt.Errorf("load issues: got %d nodes, want totalCount %d", len(rawIssues), issueTotal)
	}

	for index := range rawPulls {
		if err := completeNodeConnections(client, rawPulls[index].NodeID, "PullRequest", &rawPulls[index].Labels, &rawPulls[index].Comments); err != nil {
			return nil, nil, fmt.Errorf("PR #%d: %w", rawPulls[index].Number, err)
		}
	}
	for index := range rawIssues {
		if err := completeNodeConnections(client, rawIssues[index].NodeID, "Issue", &rawIssues[index].Labels, &rawIssues[index].Comments); err != nil {
			return nil, nil, fmt.Errorf("issue #%d: %w", rawIssues[index].Number, err)
		}
	}
	if err := loadShipHeadParents(client, rawPulls); err != nil {
		return nil, nil, err
	}

	pulls := make([]apiPull, 0, len(rawPulls))
	for _, raw := range rawPulls {
		pull, err := convertGQLPull(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("PR #%d: %w", raw.Number, err)
		}
		pulls = append(pulls, pull)
	}
	issues := make([]apiIssue, 0, len(rawIssues))
	for _, raw := range rawIssues {
		issue, err := convertGQLIssue(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("issue #%d: %w", raw.Number, err)
		}
		if issue.CommentCount > 0 {
			issues = append(issues, issue)
		}
	}
	return pulls, issues, nil
}

func splitGitHubRepository(repo string) (string, string, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(repo), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("invalid GitHub repository %q; expected owner/name", repo)
	}
	return owner, name, nil
}

func advanceGQLCursor(scope string, info gqlPageInfo, seen map[string]bool) (*string, bool, error) {
	if !info.HasNextPage {
		return nil, true, nil
	}
	if info.EndCursor == nil || *info.EndCursor == "" {
		return nil, false, fmt.Errorf("%s pagination hasNextPage without endCursor", scope)
	}
	if len(seen) >= maxPipelineGraphQLPages {
		return nil, false, fmt.Errorf("%s pagination exceeded %d pages", scope, maxPipelineGraphQLPages)
	}
	if seen[*info.EndCursor] {
		return nil, false, fmt.Errorf("%s pagination repeated cursor %q", scope, *info.EndCursor)
	}
	seen[*info.EndCursor] = true
	cursor := *info.EndCursor
	return &cursor, false, nil
}

func completeNodeConnections(client pipelineGraphQLClient, nodeID, typeName string, labels *gqlLabelConnection, comments *gqlCommentConnection) error {
	if labels.TotalCount < 0 || comments.TotalCount < 0 {
		return fmt.Errorf("load labels/comments: negative totalCount")
	}
	if len(labels.Nodes) > labels.TotalCount {
		return fmt.Errorf("load labels: accumulated %d nodes exceeds totalCount %d", len(labels.Nodes), labels.TotalCount)
	}
	if len(comments.Nodes) > comments.TotalCount {
		return fmt.Errorf("load comments: accumulated %d nodes exceeds totalCount %d", len(comments.Nodes), comments.TotalCount)
	}
	if labels.PageInfo.HasNextPage && len(labels.Nodes) == 0 {
		return fmt.Errorf("load labels: empty advancing page")
	}
	if comments.PageInfo.HasNextPage && len(comments.Nodes) == 0 {
		return fmt.Errorf("load comments: empty advancing page")
	}
	labelSeen, commentSeen := map[string]bool{}, map[string]bool{}
	for labels.PageInfo.HasNextPage || comments.PageInfo.HasNextPage {
		loadLabels := labels.PageInfo.HasNextPage
		loadComments := comments.PageInfo.HasNextPage
		labelCursor, _, err := advanceGQLCursor("labels", labels.PageInfo, labelSeen)
		if err != nil && loadLabels {
			return err
		}
		commentCursor, _, err := advanceGQLCursor("comments", comments.PageInfo, commentSeen)
		if err != nil && loadComments {
			return err
		}
		variables := map[string]any{
			"id":              nodeID,
			"labelCursor":     labelCursor,
			"commentCursor":   commentCursor,
			"includeLabels":   loadLabels,
			"includeComments": loadComments,
		}
		var data gqlNodePageData
		if err := client.Query(pipelineHealthNodePageQuery, variables, &data); err != nil {
			return fmt.Errorf("load paginated labels/comments: %w", err)
		}
		if data.Node == nil || data.Node.TypeName != typeName {
			return fmt.Errorf("load paginated labels/comments: node %q is missing or has unexpected type", nodeID)
		}
		if loadLabels {
			if data.Node.Labels == nil {
				return fmt.Errorf("load paginated labels: connection is missing")
			}
			if data.Node.Labels.TotalCount != labels.TotalCount {
				return fmt.Errorf("load paginated labels: totalCount changed from %d to %d", labels.TotalCount, data.Node.Labels.TotalCount)
			}
			if len(data.Node.Labels.Nodes) == 0 {
				return fmt.Errorf("load paginated labels: empty advancing page")
			}
			labels.Nodes = append(labels.Nodes, data.Node.Labels.Nodes...)
			if len(labels.Nodes) > labels.TotalCount {
				return fmt.Errorf("load labels: accumulated %d nodes exceeds totalCount %d", len(labels.Nodes), labels.TotalCount)
			}
			labels.PageInfo = data.Node.Labels.PageInfo
		}
		if loadComments {
			if data.Node.Comments == nil {
				return fmt.Errorf("load paginated comments: connection is missing")
			}
			if data.Node.Comments.TotalCount != comments.TotalCount {
				return fmt.Errorf("load paginated comments: totalCount changed from %d to %d", comments.TotalCount, data.Node.Comments.TotalCount)
			}
			if len(data.Node.Comments.Nodes) == 0 {
				return fmt.Errorf("load paginated comments: empty advancing page")
			}
			comments.Nodes = append(comments.Nodes, data.Node.Comments.Nodes...)
			if len(comments.Nodes) > comments.TotalCount {
				return fmt.Errorf("load comments: accumulated %d nodes exceeds totalCount %d", len(comments.Nodes), comments.TotalCount)
			}
			comments.PageInfo = data.Node.Comments.PageInfo
		}
	}
	if len(labels.Nodes) != labels.TotalCount {
		return fmt.Errorf("load labels: got %d nodes, want totalCount %d", len(labels.Nodes), labels.TotalCount)
	}
	if len(comments.Nodes) != comments.TotalCount {
		return fmt.Errorf("load comments: got %d nodes, want totalCount %d", len(comments.Nodes), comments.TotalCount)
	}
	labelNames := make(map[string]bool, len(labels.Nodes))
	for _, label := range labels.Nodes {
		if label.Name == "" || labelNames[label.Name] {
			return fmt.Errorf("load labels: empty or duplicate label name %q", label.Name)
		}
		labelNames[label.Name] = true
	}
	commentIDs := make(map[string]bool, len(comments.Nodes))
	for _, comment := range comments.Nodes {
		if comment.FullDatabaseID == "" || commentIDs[comment.FullDatabaseID] {
			return fmt.Errorf("load comments: empty or duplicate fullDatabaseId %q", comment.FullDatabaseID)
		}
		commentIDs[comment.FullDatabaseID] = true
	}
	return nil
}

func loadShipHeadParents(client pipelineGraphQLClient, pulls []gqlPull) error {
	commitPulls := map[string][]int{}
	shipPulls := map[string][]int{}
	for index := range pulls {
		preview, err := convertGQLPull(pulls[index])
		if err != nil {
			return fmt.Errorf("PR #%d: %w", pulls[index].Number, err)
		}
		if !needsHeadParents(preview) {
			continue
		}
		if len(pulls[index].Commits.Nodes) != 1 {
			return fmt.Errorf("PR #%d: head commit is missing", pulls[index].Number)
		}
		commit := pulls[index].Commits.Nodes[0].Commit
		if commit.ID == "" || commit.OID == "" || commit.OID != pulls[index].HeadRefOID {
			return fmt.Errorf("PR #%d: head commit does not match captured headRefOid", pulls[index].Number)
		}
		commitPulls[commit.ID] = append(commitPulls[commit.ID], index)
		shipPulls[pulls[index].NodeID] = append(shipPulls[pulls[index].NodeID], index)
	}

	ids := make([]string, 0, len(commitPulls))
	for id := range commitPulls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		requested := make(map[string]bool, end-start)
		for _, id := range ids[start:end] {
			requested[id] = true
		}
		var data gqlCommitParentsData
		if err := client.Query(pipelineHealthCommitParentsQuery, map[string]any{"ids": ids[start:end]}, &data); err != nil {
			return fmt.Errorf("load ship head parents: %w", err)
		}
		if len(data.Nodes) != end-start {
			return fmt.Errorf("load ship head parents: got %d commit nodes, want %d", len(data.Nodes), end-start)
		}
		seen := map[string]bool{}
		for _, node := range data.Nodes {
			if node == nil || node.TypeName != "Commit" || node.ID == "" || seen[node.ID] {
				return fmt.Errorf("load ship head parents: missing, duplicate, or unexpected commit node")
			}
			indices, ok := commitPulls[node.ID]
			if !ok {
				return fmt.Errorf("load ship head parents: unexpected commit node %q", node.ID)
			}
			if !requested[node.ID] {
				return fmt.Errorf("load ship head parents: commit node %q was not requested in this batch", node.ID)
			}
			seen[node.ID] = true
			expectedOID := pulls[indices[0]].HeadRefOID
			if node.OID != expectedOID {
				return fmt.Errorf("load ship head parents: commit %q changed oid", node.ID)
			}
			if err := completeParentConnection(client, node); err != nil {
				return fmt.Errorf("load ship head parents for %s: %w", expectedOID, err)
			}
			parents := make([]string, 0, len(node.Parents.Nodes))
			for _, parent := range node.Parents.Nodes {
				if parent.OID == "" {
					return fmt.Errorf("load ship head parents: empty parent oid for %s", expectedOID)
				}
				parents = append(parents, parent.OID)
			}
			for _, index := range indices {
				pulls[index].HeadParents = append([]string(nil), parents...)
			}
		}
		if len(seen) != end-start {
			return fmt.Errorf("load ship head parents: one or more requested commits are missing")
		}
	}
	return revalidateShipPullHeads(client, pulls, shipPulls)
}

func revalidateShipPullHeads(client pipelineGraphQLClient, pulls []gqlPull, shipPulls map[string][]int) error {
	ids := make([]string, 0, len(shipPulls))
	for id := range shipPulls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		requested := make(map[string]bool, end-start)
		for _, id := range ids[start:end] {
			requested[id] = true
		}
		var data gqlPullHeadsData
		if err := client.Query(pipelineHealthPullHeadsQuery, map[string]any{"ids": ids[start:end]}, &data); err != nil {
			return fmt.Errorf("revalidate ship pull heads: %w", err)
		}
		if len(data.Nodes) != end-start {
			return fmt.Errorf("revalidate ship pull heads: got %d pull nodes, want %d", len(data.Nodes), end-start)
		}
		seen := map[string]bool{}
		for _, node := range data.Nodes {
			if node == nil || node.TypeName != "PullRequest" || node.ID == "" || seen[node.ID] || !requested[node.ID] {
				return fmt.Errorf("revalidate ship pull heads: missing, duplicate, or unexpected pull node")
			}
			seen[node.ID] = true
			indices := shipPulls[node.ID]
			for _, index := range indices {
				if node.HeadRefOID != pulls[index].HeadRefOID {
					return fmt.Errorf("PR #%d: headRefOid changed from %s to %s while loading parents", pulls[index].Number, pulls[index].HeadRefOID, node.HeadRefOID)
				}
			}
		}
		if len(seen) != end-start {
			return fmt.Errorf("revalidate ship pull heads: one or more requested pulls are missing")
		}
	}
	return nil
}

func completeParentConnection(client pipelineGraphQLClient, node *gqlCommitParentsNode) error {
	if node.Parents.TotalCount < 0 {
		return fmt.Errorf("negative commit parent totalCount %d", node.Parents.TotalCount)
	}
	if len(node.Parents.Nodes) > node.Parents.TotalCount {
		return fmt.Errorf("accumulated %d parent nodes exceeds totalCount %d", len(node.Parents.Nodes), node.Parents.TotalCount)
	}
	if node.Parents.PageInfo.HasNextPage && len(node.Parents.Nodes) == 0 {
		return fmt.Errorf("commit parents: empty advancing page")
	}
	seen := map[string]bool{}
	for node.Parents.PageInfo.HasNextPage {
		cursor, _, err := advanceGQLCursor("commit parents", node.Parents.PageInfo, seen)
		if err != nil {
			return err
		}
		var data gqlCommitParentsPageData
		if err := client.Query(pipelineHealthCommitParentsPageQuery, map[string]any{"id": node.ID, "cursor": *cursor}, &data); err != nil {
			return err
		}
		if data.Node == nil || data.Node.TypeName != "Commit" || data.Node.ID != node.ID || data.Node.OID != node.OID {
			return fmt.Errorf("commit node changed during parent pagination")
		}
		if data.Node.Parents.TotalCount != node.Parents.TotalCount {
			return fmt.Errorf("commit parent totalCount changed from %d to %d", node.Parents.TotalCount, data.Node.Parents.TotalCount)
		}
		if len(data.Node.Parents.Nodes) == 0 {
			return fmt.Errorf("commit parents: empty advancing page")
		}
		node.Parents.Nodes = append(node.Parents.Nodes, data.Node.Parents.Nodes...)
		if len(node.Parents.Nodes) > node.Parents.TotalCount {
			return fmt.Errorf("accumulated %d parent nodes exceeds totalCount %d", len(node.Parents.Nodes), node.Parents.TotalCount)
		}
		node.Parents.PageInfo = data.Node.Parents.PageInfo
	}
	if len(node.Parents.Nodes) != node.Parents.TotalCount {
		return fmt.Errorf("got %d parent nodes, want totalCount %d", len(node.Parents.Nodes), node.Parents.TotalCount)
	}
	parentOIDs := make(map[string]bool, len(node.Parents.Nodes))
	for _, parent := range node.Parents.Nodes {
		if parent.OID == "" || parentOIDs[parent.OID] {
			return fmt.Errorf("empty or duplicate parent oid %q", parent.OID)
		}
		parentOIDs[parent.OID] = true
	}
	return nil
}

func convertGQLPull(raw gqlPull) (apiPull, error) {
	comments, err := convertGQLComments(raw.Comments.Nodes)
	if err != nil {
		return apiPull{}, err
	}
	pull := apiPull{
		Number:      raw.Number,
		Title:       raw.Title,
		Body:        raw.Body,
		HTMLURL:     raw.URL,
		CreatedAt:   raw.CreatedAt,
		UpdatedAt:   raw.UpdatedAt,
		State:       strings.ToLower(raw.State),
		Draft:       raw.Draft,
		Comments:    comments,
		HeadParents: append([]string(nil), raw.HeadParents...),
	}
	pull.Head.SHA = raw.HeadRefOID
	pull.Base.Ref = raw.BaseRefName
	for _, label := range raw.Labels.Nodes {
		pull.Labels = append(pull.Labels, apiLabel(label))
	}
	return pull, nil
}

func convertGQLIssue(raw gqlIssue) (apiIssue, error) {
	comments, err := convertGQLComments(raw.Comments.Nodes)
	if err != nil {
		return apiIssue{}, err
	}
	issue := apiIssue{
		Number:       raw.Number,
		Title:        raw.Title,
		HTMLURL:      raw.URL,
		CreatedAt:    raw.CreatedAt,
		UpdatedAt:    raw.UpdatedAt,
		State:        strings.ToLower(raw.State),
		CommentCount: raw.Comments.TotalCount,
		Thread:       comments,
	}
	for _, label := range raw.Labels.Nodes {
		issue.Labels = append(issue.Labels, apiLabel(label))
	}
	return issue, nil
}

func convertGQLComments(raw []gqlComment) ([]apiComment, error) {
	comments := make([]apiComment, 0, len(raw))
	for _, item := range raw {
		id, err := strconv.ParseInt(item.FullDatabaseID, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid fullDatabaseId %q", item.FullDatabaseID)
		}
		login := ""
		if item.Author != nil {
			login = item.Author.Login
		}
		comments = append(comments, apiComment{
			ID: id, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
			User: apiUser{Login: login}, Body: item.Body,
		})
	}
	return comments, nil
}
