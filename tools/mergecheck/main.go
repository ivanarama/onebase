// mergecheck verifies a manually resolved append-only Markdown conflict.
// It does not resolve conflicts or write repository files.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const markerSize = 32

var planRow = regexp.MustCompile(`^(\|[ \t]*)([0-9]+)([ \t]*\|.*\n?)$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mergecheck:", err)
		os.Exit(1)
	}
}

func run() error {
	kind := flag.String("kind", "entries", "entries or plans (only the first numeric table cell may change)")
	resolve := flag.Bool("resolve", false, "write a verified canonical union for append-only entries")
	base := flag.String("base", "", "merge-base file")
	ours := flag.String("ours", "", "original PR file")
	theirs := flag.String("theirs", "", "original main file")
	result := flag.String("result", "", "resolved file")
	flag.Parse()
	if (*kind != "entries" && *kind != "plans") || flag.NArg() != 0 {
		return errors.New("expected -kind entries|plans and four file paths")
	}
	if *resolve && *kind != "entries" {
		return errors.New("-resolve is supported only for append-only entries")
	}
	var resolved string
	for i, path := range []string{*base, *ours, *theirs, *result} {
		if path == "" {
			return errors.New("-base, -ours, -theirs and -result are required")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return fmt.Errorf("%s: expected UTF-8 text", path)
		}
		for _, line := range lines(string(data)) {
			for _, symbol := range []string{"<", "|", "=", ">"} {
				if strings.HasPrefix(line, strings.Repeat(symbol, markerSize)) {
					return fmt.Errorf("%s: reserved conflict marker", path)
				}
			}
		}
		if i == 3 {
			resolved = string(data)
		}
	}
	// diff3 keeps the common suffix inside BOTH additions. --union can lose
	// that suffix from one record; --zdiff3 can move it outside the conflict.
	//nolint:gosec // G204: фиксированный git, аргументы — пути файлов из флагов самого mergecheck; shell не запускается.
	cmd := exec.Command("git", "merge-file", "-p", "--diff3", "--marker-size=32",
		"-L", "ours", "-L", "base", "-L", "theirs", "--", *ours, *base, *theirs)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	merged, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 127 {
			return fmt.Errorf("git merge-file: %w: %s", err, stderr.String())
		}
	}
	if *resolve {
		// A marker shown in ordinary Markdown is not an unresolved merge. Never
		// overwrite a result that the normal verifier already accepts, either.
		if verify(string(merged), resolved, *kind) == nil || !hasConflictMarkers(resolved) {
			return errors.New("-resolve requires an unresolved conflict file")
		}
		candidate, err := resolveEntries(string(merged))
		if err != nil {
			return err
		}
		if err := verify(string(merged), candidate, *kind); err != nil {
			return err
		}
		return replaceFile(*result, []byte(candidate))
	}
	return verify(string(merged), resolved, *kind)
}

func hasConflictMarkers(content string) bool {
	state, width, conflicts := 0, 0, 0
	for _, line := range lines(content) {
		switch state {
		case 0:
			if next := conflictStartWidth(line); next > 0 {
				width, state = next, 1
			}
		case 1:
			if line == strings.Repeat("=", width)+"\n" {
				state = 2
			}
		case 2:
			if strings.HasPrefix(line, strings.Repeat(">", width)+" ") {
				state, conflicts = 0, conflicts+1
			}
		}
	}
	return state == 0 && conflicts > 0
}

func conflictStartWidth(line string) int {
	width := 0
	for width < len(line) && line[width] == '<' {
		width++
	}
	if width < 7 || width >= len(line) || line[width] != ' ' {
		return 0
	}
	return width
}

func resolveEntries(merged string) (string, error) {
	source := lines(merged)
	start := strings.Repeat("<", markerSize) + " ours\n"
	base := strings.Repeat("|", markerSize) + " base\n"
	separator := strings.Repeat("=", markerSize) + "\n"
	end := strings.Repeat(">", markerSize) + " theirs\n"
	var result strings.Builder
	conflicts := 0
	for i := 0; i < len(source); {
		if source[i] != start {
			result.WriteString(source[i])
			i++
			continue
		}
		conflicts++
		i++
		var ours, original, theirs []string
		for _, part := range []struct {
			until string
			into  *[]string
		}{{base, &ours}, {separator, &original}, {end, &theirs}} {
			for i < len(source) && source[i] != part.until {
				*part.into = append(*part.into, source[i])
				i++
			}
			if i == len(source) {
				return "", errors.New("incomplete diff3 conflict")
			}
			i++
		}
		if len(original) != 0 || len(ours) == 0 || len(theirs) == 0 {
			return "", errors.New("conflict changes existing content; manual decision required")
		}
		for _, line := range ours {
			result.WriteString(line)
		}
		for _, line := range theirs {
			result.WriteString(line)
		}
	}
	if conflicts == 0 {
		return "", errors.New("-resolve requires an append-only conflict")
	}
	return result.String(), nil
}

func replaceFile(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".mergecheck-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(content); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Sync(); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func lines(text string) []string {
	result := strings.SplitAfter(text, "\n")
	if result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}

func verify(merged, resolved, kind string) error {
	source, result := lines(merged), lines(resolved)
	start := strings.Repeat("<", markerSize) + " ours\n"
	base := strings.Repeat("|", markerSize) + " base\n"
	separator := strings.Repeat("=", markerSize) + "\n"
	end := strings.Repeat(">", markerSize) + " theirs\n"
	numbers := make(map[string]bool)
	for i, pos := 0, 0; ; {
		if i == len(source) {
			if pos != len(result) {
				return errors.New("unexpected text after the merge result")
			}
			return nil
		}
		if source[i] != start {
			if pos == len(result) || source[i] != result[pos] {
				return errors.New("text outside an append conflict changed")
			}
			i++
			pos++
			continue
		}
		i++
		var ours, original, theirs []string
		for _, part := range []struct {
			until string
			into  *[]string
		}{{base, &ours}, {separator, &original}, {end, &theirs}} {
			for i < len(source) && source[i] != part.until {
				*part.into = append(*part.into, source[i])
				i++
			}
			if i == len(source) {
				return errors.New("incomplete diff3 conflict")
			}
			i++
		}
		if len(original) != 0 || len(ours) == 0 || len(theirs) == 0 {
			return errors.New("conflict changes existing content; manual decision required")
		}
		count := len(ours) + len(theirs)
		if len(result)-pos < count {
			return errors.New("an added block lost lines or occurrences")
		}
		actual := strings.Join(result[pos:pos+count], "")
		left, right := strings.Join(ours, ""), strings.Join(theirs, "")
		if kind == "plans" {
			var err error
			left, err = normalizeRows(left, nil)
			if err != nil {
				return err
			}
			right, err = normalizeRows(right, nil)
			if err != nil {
				return err
			}
			actual, err = normalizeRows(actual, numbers)
			if err != nil {
				return err
			}
		}
		if actual != left+right && actual != right+left {
			return errors.New("added blocks must retain their context, order and occurrences")
		}
		pos += count
	}
}

func normalizeRows(text string, seen map[string]bool) (string, error) {
	var result strings.Builder
	for _, line := range lines(text) {
		match := planRow.FindStringSubmatch(line)
		if match == nil {
			return "", errors.New("plan conflict must contain only numbered table rows")
		}
		if seen != nil {
			number := strings.TrimLeft(match[2], "0")
			if number == "" || seen[number] {
				return "", errors.New("resolved plan additions need distinct positive numbers")
			}
			seen[number] = true
		}
		result.WriteString(match[1] + "<number>" + match[3])
	}
	return result.String(), nil
}
