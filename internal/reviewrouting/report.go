package reviewrouting

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	headPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hashPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	blockerPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	loginPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	idPattern      = regexp.MustCompile(`^[1-9][0-9]{0,127}$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type Inspection struct {
	Classification  string `json:"classification"`
	BlockingCount   int    `json:"blocking_count"`
	ReportSHA256    string `json:"report_sha256"`
	CanonicalRecord string `json:"canonical_record"`
}

func object(value any, fields string) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok || len(m) != len(strings.Fields(fields)) {
		return nil, fmt.Errorf("invalid routing object fields: expected %s", fields)
	}
	for _, key := range strings.Fields(fields) {
		if _, found := m[key]; !found {
			return nil, fmt.Errorf("missing routing field %s", key)
		}
	}
	return m, nil
}

func matches(value any, pattern *regexp.Regexp) bool {
	text, ok := value.(string)
	return ok && pattern.MatchString(text)
}

func integer(value any, low, high int64) (int64, error) {
	var number int64
	switch v := value.(type) {
	case json.Number:
		if !integerPattern.MatchString(string(v)) {
			return 0, fmt.Errorf("routing field must be an integer token")
		}
		var err error
		number, err = strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return 0, err
		}
	case int:
		number = int64(v)
	case int64:
		number = v
	default:
		return 0, fmt.Errorf("routing field must be an integer")
	}
	if number < low || number > high {
		return 0, fmt.Errorf("routing integer outside allowed range")
	}
	return number, nil
}

func reportText(value any) bool {
	text, ok := value.(string)
	return ok && utf8.ValidString(text) && trimPythonSpace(text) != "" && !strings.ContainsRune(text, 0)
}

func trimPythonSpace(text string) string {
	return strings.TrimFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
}

func wireText(value any) bool {
	text, ok := value.(string)
	if !ok || text == "" || !utf8.ValidString(text) || trimPythonSpace(text) != text {
		return false
	}
	for _, r := range text {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func texts(value any, fields string) error {
	m, err := object(value, fields)
	if err != nil {
		return err
	}
	for key, text := range m {
		if !reportText(text) {
			return fmt.Errorf("routing evidence %s requires nonempty text", key)
		}
	}
	return nil
}

func product(value any) error {
	m, err := object(value, "options recommendation")
	if err != nil {
		return err
	}
	options, ok := m["options"].([]any)
	if !ok || len(options) < 2 {
		return fmt.Errorf("product blocker needs two explicit options")
	}
	numbers := map[int64]bool{}
	for _, raw := range options {
		option, err := object(raw, "number behavior consequences")
		if err != nil {
			return err
		}
		n, err := integer(option["number"], 1, 2147483647)
		if err != nil {
			return err
		}
		if numbers[n] || !reportText(option["behavior"]) || !reportText(option["consequences"]) {
			return fmt.Errorf("ambiguous or incomplete product option")
		}
		numbers[n] = true
	}
	recommendation, err := object(m["recommendation"], "number reason")
	if err != nil {
		return err
	}
	n, err := integer(recommendation["number"], 1, 2147483647)
	if err != nil {
		return err
	}
	if !numbers[n] || !reportText(recommendation["reason"]) {
		return fmt.Errorf("product recommendation must select a listed option")
	}
	return nil
}

// Inspect validates explicit evidence categories; prose cannot upgrade them to
// technical. The expected HEAD is data from the caller, not an authenticated read.
func Inspect(value any, head string) (Inspection, error) {
	var result Inspection
	m, err := object(value, "routing_version head change checks blocking tail")
	if err != nil {
		return result, err
	}
	if m["routing_version"] != Version || !headPattern.MatchString(head) || m["head"] != head || !reportText(m["change"]) {
		return result, fmt.Errorf("invalid routing report version, HEAD or change")
	}
	checks, ok := m["checks"].([]any)
	if !ok || len(checks) == 0 {
		return result, fmt.Errorf("routing report needs performed checks")
	}
	for _, check := range checks {
		if !reportText(check) {
			return result, fmt.Errorf("routing report check is empty")
		}
	}
	blockers, ok := m["blocking"].([]any)
	if !ok {
		return result, fmt.Errorf("routing blockers must be an array")
	}
	tail, ok := m["tail"].([]any)
	if !ok {
		return result, fmt.Errorf("routing tail must be an array")
	}
	ids, kinds := map[string]bool{}, map[string]bool{}
	for _, raw := range blockers {
		blocker, err := object(raw, "id kind summary evidence")
		if err != nil {
			return result, err
		}
		id, ok := blocker["id"].(string)
		if !ok || !blockerPattern.MatchString(id) || ids[id] || !reportText(blocker["summary"]) {
			return result, fmt.Errorf("duplicate/invalid blocker ID or missing summary")
		}
		ids[id] = true
		kind, _ := blocker["kind"].(string)
		switch kind {
		case "technical":
			err = texts(blocker["evidence"], "entrypoint environment command expected actual result invariant")
		case "product":
			err = product(blocker["evidence"])
		case "safety_exception":
			err = texts(blocker["evidence"], "boundary requested_exception")
		case "insufficient_data":
			err = texts(blocker["evidence"], "missing verification")
		default:
			err = fmt.Errorf("unknown blocker kind; technical category cannot be inferred")
		}
		if err != nil {
			return result, err
		}
		kinds[kind] = true
	}
	for _, raw := range tail {
		item, ok := raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("routing tail item must be an object")
		}
		fields := "kind text"
		if _, found := item["title"]; found {
			fields += " title"
		}
		if _, err := object(item, fields); err != nil {
			return result, err
		}
		if item["kind"] != "issue" && item["kind"] != "discard" || !reportText(item["text"]) {
			return result, fmt.Errorf("unknown or incomplete routing tail item")
		}
		if item["kind"] == "issue" || len(item) == 3 {
			if !reportText(item["title"]) {
				return result, fmt.Errorf("routing tail requires a title")
			}
		}
	}
	canonical, err := Canonical(m)
	if err != nil {
		return result, err
	}
	if len(canonical) > MaxReportBytes {
		return result, fmt.Errorf("routing report exceeds 256 KiB")
	}
	digest, err := Digest(m)
	if err != nil {
		return result, err
	}
	classification := "mixed_blockers"
	if len(kinds) == 0 {
		classification = "no_blockers"
	} else if len(kinds) == 1 {
		for kind := range kinds {
			classification = kind
		}
	}
	return Inspection{classification, len(blockers), digest, string(canonical)}, nil
}

func Outcome(report any, depth, budget, limit int64) (string, error) {
	if depth < 0 || depth > 2147483647 || budget < 0 || budget > 16 || limit < 1 || limit > 16 {
		return "", fmt.Errorf("routing outcome bounds are invalid")
	}
	m, ok := report.(map[string]any)
	if !ok {
		return "", fmt.Errorf("routing report must be an object")
	}
	head, _ := m["head"].(string)
	inspection, err := Inspect(m, head)
	if err != nil {
		return "", err
	}
	if inspection.BlockingCount == 0 {
		return "reviewed", nil
	}
	if depth < 2 {
		return "changes-requested", nil
	}
	for _, raw := range m["blocking"].([]any) {
		kind := raw.(map[string]any)["kind"]
		if kind == "product" || kind == "safety_exception" {
			return "needs-decision", nil
		}
	}
	if inspection.Classification == "technical" && budget < limit {
		return "technical-handoff", nil
	}
	return "engineering-handoff", nil
}
