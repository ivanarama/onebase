package reviewrouting

import (
	"strings"
	"unicode"
)

// HasRecordPrefix distinguishes a wire-record candidate from prose and quoted
// examples. Only the body boundary can introduce a record; informational
// consumer declarations use a separate, exact existing marker format.
// Noncanonical leading whitespace/BOM is recognized here, but Parse must still
// receive the original body and reject it. Malformed or unknown record versions
// must not disappear from lifetime history merely because Parse rejects them.
func HasRecordPrefix(body string) bool {
	boundary := strings.TrimLeftFunc(body, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
	first, _, _ := strings.Cut(boundary, "\n")
	first = strings.TrimSuffix(first, "\r")
	for _, stage := range []string{"review", "fix", "merge", "tail"} {
		if first == "<!-- pp:review-routing-v1-consumer stage="+stage+" -->" {
			return false
		}
	}
	return strings.HasPrefix(boundary, "<!-- pp:review-routing-")
}

// HasReviewAgain accepts the exact protocol line outside Markdown fenced code.
// An unclosed fence quotes the rest of the comment, including any instructions
// that happen to spell the marker. Fences use at least three backticks or tildes
// and at most three leading spaces; a close must use the same character, be at
// least as long as the opener, and contain only trailing spaces or tabs.
func HasReviewAgain(body string) bool {
	var fence byte
	var fenceLength int
	for _, line := range strings.Split(body, "\n") {
		if fence == 0 && line == "pp:review-again" {
			return true
		}
		text := strings.TrimLeft(line, " ")
		if len(line)-len(text) > 3 || len(text) < 3 || (text[0] != '`' && text[0] != '~') {
			continue
		}
		length := 1
		for length < len(text) && text[length] == text[0] {
			length++
		}
		if fence != 0 {
			if text[0] == fence && length >= fenceLength && strings.Trim(text[length:], " \t\r") == "" {
				fence, fenceLength = 0, 0
			}
			continue
		}
		// A backtick in the info string makes a backtick opener invalid.
		if length >= 3 && (text[0] != '`' || !strings.Contains(text[length:], "`")) {
			fence, fenceLength = text[0], length
		}
	}
	return false
}
