package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the command used by MERGE, including Git's actual diff3 output.
func TestMergecheckCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mergecheck.exe")
	//nolint:gosec // G204: фиксированный go и литеральные аргументы в тесте; shell не используется.
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build mergecheck: %v\n%s", err, output)
	}
	base := "# Изменения\n\n"
	ours := "- Экспорт:\n  Сохранена совместимость.\n"
	theirs := "- Импорт:\n  Сохранена совместимость.\n"
	table := "| № | План |\n|---|---|\n"
	leftRow := "| 200 | [Экспорт](export.md) |\n"
	rightRow := "| 200 | [Импорт](import.md) |\n"
	renumbered := "| 201 | [Импорт](import.md) |\n"
	for _, tt := range []struct {
		name, kind, base, ours, theirs, result, failure string
		union                                           bool
	}{
		{name: "both complete entries", kind: "entries", base: base, ours: base + ours, theirs: base + theirs, result: base + ours + theirs},
		{name: "reverse side order", kind: "entries", base: base, ours: base + ours, theirs: base + theirs, result: base + theirs + ours},
		{name: "union loses repeated explanation", kind: "entries", base: base, ours: base + ours, theirs: base + theirs, union: true, failure: "lost lines or occurrences"},
		{name: "same counts wrong context", kind: "entries", base: base, ours: base + ours, theirs: base + theirs,
			result: base + "- Экспорт:\n- Импорт:\n  Сохранена совместимость.\n  Сохранена совместимость.\n", failure: "retain their context"},
		{name: "record line order", kind: "entries", base: base, ours: base + ours, theirs: base + theirs,
			result: base + "  Сохранена совместимость.\n- Экспорт:\n" + theirs, failure: "retain their context"},
		{name: "unchanged context is mandatory", kind: "entries", base: base, ours: base + ours, theirs: base + theirs,
			result: "# Другая версия\n\n" + ours + theirs, failure: "outside an append conflict"},
		{name: "conflicting edits require a decision", kind: "entries", base: base + "- Старое\n", ours: base + ours, theirs: base + theirs,
			result: base + ours + theirs, failure: "changes existing content"},
		{name: "clean edits outside the conflict", kind: "entries", base: "# Old\n\nContext\n\n", ours: "# New\n\nContext\n\n" + ours,
			theirs: "# Old\n\nContext\n\n" + theirs, result: "# New\n\nContext\n\n" + ours + theirs},
		{name: "identical additions already merged by Git", kind: "entries", base: base, ours: base + ours, theirs: base + ours, result: base + ours},
		{name: "renumber plans", kind: "plans", base: table, ours: table + leftRow, theirs: table + rightRow,
			result: table + leftRow + renumbered},
		{name: "plan number collision remains", kind: "plans", base: table, ours: table + leftRow, theirs: table + rightRow,
			result: table + leftRow + rightRow, failure: "distinct positive numbers"},
		{name: "plan link changed", kind: "plans", base: table, ours: table + leftRow, theirs: table + rightRow,
			result: table + leftRow + "| 201 | [Импорт](wrong.md) |\n", failure: "retain their context"},
		{name: "plan description changed", kind: "plans", base: table, ours: table + leftRow, theirs: table + rightRow,
			result: table + leftRow + "| 201 | [Другое](import.md) |\n", failure: "retain their context"},
		{name: "plan row lost", kind: "plans", base: table, ours: table + leftRow, theirs: table + rightRow,
			result: table + leftRow, failure: "lost lines or occurrences"},
		{name: "entry numbers are immutable", kind: "entries", base: table, ours: table + leftRow, theirs: table + rightRow,
			result: table + leftRow + renumbered, failure: "retain their context"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range map[string]string{"base": tt.base, "ours": tt.ours, "theirs": tt.theirs, "result": tt.result} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.union {
				//nolint:gosec // G204: фиксированный git и пути фикстур, построенные в тесте; shell не используется.
				output, err := exec.Command("git", "merge-file", "-p", "--union", filepath.Join(dir, "ours"), filepath.Join(dir, "base"), filepath.Join(dir, "theirs")).Output()
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(string(output), "Сохранена совместимость.") != 1 {
					t.Fatalf("fixture no longer reproduces --union loss: %s", output)
				}
				if err := os.WriteFile(filepath.Join(dir, "result"), output, 0600); err != nil {
					t.Fatal(err)
				}
			}
			//nolint:gosec // G204: бинарь собран самим тестом во временном каталоге, аргументы заданы тестом; shell не используется.
			output, err := exec.Command(binary, "-kind", tt.kind, "-base", filepath.Join(dir, "base"), "-ours", filepath.Join(dir, "ours"),
				"-theirs", filepath.Join(dir, "theirs"), "-result", filepath.Join(dir, "result")).CombinedOutput()
			if tt.failure == "" {
				if err != nil {
					t.Fatalf("valid resolution rejected: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), tt.failure) {
				t.Fatalf("want rejection containing %q, got %v\n%s", tt.failure, err, output)
			}
		})
	}
}

