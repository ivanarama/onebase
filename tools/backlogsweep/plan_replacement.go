package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// A different slug is a scope replacement, never an inferred renumbering.
// This advisory declaration changes report classification only; it grants no
// authority to close an issue, remove hold or execute the replacement plan.
const replacementPlanPath = `([0-9]{1,3}-[A-Za-z0-9][A-Za-z0-9_-]*\.md)`

var planReplacementLine = regexp.MustCompile(`^[ \t]*Plan-replaces:[ \t]+Plans/` + replacementPlanPath + `[ \t]+->[ \t]+Plans/` + replacementPlanPath + `[ \t]*$`)

type planReplacement struct {
	from, to string
	at       int
}

func planRefsAndReplacements(is issue, team map[string]bool) ([]planRef, []planReplacement) {
	comments := append([]comment(nil), is.Comments...)
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	var replacements []planReplacement
	for i, c := range comments {
		if !team[c.Author.Login] {
			continue
		}
		lines := strings.Split(c.Body, "\n")
		for _, j := range plainReplacementLines(c.Body) {
			match := planReplacementLine.FindStringSubmatch(strings.TrimSuffix(lines[j], "\r"))
			if match == nil || match[1] == match[2] {
				continue
			}
			replacements = append(replacements, planReplacement{from: match[1], to: match[2], at: i + 2})
			// The declaration itself must not make the retired source the latest
			// active reference. Keep its destination in normal plan accounting.
			lines[j] = "Plans/" + match[2]
		}
		comments[i].Body = strings.Join(lines, "\n")
	}
	is.Comments = comments
	return planRefs(is), replacements
}

// Only a complete plain-text line in a top-level paragraph can declare a
// decision. CommonMark parsing also excludes indented code, lazy quote/list
// continuations, raw HTML and multiline inline code without guessing fences.
func plainReplacementLines(body string) []int {
	source := []byte(body)
	doc := goldmark.DefaultParser().Parse(text.NewReader(source))
	lines := strings.Split(body, "\n")
	var result []int
	for block := doc.FirstChild(); block != nil; block = block.NextSibling() {
		if block.Kind() != ast.KindParagraph {
			continue
		}
		for child := block.FirstChild(); child != nil; child = child.NextSibling() {
			n, ok := child.(*ast.Text)
			if !ok {
				continue
			}
			line := strings.TrimRight(string(n.Segment.Value(source)), "\r\n")
			if !planReplacementLine.MatchString(line) {
				continue
			}
			index := strings.Count(body[:n.Segment.Start], "\n")
			if strings.TrimSpace(line) == strings.TrimSpace(lines[index]) {
				result = append(result, index)
			}
		}
	}
	return result
}

func replacedBelow(replacements []planReplacement, broken planRef, p plans) (planRef, bool) {
	if broken.File == "" {
		return planRef{}, false
	}
	var candidate planRef
	for _, replacement := range replacements {
		if replacement.from != broken.File || replacement.at <= broken.At {
			continue
		}
		if candidate.File != "" && candidate.File != replacement.to {
			return planRef{}, false
		}
		number, _, _ := strings.Cut(replacement.to, "-")
		n, err := strconv.Atoi(number)
		if err != nil {
			return planRef{}, false
		}
		candidate = planRef{File: replacement.to, Num: n, At: replacement.at}
	}
	return candidate, candidate.File != "" && p.has(candidate)
}
