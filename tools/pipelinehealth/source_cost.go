package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
)

const sourceGraphQLCostProtocol = "source-graphql-cost-v1"
const sourceGraphQLRateAlias = "ppSourceGraphqlRate"
const sourceGraphQLViewerAlias = "ppSourceGraphqlViewer"

// A complete receipt proves the point cost of one successful full observation.
// On failure, charged_upper_bound retains local escrow; it is NOT a proven
// upper bound on GitHub's unknown charges (timeouts may incur extra points).
type sourceGraphQLUsage struct {
	Protocol           string `json:"protocol"`
	Nonce              string `json:"nonce"`
	Allowance          int64  `json:"allowance"`
	ChargedUpperBound  int64  `json:"charged_upper_bound"`
	ObservedActualCost int64  `json:"observed_actual_cost"`
	PhysicalAttempts   int64  `json:"physical_attempts"`
	PreviewAttempts    int64  `json:"preview_attempts"`
	DataAttempts       int64  `json:"data_attempts"`
	UnprovedAttempts   int64  `json:"unproved_attempts"`
	UncertainCost      bool   `json:"uncertain_cost"`
	Complete           bool   `json:"complete"`
	Repo               string `json:"repo"`
	TrustedAccount     string `json:"trusted_account"`
	ContractSHA256     string `json:"contract_sha256"`
	Reason             string `json:"reason,omitempty"`
}

type sourceGraphQLMeter struct {
	// Preview escrows the entire remaining allowance. Serialize each pair
	// across ALL clients; pending escrow release is not new regional quota.
	mu       sync.Mutex
	usage    sourceGraphQLUsage
	contract string
	failed   bool
	escrow   int64
}

var sourceGraphQLNonce = regexp.MustCompile(`^[0-9a-f]{32}$`)
var sourceGraphQLPositiveInt = regexp.MustCompile(`^[1-9][0-9]*$`)

func sourceSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func newSourceGraphQLMeter(allowance int64, nonce, repo, trusted, contract string) (*sourceGraphQLMeter, error) {
	if allowance < 1 || allowance > math.MaxInt32 || !sourceGraphQLNonce.MatchString(nonce) {
		return nil, fmt.Errorf("source GraphQL cost requires allowance from1 to2147483647 and a32-character lowercase hex nonce")
	}
	if len(strings.Split(repo, "/")) != 2 || strings.TrimSpace(trusted) == "" {
		return nil, fmt.Errorf("source GraphQL cost requires repository and trusted account")
	}
	raw, err := os.ReadFile(contract)
	if err != nil {
		return nil, fmt.Errorf("source GraphQL cost contract: %w", err)
	}
	return &sourceGraphQLMeter{contract: contract, usage: sourceGraphQLUsage{
		Protocol: sourceGraphQLCostProtocol, Nonce: nonce, Allowance: allowance,
		Repo: repo, TrustedAccount: trusted, ContractSHA256: sourceSHA(raw),
	}}, nil
}

func (meter *sourceGraphQLMeter) stop(reason string) error {
	meter.failed = true
	if meter.usage.Reason == "" {
		meter.usage.Reason = reason
	}
	// Deliberately do not wrap a gateway error: an uncertain cost observation
	// cannot become complete by the ordinary read-retry policy.
	return fmt.Errorf("source GraphQL cost observation incomplete: %s", meter.usage.Reason)
}

func (meter *sourceGraphQLMeter) unproved(held int64, reason string) error {
	meter.usage.ChargedUpperBound += held
	meter.escrow = 0
	meter.usage.UncertainCost = true
	meter.usage.UnprovedAttempts++
	return meter.stop(reason)
}

func (meter *sourceGraphQLMeter) over(held, actual int64) error {
	meter.usage.ObservedActualCost += actual
	if actual > held {
		held = actual
	}
	return meter.unproved(held, "cost_exceeds_reservation")
}

