// prclosecheck prevents commit messages from closing issues that the PR body
// does not explicitly declare. Run from a complete checkout:
//
//	go run ./tools/prclosecheck -event "$GITHUB_EVENT_PATH"
//
// Only base..head from the immutable pull_request event is scanned. Commit
// messages are raw text (even a negative example can close an issue); body
// declarations must occupy their own top-level line outside code/comments.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	keyword         = `\b(?:close[sd]?|fix(?:es|ed)?|resolve[sd]?)\b[\s:]+`
	reference       = `(?:https://github\.com/[a-z0-9_.-]+/[a-z0-9_.-]+/(?:issues|pull)/[0-9]+|(?:[a-z0-9_.-]+/[a-z0-9_.-]+)?#[0-9]+)`
	linkDestination = `(?:<[^<>\s\\]*>|[^\s()"'\\<>]*)`
	linkTitle       = `(?:"[^"\\]*"|'[^'\\]*'|\([^()\\]*\))`
)

var (
	closingRE      = regexp.MustCompile(`(?i)` + keyword + `(` + reference + `)`)
	declarationRE  = regexp.MustCompile(`(?i)^` + keyword + `(` + reference + `)[.]?$`)
	shaRE          = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoRE         = regexp.MustCompile(`(?i)^[a-z0-9_.-]+/[a-z0-9_.-]+$`)
	rawHTMLRE      = regexp.MustCompile(`(?i)^<(pre|script|style|textarea|code)(?:\s|/?>|$)`)
	rawHTMLCloseRE = regexp.MustCompile(`(?i)^</(pre|script|style|textarea|code)[ \t]*>`)
	// Certify only simple links completed on one line. Anything more complex
	// keeps earlier declarations but cannot authorize a later closure.
	linkLabelRE      = regexp.MustCompile(`^\[[^\[\]\\\x60<>]*\]`)
	inlineLinkRE     = regexp.MustCompile(`^\[[^\[\]\\\x60<>]*\]\(` + linkDestination + `(?:[ \t]+` + linkTitle + `)?[ \t]*\)`)
	linkDefinitionRE = regexp.MustCompile(`^\[[^\[\]\\\x60<>]+\]:[ \t]*(?:<[^<>\s\\]*>|[^\s()"'\\<>]+)[ \t]+` + linkTitle + `[ \t]*$`)
	htmlTagRE        = regexp.MustCompile(`(?i)^</?[a-z][a-z0-9-]*(?:[ \t]|/?>|$)`)
)

