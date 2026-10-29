// Package nextslice defines the read-only bootstrap contract for Plan 163.
// Observations never authorize GitHub mutations or dispatch a FIX worker.
package nextslice

import (
	"crypto/sha256"
	"fmt"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	token        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)
	planPath     = regexp.MustCompile(`^Plans/[0-9]+-[a-z0-9][a-z0-9-]*\.md$`)
	manifestLine = regexp.MustCompile(`^<!-- pp:plan-slice key=([A-Za-z0-9][A-Za-z0-9._-]{0,31}) next=([A-Za-z0-9][A-Za-z0-9._-]{0,31}) -->$`)
)

type Slice struct {
	Key  string `json:"key"`
	Next string `json:"next"`
}
type Manifest struct {
	Path   string  `json:"path"`
	SHA256 string  `json:"sha256"`
	Slices []Slice `json:"slices"`
}

func Hash(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func validText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsAny(s, "\r\x00") && !strings.HasPrefix(s, "\ufeff")
}

// ParseManifest excludes code examples via the Markdown AST. Markers occupy
// a complete HTML block immediately before a heading. Hashing uses all bytes.
func ParseManifest(path string, source []byte) (Manifest, error) {
	m := Manifest{Path: path, SHA256: Hash(source)}
	if !planPath.MatchString(path) || !validText(string(source)) {
		return m, fmt.Errorf("invalid plan path or UTF-8/LF bytes")
	}
	doc := goldmark.New().Parser().Parse(text.NewReader(source))
	var parseErr error
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if raw, ok := n.(*ast.HTMLBlock); ok {
			s := strings.TrimSuffix(string(raw.Lines().Value(source)), "\n")
			if !strings.Contains(s, "pp:plan-slice") {
				return ast.WalkContinue, nil
			}
			match := manifestLine.FindStringSubmatch(s)
			if len(match) != 3 {
				parseErr = fmt.Errorf("malformed plan-slice marker")
				return ast.WalkStop, nil
			}
			if _, ok := n.NextSibling().(*ast.Heading); !ok {
				parseErr = fmt.Errorf("plan-slice marker must precede a heading")
				return ast.WalkStop, nil
			}
			m.Slices = append(m.Slices, Slice{match[1], match[2]})
		}
		if raw, ok := n.(*ast.RawHTML); ok && strings.Contains(string(raw.Segments.Value(source)), "pp:plan-slice") {
			parseErr = fmt.Errorf("plan-slice marker must be a separate block")
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return m, err
	}
	if parseErr != nil {
		return m, parseErr
	}
	if len(m.Slices) < 2 {
		return m, fmt.Errorf("multi-slice manifest requires at least two markers")
	}
	next := map[string]string{}
	incoming := map[string]int{}
	done := 0
	for _, s := range m.Slices {
		if s.Key == "done" || next[s.Key] != "" {
			return m, fmt.Errorf("duplicate or reserved key")
		}
		next[s.Key] = s.Next
		incoming[s.Next]++
		if s.Next == "done" {
			done++
		}
	}
	start := ""
	for k := range next {
		if incoming[k] == 0 {
			if start != "" {
				return m, fmt.Errorf("multiple initial slices")
			}
			start = k
		} else if incoming[k] != 1 {
			return m, fmt.Errorf("multiple predecessors")
		}
	}
	if start == "" || done != 1 {
		return m, fmt.Errorf("missing initial slice or unique done")
	}
	seen := map[string]bool{}
	ordered := []Slice{}
	for k := start; k != "done"; {
		v, ok := next[k]
		if !ok || seen[k] {
			return m, fmt.Errorf("cycle or unknown next")
		}
		seen[k] = true
		ordered = append(ordered, Slice{k, v})
		k = v
	}
	if len(seen) != len(next) {
		return m, fmt.Errorf("disconnected chain")
	}
	m.Slices = ordered
	return m, nil
}
func (m Manifest) Next(key string) (string, bool) {
	for _, s := range m.Slices {
		if s.Key == key {
			return s.Next, true
		}
	}
	return "", false
}
