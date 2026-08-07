package cli

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
)

// setSupportFlag ставит флаг команды и возвращает его в исходное состояние
// после теста: supportCmd — пакетная переменная, зарегистрированная в root.
func setSupportFlag(t *testing.T, name, value string) {
	t.Helper()
	f := supportCmd.Flags().Lookup(name)
	if f == nil {
		t.Fatalf("у команды support нет флага %q", name)
	}
	old := f.Value.String()
	if err := supportCmd.Flags().Set(name, value); err != nil {
		t.Fatalf("установить --%s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = supportCmd.Flags().Set(name, old)
		f.Changed = false
	})
}

func TestSupport_WritesBundleWithoutBase(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.zip")
	setSupportFlag(t, "out", out)
	setSupportFlag(t, "message", "не проводится реализация")
	// Без журналов: команда не должна лезть в реестр баз пользователя.
	setSupportFlag(t, "no-logs", "true")

	if err := runSupport(supportCmd, nil); err != nil {
		t.Fatalf("runSupport: %v", err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("пакет не открывается: %v", err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) != 1 || zr.File[0].Name != "report.md" {
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Fatalf("в пакете %v, ожидался только report.md", names)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	for _, want := range []string{"не проводится реализация", "## Окружение", "onebase"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("в отчёте нет %q:\n%s", want, body)
		}
	}
}

func TestSupport_ReadsAppYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	appYAML := "name: Торговля\nversion: 2.4.1\nsupport: help@trade.ru\n"
	if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), []byte(appYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	var in bugreport.Input
	got := readAppSupport(dir, &in)
	if got != "help@trade.ru" {
		t.Errorf("контакт поддержки = %q", got)
	}
	if in.AppName != "Торговля" || in.AppVersion != "2.4.1" {
		t.Errorf("конфигурация прочитана как %q %q", in.AppName, in.AppVersion)
	}

	// Каталог без конфигурации не должен ломать сбор отчёта.
	if s := readAppSupport(t.TempDir(), &in); s != "" {
		t.Errorf("для пустого каталога ожидался пустой контакт, получено %q", s)
	}
	if s := readAppSupport("", &in); s != "" {
		t.Errorf("без --project ожидался пустой контакт, получено %q", s)
	}
}

func TestSupportOutPath_DefaultsToProfile(t *testing.T) {
	now := time.Date(2026, 8, 7, 14, 32, 11, 0, time.UTC)
	explicit, err := supportOutPath(`C:\tmp\r.zip`, now)
	if err != nil || explicit != `C:\tmp\r.zip` {
		t.Fatalf("явный --out потерян: %q, %v", explicit, err)
	}
	def, err := supportOutPath("  ", now)
	if err != nil {
		t.Fatalf("supportOutPath: %v", err)
	}
	if !strings.HasSuffix(def, filepath.Join(".onebase", "reports", "onebase-report-20260807-143211.zip")) {
		t.Errorf("путь по умолчанию = %q", def)
	}
}

func TestSanitizeLogLabel(t *testing.T) {
	cases := map[string]string{
		"":              "base",
		"   ":           "base",
		"Торговля":      "Торговля",
		`Демо: C:\база`: "Демо__C__база",
		"учёт/склад":    "учёт_склад",
		"с пробелом":    "с_пробелом",
	}
	for in, want := range cases {
		if got := sanitizeLogLabel(in); got != want {
			t.Errorf("sanitizeLogLabel(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
