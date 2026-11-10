package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Компиляция Gherkin проверяется через CLI-команду и полный прогон тестов на
// копии каркаса: компилятор должен дать тест-обработки, которые раннер
// выполняет без провалов, а не только собрать текст модуля.
func TestGherkinCompileRunsCompiledTestsOnSkeleton(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "sppr")
	if err := os.CopyFS(projectDir, os.DirFS(filepath.Join("..", "..", "examples", "sppr"))); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*cobra.Command{gherkinCompileCmd, migrateCmd, testCmd} {
		for _, name := range []string{"project", "sqlite", "run"} {
			flag := cmd.Flags().Lookup(name)
			if flag == nil {
				continue
			}
			previous, changed := flag.Value.String(), flag.Changed
			t.Cleanup(func() {
				_ = flag.Value.Set(previous)
				flag.Changed = changed
			})
		}
	}
	previousContext := rootCmd.Context()
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetContext(previousContext)
	})
	rootCmd.SetContext(context.Background())
	dbPath := filepath.Join(t.TempDir(), "sppr.db")

	var out string
	var err error
	out, err = captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"gherkin", "compile", "--project", projectDir})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("onebase gherkin compile: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Тестов: 3") {
		t.Fatalf("компиляция не посчитала строки примеров:\n%s", out)
	}
	yamlPath := filepath.Join(projectDir, "processors", "тестрасстановкафункций_объект.yaml")
	body, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("тест-обработка не записана: %v", err)
	}
	if !strings.Contains(string(body), "kind: test") {
		t.Fatalf("у записанной обработки нет kind: test:\n%s", body)
	}
	srcPath := filepath.Join(projectDir, "src", "тестрасстановкафункций_объект.proc.os")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "//   То функция объекта «internal/registry» имеет код «2.1»") {
		t.Fatalf("шаг-таблица потеряна в модуле:\n%s", src)
	}

	// компиляция детерминирована: повторный прогон даёт тот же дифф
	first := append([]byte(nil), src...)
	out, err = captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"gherkin", "compile", "--project", projectDir})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("повторная компиляция: %v\n%s", err, out)
	}
	again, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(first) {
		t.Fatalf("повторная компиляция изменила модуль:\n%s\n---\n%s", first, again)
	}

	for _, args := range [][]string{
		{"migrate", "--project", projectDir, "--sqlite", dbPath},
		{"test", "--project", projectDir, "--sqlite", dbPath},
	} {
		rootCmd.SetArgs(args)
		out, err = captureStdout(t, rootCmd.Execute)
		if err != nil {
			t.Fatalf("onebase %s: %v\n%s", args[0], err, out)
		}
	}
	if !strings.Contains(out, "ТестРасстановкаФункций_Объект") {
		t.Fatalf("раннер не увидел скомпилированный тест:\n%s", out)
	}
	if !strings.Contains(out, "провалено: 0") {
		t.Fatalf("прогон тестов дал провалы:\n%s", out)
	}
}

// Неизвестный шаг — компиляция отказывается и не пишет половину теста.
func TestGherkinCompileRejectsUnmatchedStep(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "sppr")
	if err := os.CopyFS(projectDir, os.DirFS(filepath.Join("..", "..", "examples", "sppr"))); err != nil {
		t.Fatal(err)
	}
	feature := filepath.Join(projectDir, "features", "чужой.feature")
	if err := os.WriteFile(feature, []byte("Функция: Чужой\nСценарий: С\n  Дано шаг которого нет в реестре\n  Тогда трассировка содержит запись функции «2.1» для объекта «x»\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"gherkin", "compile", "--project", projectDir})
		return rootCmd.Execute()
	})
	if err == nil {
		t.Fatalf("компиляция приняла шаг вне реестра:\n%s", out)
	}
	if !strings.Contains(err.Error(), "не сопоставлен с реестром") {
		t.Fatalf("ошибка не про шаг: %v", err)
	}
	// компиляция вся целиком: отказ для одного сценария не пишет ничего,
	// половина теста записанным компилятора нет
	matches, err := filepath.Glob(filepath.Join(projectDir, "processors", "тестрасстановка*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 0 {
		t.Fatalf("отказ не отменил запись сопоставленных сценариев: %v", matches)
	}
}
