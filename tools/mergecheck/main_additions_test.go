package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMainAdditionsPublicCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mergecheck.exe")
	//nolint:gosec // G204: fixed compiler, test-owned output, no shell.
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	main := "# Changes\n\nExisting one\nRepeated\nExisting two\nRepeated\n"
	source := "# Changes\n\nWarning one\nExisting one\nRepeated\nWarning two\nExisting two\nRepeated\n"
	conflict := "<<<<<<< HEAD\nsource\n=======\nmain\n>>>>>>> main\n"
	for _, tc := range []struct {
		name, source, main, result, kind string
		resolve, pass                    bool
	}{
		{name: "two interleaved additions over nonempty old base", source: source, main: main, result: source, pass: true},
		{name: "resolve exact source", source: source, main: main, result: conflict, resolve: true, pass: true},
		{name: "ordinary mode stays strict", source: source, main: main, result: source, kind: "entries"},
		{name: "deleted old line", source: "# Changes\n\nExisting one\nRepeated\nRepeated\nWarning\n", main: main},
		{name: "replacement", source: "# Changes\n\nExisting one\nRepeated\nChanged two\nRepeated\n", main: main},
		{name: "reordered old lines", source: "# Changes\n\nExisting two\nRepeated\nExisting one\nRepeated\n", main: main},
		{name: "lost repeated line", source: "# Changes\n\nWarning\nExisting one\nExisting two\nRepeated\n", main: main},
		{name: "rewritten result", source: source, main: main, result: source + "Unapproved text\n"},
		{name: "lost new warning", source: source, main: main, result: main},
		{name: "no additions", source: main, main: main, result: main},
		{name: "empty main", source: source, result: source},
		{name: "newline changed", source: source, main: main[:len(main)-1], result: source},
		{name: "unresolved source", source: conflict, main: "source\n", result: conflict},
		{name: "never overwrite resolved result", source: source, main: main, result: main, resolve: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.result == "" {
				tc.result = tc.source
			}
			if tc.kind == "" {
				tc.kind = "main-additions"
			}
			for name, text := range map[string]string{"base": "# Changes\n\nHistorical placeholder\n", "ours": tc.source, "theirs": tc.main, "result": tc.result} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"-kind", tc.kind, "-base", filepath.Join(dir, "base"), "-ours", filepath.Join(dir, "ours"), "-theirs", filepath.Join(dir, "theirs"), "-result", filepath.Join(dir, "result")}
			if tc.resolve {
				args = append(args, "-resolve")
			}
			//nolint:gosec // G204: binary built above, test-owned arguments, no shell.
			out, err := exec.Command(binary, args...).CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("pass=%v: %v: %s", tc.pass, err, out)
			}
			after, err := os.ReadFile(filepath.Join(dir, "result"))
			if err != nil {
				t.Fatal(err)
			}
			want := tc.result
			if tc.pass && tc.resolve {
				want = tc.source
			}
			if string(after) != want {
				t.Fatalf("unexpected file mutation: %q", after)
			}
		})
	}
}
