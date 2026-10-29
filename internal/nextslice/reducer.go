package nextslice

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Comment carries server facts. The caller must obtain complete, stable
// snapshots, including deletion fences, before using a future mutation gate.
// This bootstrap reducer is deliberately diagnostic even when every check passes.
type Comment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	Edited    bool   `json:"edited"`
	Deleted   bool   `json:"deleted"`
	Body      string `json:"body"`
}
type Observation struct {
	Issue             string    `json:"issue"`
	PR                string    `json:"pr"`
	Head              string    `json:"head"`
	Merge             string    `json:"merge"`
	Body              string    `json:"body"`
	Previous          string    `json:"previous"`
	DecisionSHA256    string    `json:"decision_sha256"`
	Complete          bool      `json:"complete"`
	Open              bool      `json:"open"`
	Merged            bool      `json:"merged"`
	MergeEvents       int       `json:"merge_events"`
	Held              bool      `json:"held"`
	HumanIntervention bool      `json:"human_intervention"`
	LabelsSHA256      string    `json:"labels_sha256"`
	Declarations      []Comment `json:"declarations"`
	Comments          []Comment `json:"comments"`
}
type Result struct {
	State         string `json:"state"`
	Reason        string `json:"reason"`
	Claim         string `json:"claim,omitempty"`
	Next          string `json:"next,omitempty"`
	Authoritative bool   `json:"authoritative"`
	Executable    bool   `json:"executable"`
}

func human(reason string) Result { return Result{State: "human", Reason: reason} }
func beforeID(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
func trusted(c Comment, owner string) bool {
	return c.Author == owner && !c.Edited && !c.Deleted && decimal.MatchString(c.ID)
}
func ordered(comments []Comment) ([]Comment, error) {
	out := append([]Comment(nil), comments...)
	seen := map[string]bool{}
	for _, c := range out {
		if !decimal.MatchString(c.ID) || seen[c.ID] {
			return nil, fmt.Errorf("invalid or duplicate comment id")
		}
		seen[c.ID] = true
		if _, err := time.Parse(time.RFC3339Nano, c.CreatedAt); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return beforeID(out[i].ID, out[j].ID)
		}
		a, _ := time.Parse(time.RFC3339Nano, out[i].CreatedAt)
		b, _ := time.Parse(time.RFC3339Nano, out[j].CreatedAt)
		return a.Before(b)
	})
	return out, nil
}

