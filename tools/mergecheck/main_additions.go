package main

import (
	"errors"
	"strings"
)

// This separate mode compares the complete CURRENT parents. It deliberately
// makes no claim that edits against the historical merge-base were additions.
// Keeping result byte-identical to source also retains every new source block.
func verifyMainAdditions(source, main string) error {
	if main == "" || source == main {
		return errors.New("main-additions requires a nonempty main and actual additions")
	}
	for _, text := range []string{source, main} {
		for _, line := range lines(text) {
			if conflictStartWidth(line) > 0 || strings.HasPrefix(line, ">>>>>>> ") ||
				strings.HasPrefix(line, "||||||| ") || strings.TrimRight(line, "\r\n") == "=======" {
				return errors.New("main-additions parents contain conflict markers")
			}
		}
	}
	original, pos := lines(main), 0
	for _, line := range lines(source) {
		if pos < len(original) && line == original[pos] {
			pos++
		}
	}
	if pos != len(original) {
		return errors.New("main-additions deletes, replaces or reorders current main lines")
	}
	return nil
}
