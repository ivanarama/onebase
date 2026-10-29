package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIExplicitRetiredPlanReplacement(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "backlogsweep.exe")
	//nolint:gosec // fixed build command and test-owned executable path
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	old := "Plans/164-tail-durable-queue-and-scheduler-heartbeat.md"
	next := "Plans/213-tail-durable-discovery-and-backlog.md"
	declaration := "Plan-replaces: " + old + " -> " + next
	for _, tc := range []struct {
		name, body               string
		comments                 [][2]string
		prOnly, missing, history bool
	}{
		{"explicit different scope", old, [][2]string{{"ivanarama", declaration}}, false, false, true},
		{"replacement in open PR", old, [][2]string{{"ivanarama", declaration}}, true, false, true},
		{"unrelated later plan", old, [][2]string{{"ivanarama", next}}, false, false, false},
		{"missing destination", old, [][2]string{{"ivanarama", declaration}}, false, true, false},
		{"untrusted declaration", old, [][2]string{{"outsider", declaration}}, false, false, false},
		{"quoted declaration", old, [][2]string{{"ivanarama", "> " + declaration}}, false, false, false},
		{"fenced declaration", old, [][2]string{{"ivanarama", "```text\n" + declaration + "\n```"}}, false, false, false},
		{"long fence", old, [][2]string{{"ivanarama", "````\n```\n" + declaration + "\n````"}}, false, false, false},
		{"body is not later decision", old + "\n" + declaration, nil, false, false, false},
		{"source repeated later", old, [][2]string{{"ivanarama", declaration}, {"ivanarama", old}}, false, false, false},
		{"declaration precedes source", "", [][2]string{{"ivanarama", declaration}, {"ivanarama", old}}, false, false, false},
		{"wrong exact source", old, [][2]string{{"ivanarama", "Plan-replaces: Plans/164-other.md -> " + next}}, false, false, false},
		{"bare number source", "план 164", [][2]string{{"ivanarama", declaration}}, false, false, false},
		{"conflicting declarations", old, [][2]string{{"ivanarama", declaration}, {"ivanarama", "Plan-replaces: " + old + " -> Plans/214-different.md"}}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t.TempDir()
			plans := filepath.Join(fixture, "Plans")
			if err := os.Mkdir(plans, 0700); err != nil {
				t.Fatal(err)
			}
			var prs []pull
			if !tc.missing {
				if tc.prOnly {
					prs = []pull{{Number: 2424}}
					prs[0].Files = append(prs[0].Files, struct {
						Path string `json:"path"`
					}{next})
				} else {
					if err := os.WriteFile(filepath.Join(plans, filepath.Base(next)), []byte("# replacement\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			is := issue{Number: 1248, Title: "retired plan", Body: tc.body, CreatedAt: now, UpdatedAt: now, Author: user{Login: "ivanarama"}}
			for i, c := range tc.comments {
				is.Comments = append(is.Comments, comment{Author: user{Login: c[0]}, Body: c[1], CreatedAt: now.Add(time.Duration(i+1) * time.Minute)})
			}
			write := func(name string, value any) string {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(fixture, name)
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
				return file
			}
			issuesFile := write("issues.json", []issue{is})
			prsFile := write("prs.json", prs)
			//nolint:gosec // actual public CLI with test-owned input paths
			out, err := exec.Command(binary, "-issues", issuesFile, "-prs", prsFile, "-plans", plans, "-now", now.Format(time.RFC3339)).CombinedOutput()
			if err != nil {
				t.Fatalf("CLI: %v\n%s", err, out)
			}
			text := string(out)
			if tc.history {
				for _, want := range []string{"хвост чист", "исправленные ссылки на планы (справочно)", "явная замена", old, next} {
					if !strings.Contains(text, want) {
						t.Errorf("missing %q:\n%s", want, text)
					}
				}
				if strings.Contains(text, "застрявших заявок —") || strings.Contains(text, "ссылка на план, которого нет —") {
					t.Errorf("retired source remains active:\n%s", text)
				}
			} else {
				if !strings.Contains(text, "ссылка на план, которого нет —") || !strings.Contains(text, "#1248") {
					t.Errorf("active missing source hidden:\n%s", text)
				}
				if strings.Contains(text, "явная замена") {
					t.Errorf("unsupported replacement accepted:\n%s", text)
				}
			}
		})
	}
}