// Reduce never returns a FIX candidate. Even a complete committed fixture is
// reported as bootstrap-disabled until slices B/C deliver the mutation gates.
func Reduce(m Manifest, o Observation, owner string, now time.Time) Result {
	if owner == "" || !decimal.MatchString(o.Issue) || !decimal.MatchString(o.PR) || !validPrevious(o.Previous) || len(m.Slices) < 2 {
		return human("invalid observation identity or manifest")
	}
	if o.Held || o.HumanIntervention {
		return human("hold, manual or human intervention")
	}
	if !o.Complete {
		return human("incomplete immutable evidence")
	}
	if o.Open {
		return Result{State: "active", Reason: "open slice PR"}
	}
	if !o.Merged {
		return human("closed without merge or active PR unavailable")
	}
	if o.MergeEvents != 1 || !hex40.MatchString(o.Head) || !hex40.MatchString(o.Merge) {
		return human("unique MergedEvent and exact HEAD/merge required")
	}
	var declaration *Declaration
	declarationID := ""
	for _, c := range o.Declarations {
		if c.Author != owner {
			continue
		}
		if !trusted(c, owner) {
			return human("edited or deleted declaration")
		}
		d, err := ParseDeclaration(c.Body)
		if err != nil {
			return human("malformed declaration")
		}
		if err = VerifyDeclaration(d, m, o.Issue, o.Head, o.Body); err != nil {
			return human(err.Error())
		}
		if declaration != nil {
			return human("multiple slice declarations require an explicit review epoch")
		}
		declaration = &d
		declarationID = c.ID
	}
	if declaration == nil {
		if !regexp.MustCompile(`(?im)^Part of #` + o.Issue + `$`).MatchString(o.Body) {
			return human("legacy PR has no explicit partial intent")
		}
		return Result{State: "legacy-bootstrap-required", Reason: "legacy bootstrap required: merged partial PR has no immutable declaration"}
	}
	if declaration.Previous != o.Previous {
		return human("previous handoff mismatch")
	}
	if declaration.Next == "done" {
		return Result{State: "final", Reason: "final slice observed; no next handoff"}
	}
	comments, err := ordered(o.Comments)
	if err != nil {
		return human("invalid server comment order: " + err.Error())
	}
	var root *Root
	var active Comment
	claim := ""
	var labels *Phase
	done := false
	for _, c := range comments {
		if c.Author != owner {
			continue
		}
		protocol := strings.HasPrefix(c.Body, "pp-next-slice-v1") || strings.Contains(c.Body, "pp:next-slice-")
		if !protocol {
			continue
		}
		if !trusted(c, owner) {
			return human("edited or deleted handoff evidence")
		}
		if strings.HasPrefix(c.Body, "pp-next-slice-v1") {
			r, e := ParseRoot(c.Body)
			if e != nil {
				return human("malformed root")
			}
			fp, e := RootFingerprint(r.Payload, o.DecisionSHA256)
			if e != nil || fp != r.Fingerprint || r.Issue != o.Issue || r.PlanPath != m.Path || r.PlanSHA256 != m.SHA256 || r.Completed != declaration.Slice || r.Next != declaration.Next || r.PR != o.PR || r.Head != o.Head || r.Merge != o.Merge || r.Declaration != declarationID || r.Previous != o.Previous {
				return human("root fingerprint or immutable source mismatch")
			}
			if root != nil {
				if r.Fingerprint != root.Fingerprint {
					return human("conflicting roots")
				}
				continue
			}
			root = &r
			claim = c.ID
			active = c
			continue
		}
		p, e := ParsePhase(c.Body)
		if e != nil {
			return human("unknown or malformed handoff phase")
		}
		if root == nil || p.Claim != claim {
			return human("phase references a missing or noncanonical root")
		}
		if p.Kind == "lease" {
			if p.Previous != active.ID {
				continue
			} // losing sibling never owns the lease
			prior, _ := time.Parse(time.RFC3339Nano, active.CreatedAt)
			at, _ := time.Parse(time.RFC3339Nano, c.CreatedAt)
			if at.Before(prior.Add(30*time.Minute)) && p.Owner != root.Owner {
				continue
			}
			active = c
			root.Owner = p.Owner
			continue
		}
		activeAt, _ := time.Parse(time.RFC3339Nano, active.CreatedAt)
		phaseAt, _ := time.Parse(time.RFC3339Nano, c.CreatedAt)
		if !phaseAt.Before(activeAt.Add(30 * time.Minute)) {
			return human("phase after lease expiry")
		}
		if p.Fingerprint != root.Fingerprint {
			return human("phase fingerprint mismatch")
		}
		if p.Kind == "labels" {
			if labels != nil {
				return human("duplicate labels phase")
			}
			if p.LabelsSHA256 != o.LabelsSHA256 {
				return human("labels changed after handoff")
			}
			labels = &p
			continue
		}
		if labels == nil || done {
			return human("done before labels or duplicate done")
		}
		done = true
	}
	if root == nil {
		return Result{State: "recovery", Reason: "merged slice awaits next-slice root"}
	}
	result := Result{State: "recovery", Claim: claim, Next: root.Next}
	if labels == nil {
		result.Reason = "root recorded; labels phase missing"
	} else if !done {
		result.Reason = "labels recorded; done missing"
	} else {
		result.Reason = "committed handoff observed; bootstrap mutations disabled"
	}
	at, _ := time.Parse(time.RFC3339Nano, active.CreatedAt)
	if !now.Before(at.Add(30 * time.Minute)) {
		result.Reason += "; lease expired"
	}
	return result
}