func sourceGraphQLCost(data map[string]any) (int64, error) {
	rate, ok := data[sourceGraphQLRateAlias].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("missing server rateLimit cost")
	}
	number, ok := rate["cost"].(json.Number)
	if !ok || !sourceGraphQLPositiveInt.MatchString(number.String()) {
		return 0, fmt.Errorf("server cost is not a positive integer")
	}
	cost, err := number.Int64()
	if err != nil || cost > math.MaxInt32 {
		return 0, fmt.Errorf("server cost exceeds GraphQL Int")
	}
	return cost, nil
}

func sourceGraphQLData(raw json.RawMessage) (map[string]any, error) {
	value, err := sourceCostDecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	data, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("source GraphQL data must be an object")
	}
	return data, nil
}

func validateSourceGraphQLEnvelope(raw []byte) error {
	value, err := sourceCostDecodeJSON(raw)
	if err != nil {
		return err
	}
	envelope, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("source GraphQL envelope must be an object")
	}
	if rawErrors, present := envelope["errors"]; present {
		items, valid := rawErrors.([]any)
		if !valid || len(items) != 0 {
			return fmt.Errorf("source GraphQL receipt requires an absent or empty errors array")
		}
	}
	return nil
}

// These queries are the complete central transport's closed query vocabulary.
// Do not rewrite strings supplied by GitHub or infer syntax with a loose regexp.
func sourceGraphQLQuery(query string, preview bool) (string, error) {
	known := false
	for _, candidate := range []string{pipelineHealthSnapshotQuery, pipelineHealthNodePageQuery,
		pipelineHealthCommitParentsQuery, pipelineHealthCommitParentsPageQuery,
		pipelineHealthPullHeadsQuery, routingRepositoryQuery, routingTimelineQuery} {
		if query == candidate {
			known = true
			break
		}
	}
	if !known {
		return "", fmt.Errorf("query is outside the source cost protocol")
	}
	end := strings.LastIndex(query, "}")
	if end < 0 || strings.Contains(query, sourceGraphQLRateAlias) || strings.Contains(query, sourceGraphQLViewerAlias) {
		return "", fmt.Errorf("reserved source cost query fields")
	}
	argument := ""
	if preview {
		argument = "(dryRun: true)"
	}
	fields := "\n" + sourceGraphQLRateAlias + ": rateLimit" + argument + " { cost }\n" +
		sourceGraphQLViewerAlias + ": viewer { login }\n"
	return query[:end] + fields + query[end:], nil
}

