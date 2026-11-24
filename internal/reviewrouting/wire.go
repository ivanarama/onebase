package reviewrouting

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var (
	markerPattern     = regexp.MustCompile(`^<!-- pp:review-routing-v1 kind=([a-z-]+) sha256=([0-9a-f]{64}) -->\n`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

func validatePR(value any) error {
	m, err := object(value, "base_repository_id pr_id pr_number")
	if err != nil {
		return err
	}
	if !wireText(m["base_repository_id"]) || !wireText(m["pr_id"]) {
		return fmt.Errorf("invalid routing lifetime PR identity")
	}
	_, err = integer(m["pr_number"], 1, 2147483647)
	return err
}

// IdentityDigest uses the fixed PRIdentityV1 field order, not JSON key order.
func IdentityDigest(value any) (string, error) {
	fields := "base_repository_id base_repository base_ref pr_id pr_number head_repository_id head_repository head_ref head_sha cross_repository"
	m, err := object(value, fields)
	if err != nil {
		return "", err
	}
	for key, text := range m {
		if key != "pr_number" && key != "cross_repository" && !identityText(text) {
			return "", fmt.Errorf("invalid PR identity text %s", key)
		}
	}
	number, err := integer(m["pr_number"], 1, 2147483647)
	if err != nil {
		return "", err
	}
	cross, ok := m["cross_repository"].(bool)
	if !ok {
		return "", fmt.Errorf("invalid cross-repository flag")
	}
	if m["base_ref"] != "main" || !matches(m["head_sha"], headPattern) || !matches(m["base_repository"], repositoryPattern) || !matches(m["head_repository"], repositoryPattern) {
		return "", fmt.Errorf("invalid PR identity repository/ref/HEAD")
	}
	sameID := m["base_repository_id"] == m["head_repository_id"]
	sameName := m["base_repository"] == m["head_repository"]
	if sameID != sameName || cross == sameID {
		return "", fmt.Errorf("inconsistent PR identity repositories")
	}
	var text strings.Builder
	text.WriteString("pp-pr-identity-v1\n")
	for _, key := range strings.Fields(fields) {
		var value string
		switch key {
		case "pr_number":
			value = fmt.Sprint(number)
		case "cross_repository":
			value = fmt.Sprint(cross)
		default:
			value = m[key].(string)
		}
		fmt.Fprintf(&text, "%s=%s\n", strings.ReplaceAll(key, "_", "-"), value)
	}
	sum := sha256.Sum256([]byte(text.String()))
	return hex.EncodeToString(sum[:]), nil
}

func identityText(value any) bool {
	text, ok := value.(string)
	if !ok || text == "" {
		return false
	}
	for _, r := range text {
		if r < 32 || r == 127 {
			return false
		}
	}
	// Canonical performs the remaining strict UTF-8 check.
	_, err := Canonical(text)
	return err == nil
}

func source(value any) error {
	m, err := object(value, "publication_id claim_id completion_id head identity_sha256 epoch report_sha256")
	if err != nil {
		return err
	}
	for _, key := range []string{"publication_id", "claim_id", "completion_id"} {
		if !matches(m[key], idPattern) {
			return fmt.Errorf("invalid source comment ID")
		}
	}
	if !matches(m["head"], headPattern) {
		return fmt.Errorf("invalid source HEAD")
	}
	for _, key := range []string{"identity_sha256", "epoch", "report_sha256"} {
		if !matches(m[key], hashPattern) {
			return fmt.Errorf("invalid source digest %s", key)
		}
	}
	return nil
}

func audit(value any) error {
	m := mapValue(value)
	fields := "snapshot_sha256 last_edge_cursor last_edge_node_id base_ref_oid"
	if _, ok := m["history"]; ok {
		fields += " history"
	}
	m, err := object(value, fields)
	if err != nil {
		return err
	}
	if !matches(m["snapshot_sha256"], hashPattern) || !matches(m["base_ref_oid"], headPattern) {
		return fmt.Errorf("invalid audit hashes")
	}
	if (m["last_edge_cursor"] == nil) != (m["last_edge_node_id"] == nil) {
		return fmt.Errorf("audit boundary needs both cursor and node ID")
	}
	if m["last_edge_cursor"] != nil && (!wireText(m["last_edge_cursor"]) || !wireText(m["last_edge_node_id"])) {
		return fmt.Errorf("invalid audit boundary")
	}
	if retained, ok := m["history"]; ok {
		history, err := object(retained, "protocol header edges")
		if err != nil {
			return err
		}
		if history["protocol"] != "audit-history-v1" {
			return fmt.Errorf("unsupported retained audit history")
		}
		if _, err := object(history["header"], "headRefOid baseRefName state isDraft identity identity_sha256 pr_key author labels labelsComplete"); err != nil {
			return err
		}
		header := mapValue(history["header"])
		original := map[string]any{}
		for key, value := range header {
			original[key] = value
		}
		original["baseRefOid"], original["updatedAt"], original["edges"] = m["base_ref_oid"], nil, []any{}
		identity := mapValue(header["identity"])
		number, err := integer(identity["pr_number"], 1, 2147483647)
		if err != nil {
			return err
		}
		if err := ValidateSnapshot(original, stringValue(identity["base_repository"]), number, stringValue(header["headRefOid"])); err != nil {
			return fmt.Errorf("invalid retained original audit header: %w", err)
		}
		if header["state"] != "OPEN" || header["isDraft"] != false || header["labelsComplete"] != true {
			return fmt.Errorf("retained original audit was not open, non-draft and complete")
		}
		manifest, ok := history["edges"].([]any)
		if !ok || len(manifest) > 10000 {
			return fmt.Errorf("retained audit edge manifest is invalid or oversized")
		}
		cursors, ids := map[string]bool{}, map[string]bool{}
		var cursor, id any
		for _, raw := range manifest {
			edge, err := object(raw, "cursor node_id")
			if err != nil {
				return err
			}
			if !wireText(edge["cursor"]) || !wireText(edge["node_id"]) {
				return fmt.Errorf("invalid retained audit server identity")
			}
			cursor, id = edge["cursor"], edge["node_id"]
			if cursors[cursor.(string)] || ids[id.(string)] {
				return fmt.Errorf("duplicate retained audit server identity")
			}
			cursors[cursor.(string)], ids[id.(string)] = true, true
		}
		if cursor != m["last_edge_cursor"] || id != m["last_edge_node_id"] {
			return fmt.Errorf("retained audit manifest differs from its boundary")
		}
	}
	return nil
}

// Validate checks the closed wire schema only. Matching a source ID/hash does
// not prove a canonical publication, winning lease, stopped executor or delivery.
func Validate(value any) error {
	m, ok := value.(map[string]any)
	if !ok || m["protocol"] != Version {
		return fmt.Errorf("unsupported routing wire protocol")
	}
	fields := map[string]string{
		"publication": "owner identity author epoch anchor audit depth budget_used max_attempts report report_sha256 outcome resume_id",
		"lease":       "owner context_sha256 purpose root previous",
		"claim":       "owner context_sha256 publication_id lease_id",
		"completion":  "owner context_sha256 publication_id claim_id lease_id",
		"attempt":     "owner context_sha256 source series previous attempt executor",
		"phase":       "owner context_sha256 attempt_id lease_id phase details",
		"resume":      "head parking_id previous reason",
		"settlement":  "owner context_sha256 attempt_id lease_id source barrier_id started_id observed_head reason executor_outcome evidence_sha256",
	}
	kind, _ := m["kind"].(string)
	required, ok := fields[kind]
	if !ok {
		return fmt.Errorf("unsupported routing wire kind")
	}
	if _, err := object(m, "protocol kind pr "+required); err != nil {
		return err
	}
	if err := validatePR(m["pr"]); err != nil {
		return err
	}
	if kind != "resume" && !matches(m["owner"], uuidPattern) {
		return fmt.Errorf("routing owner must be canonical UUID4")
	}
	if kind != "publication" && kind != "resume" && !matches(m["context_sha256"], hashPattern) {
		return fmt.Errorf("invalid routing context hash")
	}
	if kind == "publication" {
		if m["resume_id"] != nil && !matches(m["resume_id"], idPattern) {
			return fmt.Errorf("invalid publication resume ID")
		}
		if _, err := IdentityDigest(m["identity"]); err != nil {
			return err
		}
		identity := m["identity"].(map[string]any)
		pr, _ := m["pr"].(map[string]any)
		for _, key := range []string{"base_repository_id", "pr_id", "pr_number"} {
			left, err := Canonical(pr[key])
			if err != nil {
				return err
			}
			right, err := Canonical(identity[key])
			if err != nil {
				return err
			}
			if !bytes.Equal(left, right) {
				return fmt.Errorf("publication belongs to another lifetime PR identity")
			}
		}
		author, err := object(m["author"], "id login")
		if err != nil {
			return err
		}
		if !wireText(author["id"]) || !matches(author["login"], loginPattern) || !matches(m["epoch"], hashPattern) || !wireText(m["anchor"]) {
			return fmt.Errorf("invalid publication author/epoch/anchor")
		}
		if err := audit(m["audit"]); err != nil {
			return err
		}
		inspection, err := Inspect(m["report"], identity["head_sha"].(string))
		if err != nil {
			return err
		}
		if m["report_sha256"] != inspection.ReportSHA256 {
			return fmt.Errorf("publication report hash differs")
		}
		depth, err := integer(m["depth"], 0, 2147483647)
		if err != nil {
			return err
		}
		budget, err := integer(m["budget_used"], 0, 16)
		if err != nil {
			return err
		}
		limit, err := integer(m["max_attempts"], 1, 16)
		if err != nil {
			return err
		}
		expected, err := Outcome(m["report"], depth, budget, limit)
		if err != nil {
			return err
		}
		if m["outcome"] != expected {
			return fmt.Errorf("publication outcome differs from explicit evidence and lifetime budget")
		}
		return nil
	}
	idFields := ""
	switch kind {
	case "lease":
		if m["purpose"] != "review-publication" && m["purpose"] != "technical-attempt" {
			return fmt.Errorf("invalid lease purpose")
		}
		idFields = "root previous"
	case "claim":
		idFields = "publication_id lease_id"
	case "completion":
		idFields = "publication_id claim_id lease_id"
	case "attempt":
		if err := source(m["source"]); err != nil {
			return err
		}
		if m["previous"] != nil && !matches(m["previous"], idPattern) {
			return fmt.Errorf("invalid previous attempt")
		}
		if _, err := integer(m["attempt"], 1, 16); err != nil {
			return err
		}
		executor, err := object(m["executor"], "kind login")
		if err != nil {
			return err
		}
		if executor["kind"] != "author" && executor["kind"] != "pipeline" || !matches(executor["login"], loginPattern) {
			return fmt.Errorf("invalid executor")
		}
		idFields = "series"
	case "phase":
		idFields = "attempt_id lease_id"
		switch m["phase"] {
		case "assigned", "handoff-done":
			if _, err := object(m["details"], ""); err != nil {
				return err
			}
		case "started":
			details, err := object(m["details"], "task_id attempt_uuid")
			if err != nil {
				return err
			}
			if _, err := integer(details["task_id"], 1, 9223372036854775807); err != nil {
				return err
			}
			if !matches(details["attempt_uuid"], uuidPattern) {
				return fmt.Errorf("invalid execution attempt UUID")
			}
		case "fix-done":
			details, err := object(m["details"], "new_head transition source_trailer")
			if err != nil {
				return err
			}
			if !matches(details["new_head"], headPattern) || !wireText(details["transition"]) || !matches(details["source_trailer"], hashPattern) {
				return fmt.Errorf("invalid fix-done details")
			}
		case "barrier":
			details, err := object(m["details"], "reason")
			if err != nil {
				return err
			}
			if !wireText(details["reason"]) {
				return fmt.Errorf("missing barrier reason")
			}
		default:
			return fmt.Errorf("unsupported routing phase")
		}
	case "resume":
		idFields = "parking_id"
		if !matches(m["head"], headPattern) || !wireText(m["reason"]) || m["previous"] != nil && !matches(m["previous"], idPattern) {
			return fmt.Errorf("invalid routing resume")
		}
	case "settlement":
		idFields = "attempt_id lease_id"
		if err := source(m["source"]); err != nil {
			return err
		}
		for _, key := range []string{"barrier_id", "started_id"} {
			if m[key] != nil && !matches(m[key], idPattern) {
				return fmt.Errorf("invalid settlement source ID")
			}
		}
		if !matches(m["observed_head"], headPattern) || !matches(m["evidence_sha256"], hashPattern) || !wireText(m["reason"]) {
			return fmt.Errorf("invalid settlement evidence")
		}
		if m["executor_outcome"] != "not-started" && m["executor_outcome"] != "pipeline-stopped" && m["executor_outcome"] != "author-unassigned" {
			return fmt.Errorf("settlement lacks explicit executor outcome")
		}
	}
	for _, key := range strings.Fields(idFields) {
		if !matches(m[key], idPattern) {
			return fmt.Errorf("invalid routing comment ID %s", key)
		}
	}
	return nil
}

func Render(value any) (string, error) {
	if err := Validate(value); err != nil {
		return "", err
	}
	data, err := Canonical(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	body := fmt.Sprintf("<!-- pp:review-routing-v1 kind=%s sha256=%x -->\n%s", value.(map[string]any)["kind"], sum, data)
	if len(body) > MaxWireBytes {
		return "", fmt.Errorf("routing wire exceeds comment limit; evidence was not truncated")
	}
	return body, nil
}

func Parse(body string) (map[string]any, error) {
	if len(body) > MaxWireBytes {
		return nil, fmt.Errorf("routing wire exceeds comment limit")
	}
	marker := markerPattern.FindStringSubmatchIndex(body)
	if marker == nil {
		return nil, fmt.Errorf("malformed routing wire marker")
	}
	value, err := Decode([]byte(body[marker[1]:]), MaxWireBytes)
	if err != nil {
		return nil, err
	}
	encoded, err := Render(value)
	if err != nil {
		return nil, err
	}
	if body != encoded {
		return nil, fmt.Errorf("routing wire is not exact canonical encoding")
	}
	return value.(map[string]any), nil
}

func Context(value any) (string, error) {
	if err := Validate(value); err != nil {
		return "", err
	}
	m := value.(map[string]any)
	if m["kind"] != "publication" && m["kind"] != "attempt" {
		return "", fmt.Errorf("only routing roots define contexts")
	}
	projection := make(map[string]any, len(m)-1)
	for key, item := range m {
		if key != "owner" {
			projection[key] = item
		}
	}
	return Digest(projection)
}
