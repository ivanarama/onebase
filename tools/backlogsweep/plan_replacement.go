package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
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
		var fence byte
		var fenceLength int
		for j, line := range lines {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
				n := 0
				for n < len(trimmed) && trimmed[n] == trimmed[0] {
					n++
				}
				if n >= 3 {
					if fence == 0 {
						fence, fenceLength = trimmed[0], n
					} else if trimmed[0] == fence && n >= fenceLength && strings.TrimSpace(trimmed[n:]) == "" {
						fence = 0
					}
					continue
				}
			}
			if fence != 0 {
				continue
			}
			match := planReplacementLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
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
