// releasecheck проверяет, что собственная заметка стабильного релиза охватывает
// каждый PR полного first-parent диапазона между соседними тегами.
//
// Упоминание — любая ссылка #N в видимом тексте. Осознанный пропуск записывается
// отдельным HTML-комментарием с причиной:
//
//	<!-- release-check: omit #1234 reason=PR готовит саму заметку -->
//
// Исключения намеренно поштучные. Правило вида «не проверять docs/ci» снова
// спрятало бы содержательный PR наподобие #1094, где вместе с CI был исправлен
// date-only путь записи.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	mergePullRequestRe    = regexp.MustCompile(`^Merge pull request #([1-9][0-9]*)\b`)
	squashPullRequestRe   = regexp.MustCompile(`\(#([1-9][0-9]*)\)\s*$`)
	referenceRe           = regexp.MustCompile(`#([1-9][0-9]*)\b`)
	omissionRe            = regexp.MustCompile(`^<!-- release-check: omit #([1-9][0-9]*) reason=(\S(?:.*\S)?) -->$`)
	referenceDefinitionRe = regexp.MustCompile(`^[ \t]{0,3}\[[^\]\r\n]+\]:`)
	commitSHARe           = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

type pullRequest struct {
	number  int
	commit  string
	subject string
}

type noteCoverage struct {
	mentions  map[int]struct{}
	omissions map[int]string
}

type coverageResult struct {
	missing   []pullRequest
	omitted   []pullRequest
	mentioned int
}

type checkedOutput struct {
	writer io.Writer
	err    error
}

