package nextslice

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	decimal    = regexp.MustCompile(`^[1-9][0-9]*$`)
	hex40      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uuid       = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	rootEnd    = regexp.MustCompile(`^<!-- pp:next-slice-claim fingerprint-sha256=([0-9a-f]{64}) owner=(` + uuid + `) -->$`)
	leaseLine  = regexp.MustCompile(`^<!-- pp:next-slice-lease claim=([1-9][0-9]*) previous=([1-9][0-9]*) owner=(` + uuid + `) -->$`)
	labelsLine = regexp.MustCompile(`^<!-- pp:next-slice-labels claim=([1-9][0-9]*) fingerprint-sha256=([0-9a-f]{64}) events-through=([1-9][0-9]*|none) labels-sha256=([0-9a-f]{64}) -->$`)
	doneLine   = regexp.MustCompile(`^<!-- pp:next-slice-done claim=([1-9][0-9]*) fingerprint-sha256=([0-9a-f]{64}) -->$`)
)

type Declaration struct{ Issue, PlanPath, PlanSHA256, Slice, Next, Previous, Head, BodySHA256 string }
type Root struct {
	Issue, PlanPath, PlanSHA256, Completed, Next, PR, Head, Merge, Declaration, Previous string
	Fingerprint, Owner, Payload                                                          string
}
type Phase struct{ Kind, Claim, Previous, Owner, Fingerprint, Watermark, LabelsSHA256 string }

// exactFields accepts one version, exact field order and one optional final LF.
// IDs stay decimal strings, never floating point or platform-sized integers.
func exactFields(body, header string, keys []string) ([]string, string, error) {
	if !validText(body) {
		return nil, "", fmt.Errorf("invalid record bytes")
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != len(keys)+2 || lines[0] != header {
		return nil, "", fmt.Errorf("wrong version or field count")
	}
	values := make([]string, len(keys))
	for i, k := range keys {
		p := k + "="
		if !strings.HasPrefix(lines[i+1], p) {
			return nil, "", fmt.Errorf("wrong field order: %s", k)
		}
		values[i] = strings.TrimPrefix(lines[i+1], p)
		if values[i] == "" {
			return nil, "", fmt.Errorf("empty field")
		}
	}
	return values, lines[len(lines)-1], nil
}
func validPrevious(s string) bool { return s == "none" || decimal.MatchString(s) }
func ParseDeclaration(body string) (Declaration, error) {
	v, end, err := exactFields(body, "pp-slice-declaration-v1", []string{"issue", "plan-path", "plan-sha256", "slice", "next", "previous-handoff", "head", "body-sha256"})
	if err != nil {
		return Declaration{}, err
	}
	d := Declaration{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}
	if end != "<!-- pp:slice-declaration -->" || !decimal.MatchString(d.Issue) || !planPath.MatchString(d.PlanPath) || !hex64.MatchString(d.PlanSHA256) || !token.MatchString(d.Slice) || d.Slice == "done" || !token.MatchString(d.Next) || !validPrevious(d.Previous) || !hex40.MatchString(d.Head) || !hex64.MatchString(d.BodySHA256) {
		return Declaration{}, fmt.Errorf("invalid declaration field")
	}
	return d, nil
}
func VerifyDeclaration(d Declaration, m Manifest, issue, head, body string) error {
	next, ok := m.Next(d.Slice)
	if !ok || d.Issue != issue || d.PlanPath != m.Path || d.PlanSHA256 != m.SHA256 || d.Next != next || d.Head != head || d.BodySHA256 != Hash([]byte(body)) {
		return fmt.Errorf("declaration does not match plan, issue, HEAD or body")
	}
	for _, line := range []string{"Slice-Issue: #" + issue, "Slice-Plan: " + m.Path, "Slice-Key: " + d.Slice, "Next-Slice: " + d.Next} {
		count := 0
		prefix := strings.SplitN(line, ":", 2)[0] + ":"
		for _, actual := range strings.Split(body, "\n") {
			if strings.HasPrefix(actual, prefix) {
				if actual != line {
					return fmt.Errorf("conflicting body fields")
				}
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("missing or duplicate slice body fields")
		}
	}
	closing := regexp.MustCompile(`(?im)\b(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+(?:(?:https://github\.com/ivanarama/onebase/issues/)|(?:ivanarama/onebase)?#)` + issue + `(?:\b|$)`)
	count := len(closing.FindAllStringIndex(body, -1))
	part := regexp.MustCompile(`(?im)^Part of #` + issue + `$`).MatchString(body)
	if d.Next != "done" && (count != 0 || !part) {
		return fmt.Errorf("partial slice requires Part of without closing the leading issue")
	}
	if d.Next == "done" && count != 1 {
		return fmt.Errorf("final slice requires exactly one closing reference")
	}
	return nil
}
func ParseRoot(body string) (Root, error) {
	v, end, err := exactFields(body, "pp-next-slice-v1", []string{"issue", "plan-path", "plan-sha256", "completed-slice", "next-slice", "pr", "head", "merge", "declaration", "previous-handoff"})
	if err != nil {
		return Root{}, err
	}
	marker := rootEnd.FindStringSubmatch(end)
	if len(marker) != 3 {
		return Root{}, fmt.Errorf("invalid root marker")
	}
	r := Root{Issue: v[0], PlanPath: v[1], PlanSHA256: v[2], Completed: v[3], Next: v[4], PR: v[5], Head: v[6], Merge: v[7], Declaration: v[8], Previous: v[9], Fingerprint: marker[1], Owner: marker[2]}
	if !decimal.MatchString(r.Issue) || !planPath.MatchString(r.PlanPath) || !hex64.MatchString(r.PlanSHA256) || !token.MatchString(r.Completed) || r.Completed == "done" || !token.MatchString(r.Next) || r.Next == "done" || !decimal.MatchString(r.PR) || !hex40.MatchString(r.Head) || !hex40.MatchString(r.Merge) || !decimal.MatchString(r.Declaration) || !validPrevious(r.Previous) {
		return Root{}, fmt.Errorf("invalid root field")
	}
	r.Payload = strings.TrimSuffix(body, "\n")
	r.Payload = r.Payload[:strings.LastIndex(r.Payload, "\n")+1]
	return r, nil
}

// RootFingerprint freezes the decision serialization: root payload through
// previous-handoff LF, followed by decision-sha256=<hash> LF. No JSON involved.
func RootFingerprint(payload, decision string) (string, error) {
	if !hex64.MatchString(decision) {
		return "", fmt.Errorf("invalid decision digest")
	}
	return Hash([]byte(payload + "decision-sha256=" + decision + "\n")), nil
}
func ParsePhase(body string) (Phase, error) {
	if !validText(body) {
		return Phase{}, fmt.Errorf("invalid phase bytes")
	}
	body = strings.TrimSuffix(body, "\n")
	if m := leaseLine.FindStringSubmatch(body); m != nil {
		return Phase{Kind: "lease", Claim: m[1], Previous: m[2], Owner: m[3]}, nil
	}
	if m := labelsLine.FindStringSubmatch(body); m != nil {
		return Phase{Kind: "labels", Claim: m[1], Fingerprint: m[2], Watermark: m[3], LabelsSHA256: m[4]}, nil
	}
	if m := doneLine.FindStringSubmatch(body); m != nil {
		return Phase{Kind: "done", Claim: m[1], Fingerprint: m[2]}, nil
	}
	return Phase{}, fmt.Errorf("unknown or malformed phase")
}