func TestMergecheckCLIResolveAppendOnlyConflict(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mergecheck.exe")
	//nolint:gosec // G204: fixed command and test-owned path.
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build mergecheck: %v\n%s", err, output)
	}
	dir := t.TempDir()
	base := "# Changes\n\n- Existing\n"
	left := "- Left:\n  Repeated explanation.\n\n"
	right := "- Right:\n  Repeated explanation.\n\n"
	conflicted := "<<<<<<< HEAD\n- incomplete\n=======\n- incomplete\n>>>>>>> origin/main\n"
	paths := map[string]string{
		"base":   base,
		"ours":   "# Changes\n\n" + left + "- Existing\n",
		"theirs": "# Changes\n\n" + right + "- Existing\n",
		"result": conflicted,
	}
	for name, content := range paths {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-kind", "entries", "-base", filepath.Join(dir, "base"),
		"-ours", filepath.Join(dir, "ours"), "-theirs", filepath.Join(dir, "theirs"),
		"-result", filepath.Join(dir, "result")}
	//nolint:gosec // G204: binary and arguments are test-owned fixtures.
	if output, err := exec.Command(binary, append([]string{"-resolve"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("resolve append-only conflict: %v\n%s", err, output)
	}
	actual, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Changes\n\n" + left + right + "- Existing\n"
	if string(actual) != want {
		t.Fatalf("resolved blocks were not preserved exactly:\nwant %q\n got %q", want, actual)
	}
	// The normal public verifier must accept the generated file.
	//nolint:gosec // G204: binary and arguments are test-owned fixtures.
	if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("verify generated result: %v\n%s", err, output)
	}
	// Never replace a file that is already resolved by a person.
	//nolint:gosec // G204: binary and arguments are test-owned fixtures.
	if output, err := exec.Command(binary, append([]string{"-resolve"}, args...)...).CombinedOutput(); err == nil ||
		!strings.Contains(string(output), "unresolved conflict file") {
		t.Fatalf("resolved file was overwritten: %v\n%s", err, output)
	}
	// Literal marker examples inside a valid Markdown line do not make a
	// previously resolved result writable again, even in the opposite order.
	markerExample := "Marker examples: `<<<<<<<` and `>>>>>>>`.\n"
	base = "# Changes\n" + markerExample
	left, right = "- Left\n", "- Right\n"
	for name, content := range map[string]string{
		"base": base, "ours": base + left, "theirs": base + right,
		"result": base + right + left,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: binary and arguments are test-owned fixtures.
	if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("literal marker fixture must be a valid resolution: %v\n%s", err, output)
	}
	//nolint:gosec // G204: binary and arguments are test-owned fixtures.
	if output, err := exec.Command(binary, append([]string{"-resolve"}, args...)...).CombinedOutput(); err == nil ||
		!strings.Contains(string(output), "unresolved conflict file") {
		t.Fatalf("literal markers triggered replacement: %v\n%s", err, output)
	}
	after, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("resolved file changed: before=%q after=%q", before, after)
	}
}

func TestMergecheckCLIResolveRejectsContentConflictWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"base":   "# Changes\n\n- Existing\n",
		"ours":   "# Changes\n\n- Changed by ours\n",
		"theirs": "# Changes\n\n- Changed by theirs\n",
		"result": "<<<<<<< HEAD\n- ours\n=======\n- theirs\n>>>>>>> origin/main\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: fixed tool package and test-owned fixture paths.
	command := exec.Command("go", "run", ".", "-resolve", "-kind", "entries",
		"-base", filepath.Join(dir, "base"), "-ours", filepath.Join(dir, "ours"),
		"-theirs", filepath.Join(dir, "theirs"), "-result", filepath.Join(dir, "result"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "manual decision required") {
		t.Fatalf("content conflict was accepted: %v\n%s", err, output)
	}
	current, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatalf("rejected conflict changed result: %q", current)
	}
}
