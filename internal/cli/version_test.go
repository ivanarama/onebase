package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/version"
)

// Проверка идёт через argv и диспетчеризацию cobra, а не вызовом RunE: заявку
// #1052 завело именно то, что команды `version` не существовало — вызов RunE
// напрямую этого бы не заметил.
func runRootVersion(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"version"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("`onebase version`: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	got := runRootVersion(t)
	want := "onebase version " + version.String()
	if !strings.Contains(got, want) {
		t.Errorf("вывод не содержит %q:\n%s", want, got)
	}
}

// Ради этой строки команда и заведена: «версия не та» почти всегда означает
// «отвечает другой бинарь», и путь отвечает на это без переписки.
func TestVersionCommandPrintsExecutablePath(t *testing.T) {
	got := runRootVersion(t)
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("путь исполняемого файла недоступен: %v", err)
	}
	if !strings.Contains(got, exe) {
		t.Errorf("в выводе нет пути %q — по такому ответу нельзя понять, какой бинарь его дал:\n%s", exe, got)
	}
	if !strings.Contains(got, filepath.Base(exe)) {
		t.Errorf("в выводе нет даже имени файла:\n%s", got)
	}
}

func TestVersionCommandPrintsPlatform(t *testing.T) {
	got := runRootVersion(t)
	for _, want := range []string{"платформа:", "Go go1."} {
		if !strings.Contains(got, want) {
			t.Errorf("вывод не содержит %q:\n%s", want, got)
		}
	}
}

// Лишние аргументы — ошибка, а не молчаливое игнорирование: `onebase version
// --json` должен сказать, что такого не умеет, а не притвориться, что понял.
func TestVersionCommandRejectsArgs(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"version", "лишний"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	if err := rootCmd.Execute(); err == nil {
		t.Fatalf("лишний аргумент принят молча:\n%s", out.String())
	}
}
