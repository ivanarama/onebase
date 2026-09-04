package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFirstParentLogRecognizesMergeAndSquashPRs(t *testing.T) {
	raw := []byte(strings.Join([]string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\tfix(cache): one (#1091)",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\tMerge pull request #1092 from owner/branch",
		"cccccccccccccccccccccccccccccccccccccccc\tfix(queue): three (#1093)",
	}, "\n"))

	prs, err := parseFirstParentLog(raw)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, pr := range prs {
		numbers = append(numbers, pr.number)
	}
	if !reflect.DeepEqual(numbers, []int{1091, 1092, 1093}) {
		t.Fatalf("PR диапазона = %v, ожидались [1091 1092 1093]", numbers)
	}
}

func TestParseFirstParentLogRejectsCommitWithoutPRNumber(t *testing.T) {
	_, err := parseFirstParentLog([]byte(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\tdirect maintenance commit\n",
	))
	if err == nil || !strings.Contains(err.Error(), "не называет PR") {
		t.Fatalf("ошибка = %v, ожидался fail-closed для неизвестного commit", err)
	}
}

// Регрессия #1242 идёт через тот же CLI-слой и настоящий first-parent диапазон,
// что workflow. В v0.10.2 потерялись ровно первые четыре PR диапазона.
func TestRunReportsMissingFirstPRFromRealGitRange(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "releasecheck@example.test")
	runGit(t, repo, "config", "user.name", "Release Check")
	runGit(t, repo, "config", "commit.gpgsign", "false")
	runGit(t, repo, "config", "tag.gpgSign", "false")
	runGit(t, repo, "commit", "--allow-empty", "-m", "initial")
	runGit(t, repo, "tag", "v0.1.0")
	runGit(t, repo, "commit", "--allow-empty", "-m", "fix(public-files): one (#1091)")
	runGit(t, repo, "commit", "--allow-empty", "-m", "fix(http): two (#1092)")
	runGit(t, repo, "tag", "v0.2.0")

	notesPath := filepath.Join(repo, "release_v0.2.0.md")
	if err := os.WriteFile(notesPath, []byte("Исправлено в #1092.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("возврат в исходный каталог: %v", err)
		}
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-from", "v0.1.0",
		"-to", "v0.2.0",
		"-notes", notesPath,
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, ожидался 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "#1091") || strings.Contains(stderr.String(), "#1092  ") {
		t.Fatalf("stderr не называет только первый пропущенный PR: %q", stderr.String())
	}
}

func TestRunDoesNotCountHiddenMarkdownReferences(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "releasecheck@example.test")
	runGit(t, repo, "config", "user.name", "Release Check")
	runGit(t, repo, "config", "commit.gpgsign", "false")
	runGit(t, repo, "config", "tag.gpgSign", "false")
	runGit(t, repo, "commit", "--allow-empty", "-m", "initial")
	runGit(t, repo, "tag", "v0.1.0")
	runGit(t, repo, "commit", "--allow-empty", "-m", "fix(public-files): one (#1091)")
	runGit(t, repo, "tag", "v0.2.0")

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDirectory); err != nil {
			t.Errorf("возврат в исходный каталог: %v", err)
		}
	})

	tests := map[string]string{
		"link destination":          "[Текст без номера](https://example.invalid/release-notes#1091)\n",
		"unterminated HTML comment": "Видимый текст без номера.\n<!-- скрытая ссылка #1091\n",
	}
	for name, note := range tests {
		t.Run(name, func(t *testing.T) {
			notesPath := filepath.Join(repo, strings.ReplaceAll(name, " ", "-")+".md")
			if err := os.WriteFile(notesPath, []byte(note), 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := run([]string{
				"-from", "v0.1.0",
				"-to", "v0.2.0",
				"-notes", notesPath,
			}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("exit code = %d, ожидался 1; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "#1091") {
				t.Fatalf("stderr не называет пропущенный PR #1091: %q", stderr.String())
			}
		})
	}
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	//nolint:gosec // G204: фиксированный git и полностью заданные тестом аргументы, shell не используется.
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func TestCheckCoverageAcceptsExactOmissionWithReason(t *testing.T) {
	prs := []pullRequest{
		{number: 1091, commit: "aaaaaaaaaaaa", subject: "fix(public-files): one (#1091)"},
		{number: 1096, commit: "bbbbbbbbbbbb", subject: "Merge pull request #1096 from owner/docs"},
	}
	note := strings.Join([]string{
		"Исправлен отзыв публичных ссылок ([#1091](https://example.test/pull/1091)).",
		"<!-- release-check: omit #1096 reason=план не поставляет пользовательского поведения -->",
	}, "\n")

	result, err := checkCoverage(prs, note)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.missing) != 0 || len(result.omitted) != 1 || result.omitted[0].number != 1096 {
		t.Fatalf("неожиданный результат: %+v", result)
	}
}

func TestCheckCoverageRejectsMalformedOmission(t *testing.T) {
	_, err := checkCoverage(
		[]pullRequest{{number: 1096}},
		"<!-- release-check: omit #1096 -->",
	)
	if err == nil || !strings.Contains(err.Error(), "неверный marker") {
		t.Fatalf("ошибка = %v, ожидался отказ на marker без причины", err)
	}
}

func TestCheckCoverageRejectsStaleOmission(t *testing.T) {
	_, err := checkCoverage(
		[]pullRequest{{number: 1091}},
		strings.Join([]string{
			"Исправлено в #1091.",
			"<!-- release-check: omit #9999 reason=остаток от другой версии -->",
		}, "\n"),
	)
	if err == nil || !strings.Contains(err.Error(), "не входит") {
		t.Fatalf("ошибка = %v, ожидался отказ на чужое исключение", err)
	}
}

func TestParseNoteCoverageDoesNotCountOmissionAsMention(t *testing.T) {
	coverage, err := parseNoteCoverage("<!-- release-check: omit #1096 reason=служебная заметка -->")
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.mentions) != 0 {
		t.Fatalf("marker исключения посчитан видимым упоминанием: %v", coverage.mentions)
	}
	if coverage.omissions[1096] == "" {
		t.Fatal("исключение #1096 не разобрано")
	}
}

func TestParseNoteCoverageIgnoresHiddenReference(t *testing.T) {
	coverage, err := parseNoteCoverage(strings.Join([]string{
		"Видимый текст без номера.",
		"<!-- прежняя служебная ссылка #1091",
		"продолжается на второй строке -->",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.mentions) != 0 {
		t.Fatalf("скрытая HTML-ссылка посчитана упоминанием: %v", coverage.mentions)
	}
}