type event struct {
	PullRequest *struct {
		Body *string `json:"body"`
		Base struct {
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
}

func main() {
	path := flag.String("event", "", "path to the pull_request event JSON")
	flag.Parse()
	if err := check(*path); err != nil {
		fmt.Fprintln(os.Stderr, "prclosecheck:", err)
		os.Exit(1)
	}
}

func check(path string) error {
	if path == "" {
		return fmt.Errorf("-event is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read event: %w", err)
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("event is not UTF-8")
	}
	var e event
	if err := json.Unmarshal(raw, &e); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	pr := e.PullRequest
	if pr == nil || pr.Body == nil {
		return fmt.Errorf("pull_request.body is missing or null")
	}
	if !shaRE.MatchString(pr.Base.SHA) || !shaRE.MatchString(pr.Head.SHA) || !repoRE.MatchString(pr.Base.Repo.FullName) {
		return fmt.Errorf("pull_request base/head SHA or base repository is missing/invalid")
	}
	shallow, err := git("rev-parse", "--is-shallow-repository")
	if err != nil {
		return err
	}
	if strings.TrimSpace(shallow) != "false" {
		return fmt.Errorf("complete history required (checkout fetch-depth: 0)")
	}
	for _, sha := range []string{pr.Base.SHA, pr.Head.SHA} {
		if _, err := git("cat-file", "-e", sha+"^{commit}"); err != nil {
			return err
		}
	}
	commits, err := git("rev-list", pr.Base.SHA+".."+pr.Head.SHA, "--")
	if err != nil {
		return err
	}
	declared := declarations(*pr.Body, pr.Base.Repo.FullName)
	var violations []string
	for _, sha := range strings.Fields(commits) {
		message, err := git("show", "-s", "--format=%B", sha, "--")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, match := range closingRE.FindAllStringSubmatch(message, -1) {
			ref := canonicalRef(match[1], pr.Base.Repo.FullName)
			if !declared[ref] && !seen[ref] {
				violations = append(violations, fmt.Sprintf("commit %s closes %s without a standalone declaration in the PR body", sha, ref))
				seen[ref] = true
			}
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("%s", strings.Join(violations, "\n"))
	}
	fmt.Printf("prclosecheck: checked %d PR commits\n", len(strings.Fields(commits)))
	return nil
}

func git(args ...string) (string, error) {
	// No shell, branch names or message text are executed. Replacement objects
	// and signature display must not change the graph/messages being inspected.
	cmd := exec.Command("git", append([]string{"--no-pager", "--no-replace-objects", "-c", "log.showSignature=false"}, args...)...) //nolint:gosec // fixed executable, separate validated/literal arguments
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func canonicalRef(ref, repo string) string {
	ref = strings.ToLower(ref)
	if strings.HasPrefix(ref, "https://github.com/") {
		parts := strings.Split(strings.TrimPrefix(ref, "https://github.com/"), "/")
		ref = parts[0] + "/" + parts[1] + "#" + parts[3]
	}
	if strings.HasPrefix(ref, "#") {
		ref = strings.ToLower(repo) + ref
	}
	parts := strings.SplitN(ref, "#", 2)
	n := strings.TrimLeft(parts[1], "0")
	if n == "" {
		n = "0"
	}
	return parts[0] + "#" + n
}

// Deliberately accept only plain, whole-line declarations. The scanner is
// conservative around nested fences and HTML: uncertainty cannot authorize a
// closure. It never strips markup and joins fragments into a declaration.
func declarations(body, repo string) map[string]bool {
	result := map[string]bool{}
	var fence byte
	fenceLen, spanLen := 0, 0
	inComment, inHTML := false, false
	var htmlEnd string
	var rawTags []string
	inTag := false
	var tagQuote byte
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != 0 {
			closing := strings.TrimLeft(line, " ")
			if n := runLength(closing, fence); len(line)-len(closing) <= 3 && n >= fenceLen && strings.TrimSpace(closing[n:]) == "" {
				fence = 0
			}
			continue
		}
		// Also recognize fence openers in lists/quotes; their contents cannot
		// become top-level declarations after indentation is removed.
		candidate := strings.TrimLeft(trimmed, "> +-0123456789.)\t")
		if !inComment && !inHTML && htmlEnd == "" && len(rawTags) == 0 && spanLen == 0 && len(candidate) > 0 && (candidate[0] == '`' || candidate[0] == '~') {
			n := runLength(candidate, candidate[0])
			if n >= 3 && (candidate[0] != '`' || !strings.Contains(candidate[n:], "`")) {
				// Indented fences may continue a list; container fences can
				// end with their container. Without a container parser, a later
				// top-level opener is not a safe closer. Keep only earlier
				// declarations rather than authorize text in a later code block.
				if candidate != strings.TrimRight(line, " \t") {
					return result
				}
				fence, fenceLen = candidate[0], n
				continue
			}
		}
		// A blank line ends an ordinary HTML block. Comments, special HTML
		// blocks and code elements retain their own closing delimiter.
		hadMarkup := inHTML || inComment || htmlEnd != "" || len(rawTags) != 0 || spanLen != 0
		if trimmed == "" {
			inHTML = false
		} else if !inComment && len(rawTags) == 0 && spanLen == 0 && strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "<!--") {
			inHTML, hadMarkup = true, true
		}
		for i := 0; i < len(line); i++ {
			if htmlEnd != "" {
				n := strings.Index(line[i:], htmlEnd)
				if n < 0 {
					break
				}
				i += n + len(htmlEnd) - 1
				htmlEnd = ""
				continue
			}
			if inComment {
				n := strings.Index(line[i:], "-->")
				if n < 0 {
					break
				}
				i += n + 2
				inComment = false
				continue
			}
			// Do not mistake a delimiter inside an opening tag's quoted
			// attribute for the end of a code element, even across lines.
			if inTag {
				switch {
				case tagQuote != 0:
					if line[i] == tagQuote {
						tagQuote = 0
					}
				case line[i] == '\'' || line[i] == '"':
					tagQuote = line[i]
				case line[i] == '>':
					inTag = false
				}
				continue
			}
			if len(rawTags) != 0 {
				tag := rawTags[len(rawTags)-1]
				if (tag == "pre" || tag == "code") && strings.HasPrefix(line[i:], "<!--") {
					inComment = true
					i += 3
					continue
				}
				if line[i] == '<' {
					if m := rawHTMLCloseRE.FindStringSubmatch(line[i:]); m != nil && strings.EqualFold(m[1], tag) {
						rawTags = rawTags[:len(rawTags)-1]
						i += len(m[0]) - 1
					} else if tag == "pre" || tag == "code" {
						if m := rawHTMLRE.FindStringSubmatch(line[i:]); m != nil {
							rawTags = append(rawTags, strings.ToLower(m[1]))
							inTag = !strings.HasSuffix(m[0], ">")
							i += len(m[0]) - 1
						}
					}
				}
				continue
			}
			if spanLen == 0 && strings.HasPrefix(line[i:], "<!--") {
				inComment, hadMarkup = true, true
				i += 3
				continue
			}
			if spanLen == 0 && line[i] == '<' {
				// GFM special HTML blocks end at their terminator, including
				// across blank lines. Their contents are not Markdown or tags.
				switch {
				case strings.HasPrefix(line[i:], "<![CDATA["):
					htmlEnd = "]]>"
					i += len("<![CDATA[") - 1
				case strings.HasPrefix(line[i:], "<?"):
					htmlEnd = "?>"
					i++
				case len(line)-i >= 3 && line[i+1] == '!' && line[i+2] >= 'A' && line[i+2] <= 'Z':
					htmlEnd = ">"
					i += 2
				}
				if htmlEnd != "" {
					hadMarkup = true
					continue
				}
				if m := rawHTMLRE.FindStringSubmatch(line[i:]); m != nil {
					rawTags = append(rawTags, strings.ToLower(m[1]))
					inTag = !strings.HasSuffix(m[0], ">")
					hadMarkup = true
					i += len(m[0]) - 1
					continue
				}
				if m := htmlTagRE.FindString(line[i:]); m != "" {
					inTag = !strings.HasSuffix(m, ">")
					hadMarkup = true
					i += len(m) - 1
					continue
				}
			}
			if spanLen == 0 && !inHTML {
				// Escaped brackets cannot start a Markdown link. Consume the
				// escaped byte so an escaped backslash still leaves '[' active.
				if line[i] == '\\' && i+1 < len(line) {
					i++
					continue
				}
				if line[i] == '[' {
					label := linkLabelRE.FindString(line[i:])
					if label == "" {
						return result
					}
					rest := line[i+len(label):]
					switch {
					case strings.HasPrefix(rest, "("):
						link := inlineLinkRE.FindString(line[i:])
						if link == "" {
							return result
						}
						i += len(link) - 1
					case strings.HasPrefix(rest, ":"):
						// Even a destination completed here can have a title
						// on the next line. Only a complete same-line title
						// certifies the end of this definition.
						if !linkDefinitionRE.MatchString(line[i:]) {
							return result
						}
						i = len(line) - 1
					}
					continue
				}
			}
			if line[i] != '`' || inHTML {
				continue
			}
			n := runLength(line[i:], '`')
			hadMarkup = true
			switch spanLen {
			case 0:
				spanLen = n
			case n:
				spanLen = 0
			}
			i += n - 1
		}
		// An ordinary tag split across lines can hide later text in a quoted
		// attribute even after a blank line ends the Markdown HTML block.
		// Without parsing arbitrary HTML, keep only earlier declarations.
		if inTag && len(rawTags) == 0 {
			return result
		}
		indent := 0
	indentation:
		for _, ch := range line {
			switch ch {
			case ' ':
				indent++
			case '\t':
				indent += 4 - indent%4
			default:
				break indentation
			}
		}
		if hadMarkup || indent >= 4 {
			continue
		}
		if m := declarationRE.FindStringSubmatch(trimmed); m != nil {
			result[canonicalRef(m[1], repo)] = true
		}
	}
	return result
}

func runLength(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}