func (client *ghPipelineGraphQLClient) queryWithSourceCost(query string, variables map[string]any, destination any) error {
	meter := client.sourceCost
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if meter.failed {
		return meter.stop(meter.usage.Reason)
	}
	preview, err := sourceGraphQLQuery(query, true)
	if err != nil {
		return meter.stop("query_not_supported")
	}
	dataQuery, err := sourceGraphQLQuery(query, false)
	if err != nil {
		return meter.stop("query_not_supported")
	}
	if _, err := json.Marshal(variables); err != nil {
		return meter.stop("request_not_encodable")
	}
	held := meter.usage.Allowance - meter.usage.ChargedUpperBound
	if held < 1 {
		return meter.stop("allowance_exhausted")
	}
	if meter.usage.PhysicalAttempts >= maxPipelineGraphQLQueries {
		return meter.stop("physical_request_limit")
	}
	// Unknown preview price: hold ALL remaining points before the physical
	// invocation, not an invented0/1. Only a clean server quote settles it.
	meter.escrow = held
	meter.usage.PhysicalAttempts++
	meter.usage.PreviewAttempts++
	var raw json.RawMessage
	if err := client.queryUnmetered(preview, variables, &raw); err != nil {
		return meter.unproved(held, "unproved_preview")
	}
	previewData, err := sourceGraphQLData(raw)
	if err != nil {
		return meter.unproved(held, "unproved_preview")
	}
	quote, err := sourceGraphQLCost(previewData)
	if err != nil {
		return meter.unproved(held, "unproved_preview")
	}
	if quote > held {
		return meter.over(held, quote)
	}
	meter.usage.ChargedUpperBound += quote
	meter.usage.ObservedActualCost += quote
	meter.escrow = 0
	if quote > meter.usage.Allowance-meter.usage.ChargedUpperBound {
		return meter.stop("allowance_exhausted")
	}
	if meter.usage.PhysicalAttempts >= maxPipelineGraphQLQueries {
		return meter.stop("physical_request_limit")
	}
	// Each actual physical data send needs its own pre-request reservation.
	meter.escrow = quote
	meter.usage.PhysicalAttempts++
	meter.usage.DataAttempts++
	if err := client.queryUnmetered(dataQuery, variables, &raw); err != nil {
		return meter.unproved(quote, "unproved_data")
	}
	data, err := sourceGraphQLData(raw)
	if err != nil {
		return meter.unproved(quote, "unproved_data")
	}
	actual, err := sourceGraphQLCost(data)
	if err != nil {
		return meter.unproved(quote, "unproved_data")
	}
	if actual > quote {
		return meter.over(quote, actual)
	}
	viewer, ok := data[sourceGraphQLViewerAlias].(map[string]any)
	if !ok || viewer["login"] != meter.usage.TrustedAccount {
		return meter.unproved(quote, "viewer_mismatch")
	}
	meter.usage.ChargedUpperBound += actual
	meter.usage.ObservedActualCost += actual
	meter.escrow = 0
	delete(data, sourceGraphQLRateAlias)
	delete(data, sourceGraphQLViewerAlias)
	clean, err := json.Marshal(data)
	if err != nil {
		return meter.stop("data_not_encodable")
	}
	if err := json.Unmarshal(clean, destination); err != nil {
		return meter.stop("data_not_decodable")
	}
	return nil
}

func (meter *sourceGraphQLMeter) receipt(fullReport bool) sourceGraphQLUsage {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if !fullReport && !meter.failed {
		_ = meter.stop("incomplete_checker_report")
	}
	raw, err := os.ReadFile(meter.contract)
	if err != nil || sourceSHA(raw) != meter.usage.ContractSHA256 {
		_ = meter.stop("contract_changed")
		// A later provenance failure must never be masked by an earlier clean
		// allowance refusal and become a temporary-availability exit.
		meter.usage.Reason = "contract_changed"
	}
	value := meter.usage
	value.Complete = fullReport && !meter.failed && meter.escrow == 0 && !value.UncertainCost && value.UnprovedAttempts == 0 &&
		value.DataAttempts > 0 && value.ChargedUpperBound == value.ObservedActualCost && value.ChargedUpperBound <= value.Allowance
	return value
}

func sourceGraphQLFailure(meter *sourceGraphQLMeter, err error) {
	if meter == nil {
		fail(err)
	}
	usage := meter.receipt(false)
	// No partial queue, selection or routing data accompanies a failed proof.
	if encodeErr := json.NewEncoder(os.Stdout).Encode(struct {
		Usage sourceGraphQLUsage `json:"source_graphql_usage"`
	}{Usage: usage}); encodeErr != nil {
		fail(encodeErr)
	}
	if usage.Reason == "allowance_exhausted" && !usage.UncertainCost && usage.UnprovedAttempts == 0 &&
		usage.PhysicalAttempts > 0 && usage.PhysicalAttempts == usage.PreviewAttempts+usage.DataAttempts &&
		usage.PreviewAttempts >= usage.DataAttempts && usage.PreviewAttempts-usage.DataAttempts <= 1 &&
		usage.ChargedUpperBound == usage.ObservedActualCost && usage.ChargedUpperBound <= usage.Allowance {
		_, _ = fmt.Fprintf(os.Stderr, "pipelinehealth: %v\n", err)
		os.Exit(75) // Exact clean finite allowance refusal, never Source admission.
	}
	fail(err)
}