func (output *checkedOutput) Write(value []byte) (int, error) {
	if output.err != nil {
		return 0, output.err
	}
	written, err := output.writer.Write(value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil {
		output.err = err
	}
	return written, err
}

func (output *checkedOutput) printf(format string, args ...any) {
	if output.err != nil {
		return
	}
	_, output.err = fmt.Fprintf(output.writer, format, args...)
}

func (output *checkedOutput) println(value string) {
	output.printf("%s\n", value)
}

func resultCode(code int, outputs ...*checkedOutput) int {
	for _, output := range outputs {
		if output.err != nil {
			return 2
		}
	}
	return code
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	standardOutput := &checkedOutput{writer: stdout}
	errorOutput := &checkedOutput{writer: stderr}
	flags := flag.NewFlagSet("releasecheck", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	from := flags.String("from", "", "предыдущий стабильный тег или commit")
	to := flags.String("to", "", "тег или commit проверяемого релиза")
	notes := flags.String("notes", "", "путь к markdown заметки релиза")
	if err := flags.Parse(args); err != nil {
		return resultCode(2, errorOutput)
	}

	if *from == "" || *to == "" || *notes == "" {
		errorOutput.println("releasecheck: обязательны -from, -to и -notes")
		flags.Usage()
		return resultCode(2, errorOutput)
	}

	raw, err := os.ReadFile(*notes)
	if err != nil {
		errorOutput.printf("releasecheck: чтение %s: %v\n", *notes, err)
		return resultCode(2, errorOutput)
	}
	if !utf8.Valid(raw) {
		errorOutput.printf("releasecheck: %s не является корректным UTF-8\n", *notes)
		return resultCode(2, errorOutput)
	}

	prs, err := firstParentPullRequests(*from, *to)
	if err != nil {
		errorOutput.printf("releasecheck: %v\n", err)
		return resultCode(2, errorOutput)
	}
	result, err := checkCoverage(prs, string(raw))
	if err != nil {
		errorOutput.printf("releasecheck: %s: %v\n", *notes, err)
		return resultCode(2, errorOutput)
	}
	if len(result.missing) > 0 {
		errorOutput.printf("releasecheck: %s пропускает PR из полного first-parent диапазона %s..%s:\n", *notes, *from, *to)
		for _, pr := range result.missing {
			errorOutput.printf("  #%d  %.12s  %s\n", pr.number, pr.commit, pr.subject)
		}
		errorOutput.println("добавьте ссылку #N в заметку либо точное исключение:")
		errorOutput.println("  <!-- release-check: omit #N reason=конкретная причина -->")
		return resultCode(1, errorOutput)
	}

	standardOutput.printf(
		"releasecheck: %s охватывает %d PR (%d упомянуто, %d явно исключено)\n",
		*notes,
		len(prs),
		result.mentioned,
		len(result.omitted),
	)
	return resultCode(0, standardOutput)
}

func firstParentPullRequests(fromRef, toRef string) ([]pullRequest, error) {
	from, err := resolveCommit(fromRef)
	if err != nil {
		return nil, fmt.Errorf("исходная граница %q: %w", fromRef, err)
	}
	to, err := resolveCommit(toRef)
	if err != nil {
		return nil, fmt.Errorf("конечная граница %q: %w", toRef, err)
	}
	if err := requireFirstParentAncestor(from, to); err != nil {
		return nil, fmt.Errorf("границы %s..%s: %w", fromRef, toRef, err)
	}

	raw, err := gitOutput(
		"log",
		"--first-parent",
		"--reverse",
		"--format=%H%x09%s",
		from+".."+to,
	)
	if err != nil {
		return nil, fmt.Errorf("first-parent диапазон %s..%s: %w", fromRef, toRef, err)
	}
	return parseFirstParentLog(raw)
}

func resolveCommit(ref string) (string, error) {
	raw, err := gitOutput("rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(raw))
	if !commitSHARe.MatchString(sha) {
		return "", fmt.Errorf("git вернул неожиданный SHA %q", sha)
	}
	return sha, nil
}

func requireFirstParentAncestor(from, to string) error {
	raw, err := gitOutput("rev-list", "--first-parent", to)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if scanner.Text() == from {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("чтение first-parent цепочки: %w", err)
	}
	return fmt.Errorf("исходная граница %.12s не входит в first-parent цепочку %.12s", from, to)
}

func gitOutput(args ...string) ([]byte, error) {
	//nolint:gosec // G204: executable fixed; arguments are passed without a shell, refs are resolved to SHA before ranges.
	cmd := exec.Command("git", args...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return raw, nil
}

func parseFirstParentLog(raw []byte) ([]pullRequest, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	seen := make(map[int]struct{})
	var result []pullRequest
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("неожиданная строка git log: %q", line)
		}
		number, ok := pullRequestNumber(parts[1])
		if !ok {
			// Main защищён и пополняется через PR. Если subject не несёт номера,
			// проверка не может доказать полноту и обязана остановиться, а не
			// молча принять неизвестный commit за служебный.
			return nil, fmt.Errorf("first-parent commit %.12s не называет PR: %q", parts[0], parts[1])
		}
		if _, duplicate := seen[number]; duplicate {
			continue
		}
		seen[number] = struct{}{}
		result = append(result, pullRequest{number: number, commit: parts[0], subject: parts[1]})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("чтение git log: %w", err)
	}
	return result, nil
}

func pullRequestNumber(subject string) (int, bool) {
	match := mergePullRequestRe.FindStringSubmatch(subject)
	if match == nil {
		match = squashPullRequestRe.FindStringSubmatch(subject)
	}
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil
}

func checkCoverage(prs []pullRequest, note string) (coverageResult, error) {
	coverage, err := parseNoteCoverage(note)
	if err != nil {
		return coverageResult{}, err
	}

	inRange := make(map[int]pullRequest, len(prs))
	for _, pr := range prs {
		inRange[pr.number] = pr
	}
	for number := range coverage.omissions {
		if _, ok := inRange[number]; !ok {
			return coverageResult{}, fmt.Errorf("исключение #%d не входит в проверяемый диапазон", number)
		}
		if _, mentioned := coverage.mentions[number]; mentioned {
			return coverageResult{}, fmt.Errorf("PR #%d одновременно упомянут и исключён", number)
		}
	}

	result := coverageResult{}
	for _, pr := range prs {
		if _, ok := coverage.mentions[pr.number]; ok {
			result.mentioned++
			continue
		}
		if _, ok := coverage.omissions[pr.number]; ok {
			result.omitted = append(result.omitted, pr)
			continue
		}
		result.missing = append(result.missing, pr)
	}
	return result, nil
}

func parseNoteCoverage(note string) (noteCoverage, error) {
	result := noteCoverage{
		mentions:  make(map[int]struct{}),
		omissions: make(map[int]string),
	}
	visibleLines := strings.Split(note, "\n")
	for index, rawLine := range visibleLines {
		line := strings.TrimSuffix(rawLine, "\r")
		if strings.Contains(line, "release-check: omit") {
			match := omissionRe.FindStringSubmatch(line)
			if match == nil {
				return noteCoverage{}, fmt.Errorf("строка %d: неверный marker исключения", index+1)
			}
			number, err := strconv.Atoi(match[1])
			if err != nil {
				return noteCoverage{}, fmt.Errorf("строка %d: неверный номер PR: %w", index+1, err)
			}
			if _, duplicate := result.omissions[number]; duplicate {
				return noteCoverage{}, fmt.Errorf("строка %d: повторное исключение #%d", index+1, number)
			}
			result.omissions[number] = match[2]
			visibleLines[index] = ""
			continue
		}
	}
	visibleText := markdownVisibleText(strings.Join(visibleLines, "\n"))
	for _, match := range referenceRe.FindAllStringSubmatch(visibleText, -1) {
		number, err := strconv.Atoi(match[1])
		if err == nil {
			result.mentions[number] = struct{}{}
		}
	}
	return result, nil
}

// markdownVisibleText conservatively keeps only text rendered by Markdown.
// False negatives make the release gate ask for an explicit visible mention;
// false positives could silently publish incomplete release notes.
func markdownVisibleText(markdown string) string {
	withoutComments := stripHTMLComments(markdown)
	lines := strings.Split(withoutComments, "\n")
	for index, line := range lines {
		if referenceDefinitionRe.MatchString(strings.TrimSuffix(line, "\r")) {
			lines[index] = ""
		}
	}

	text := strings.Join(lines, "\n")
	var visible strings.Builder
	visible.Grow(len(text))
	for index := 0; index < len(text); {
		switch {
		case text[index] == ']' && index+1 < len(text) && text[index+1] == '(':
			visible.WriteByte(']')
			index = skipBalancedMarkdown(text, index+1, '(', ')')
		case text[index] == ']' && index+1 < len(text) && text[index+1] == '[':
			visible.WriteByte(']')
			index = skipBalancedMarkdown(text, index+1, '[', ']')
		case text[index] == '<':
			end, isTag := markdownHTMLTagEnd(text, index)
			if !isTag {
				visible.WriteByte(text[index])
				index++
				continue
			}
			index = end
		default:
			visible.WriteByte(text[index])
			index++
		}
	}
	return visible.String()
}

func stripHTMLComments(markdown string) string {
	var visible strings.Builder
	visible.Grow(len(markdown))
	for {
		start := strings.Index(markdown, "<!--")
		if start < 0 {
			visible.WriteString(markdown)
			return visible.String()
		}
		visible.WriteString(markdown[:start])
		markdown = markdown[start+len("<!--"):]
		end := strings.Index(markdown, "-->")
		if end < 0 {
			return visible.String()
		}
		markdown = markdown[end+len("-->"):]
	}
}

func skipBalancedMarkdown(text string, start int, open, close byte) int {
	depth := 0
	for index := start; index < len(text); index++ {
		if text[index] == '\\' && index+1 < len(text) {
			index++
			continue
		}
		switch text[index] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return len(text)
}

func markdownHTMLTagEnd(text string, start int) (int, bool) {
	index := start + 1
	if index >= len(text) {
		return start, false
	}
	if text[index] == '!' || text[index] == '?' {
		return scanHTMLTagEnd(text, index+1), true
	}
	if text[index] == '/' {
		index++
	}
	nameStart := index
	for index < len(text) && isHTMLNameByte(text[index]) {
		index++
	}
	if index == nameStart || index >= len(text) {
		return start, false
	}
	if text[index] != '>' && text[index] != '/' && text[index] != ' ' && text[index] != '\t' && text[index] != '\r' && text[index] != '\n' {
		return start, false
	}
	return scanHTMLTagEnd(text, index), true
}

func scanHTMLTagEnd(text string, start int) int {
	var quote byte
	for index := start; index < len(text); index++ {
		if quote != 0 {
			if text[index] == quote {
				quote = 0
			}
			continue
		}
		switch text[index] {
		case '\'', '"':
			quote = text[index]
		case '>':
			return index + 1
		}
	}
	return len(text)
}

func isHTMLNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '-'
}
