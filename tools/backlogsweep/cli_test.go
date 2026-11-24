package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the actual user command, including file loading and report counts.
func TestCLISeparatesCorrectedPlanHistoryFromActiveProblems(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "backlogsweep.exe")
	//nolint:gosec // G204: fixed go build command; output path belongs to this test, not user input.
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	plansDir := filepath.Join(dir, "Plans")
	if err := os.Mkdir(plansDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plansDir, "158-open-form.md"), []byte("# Existing plan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prsFile := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(prsFile, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	corrected := withBody(mk(1, "ivanarama", ago(1), nil,
		[2]string{"ivanarama", "Plans/158-open-form.md"}), "Plans/157-open-form.md")
	missing := withBody(mk(2, "ivanarama", ago(1), nil), "Plans/900-missing.md")
	repeated := withBody(mk(3, "ivanarama", ago(1), nil,
		[2]string{"ivanarama", "Plans/158-open-form.md"},
		[2]string{"ivanarama", "Plans/157-open-form.md"}), "Plans/157-open-form.md")
	for _, tc := range []struct {
		name   string
		issues []issue
		want   []string
		absent []string
	}{
		{"only-history", []issue{corrected},
			[]string{"хвост чист", "исправленные ссылки на планы (справочно) — 1", "#1", "Plans/157-open-form.md", "Plans/158-open-form.md"},
			[]string{"застрявших заявок —", "ссылка на план, которого нет —"}},
		{"history-and-missing", []issue{corrected, missing},
			[]string{"застрявших заявок — 1 (из 2 открытых)", "ссылка на план, которого нет — 1", "#2", "900-missing.md", "исправленные ссылки на планы (справочно) — 1"}, nil},
		{"old-reference-repeated", []issue{repeated},
			[]string{"застрявших заявок — 1", "ссылка на план, которого нет — 1", "#3"},
			[]string{"исправленные ссылки на планы (справочно)", "уже поправлено"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(dir, tc.name+".json")
			data, err := json.Marshal(tc.issues)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // G204: execute the binary just built by this test, with test-owned fixture paths.
			output, err := exec.Command(binary, "-issues", file, "-prs", prsFile,
				"-plans", plansDir, "-now", now.Format("2006-01-02T15:04:05Z07:00")).CombinedOutput()
			if err != nil {
				t.Fatalf("CLI: %v\n%s", err, output)
			}
			text := string(output)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q:\n%s", want, text)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(text, absent) {
					t.Errorf("unexpected %q:\n%s", absent, text)
				}
			}
		})
	}
}
