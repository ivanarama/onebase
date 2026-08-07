package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/selfupdate"
	"github.com/spf13/cobra"
)

// updateCmdFor собирает команду обновления, нацеленную на временный «каталог
// установки»: тест не должен ни трогать настоящий бинарь, ни ходить в сеть.
func updateCmdFor(t *testing.T, binDir string, args ...string) *cobra.Command {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cmd := &cobra.Command{Use: "update"}
	registerUpdateFlags(cmd)
	all := append([]string{"--target", binDir}, args...)
	if err := cmd.Flags().Parse(all); err != nil {
		t.Fatalf("разбор флагов: %v", err)
	}
	return cmd
}

func TestUpdateContext_DefaultChannel(t *testing.T) {
	uc, err := newUpdateContext(updateCmdFor(t, t.TempDir()))
	if err != nil {
		t.Fatalf("newUpdateContext: %v", err)
	}
	if uc.channel != selfupdate.DefaultChannel {
		t.Fatalf("канал %q, ждали %q", uc.channel, selfupdate.DefaultChannel)
	}
	if uc.repo != selfupdate.DefaultRepo {
		t.Fatalf("репозиторий %q, ждали %q", uc.repo, selfupdate.DefaultRepo)
	}
}

func TestUpdateContext_ChannelFlag(t *testing.T) {
	uc, err := newUpdateContext(updateCmdFor(t, t.TempDir(), "--channel", "stable"))
	if err != nil {
		t.Fatalf("newUpdateContext: %v", err)
	}
	if uc.channel != selfupdate.ChannelStable {
		t.Fatalf("канал %q, ждали stable", uc.channel)
	}
}

func TestUpdateContext_UnknownChannelRejected(t *testing.T) {
	if _, err := newUpdateContext(updateCmdFor(t, t.TempDir(), "--channel", "nightly")); err == nil {
		t.Fatal("ждали ошибку про неизвестный канал")
	}
}

// Политика администратора сильнее флага: на общей установке пользователь не
// должен уводить платформу на другой канал.
func TestUpdateContext_PolicyLocksChannel(t *testing.T) {
	binDir := t.TempDir()
	writeUpdatePolicy(t, binDir, "updates:\n  channel: stable\n")

	uc, err := newUpdateContext(updateCmdFor(t, binDir))
	if err != nil {
		t.Fatalf("newUpdateContext: %v", err)
	}
	if uc.channel != selfupdate.ChannelStable {
		t.Fatalf("политика задала stable, получили %q", uc.channel)
	}

	_, err = newUpdateContext(updateCmdFor(t, binDir, "--channel", "build"))
	if err == nil || !strings.Contains(err.Error(), "зафиксирован политикой") {
		t.Fatalf("смена зафиксированного канала должна отвергаться, получили %v", err)
	}
}

func TestUpdateContext_PolicyRepo(t *testing.T) {
	binDir := t.TempDir()
	writeUpdatePolicy(t, binDir, "updates:\n  repo: myorg/onebase-dist\n")

	uc, err := newUpdateContext(updateCmdFor(t, binDir, "--repo", "other/repo"))
	if err != nil {
		t.Fatalf("newUpdateContext: %v", err)
	}
	if uc.repo != "myorg/onebase-dist" {
		t.Fatalf("репозиторий %q — политика должна быть сильнее флага", uc.repo)
	}
}

// Офлайн-контур: сетевые проверки запрещены, команда обязана сказать об этом
// внятно и не ходить наружу.
func TestUpdateCheck_ForbiddenByPolicy(t *testing.T) {
	binDir := t.TempDir()
	writeUpdatePolicy(t, binDir, "updates:\n  check: off\n")

	err := runUpdateCheck(updateCmdFor(t, binDir, "--check"))
	if err == nil || !strings.Contains(err.Error(), "запрещена политикой") {
		t.Fatalf("ждали отказ по политике, получили %v", err)
	}
}

func TestUpdateNetwork_ForbiddenByPolicy(t *testing.T) {
	binDir := t.TempDir()
	writeUpdatePolicy(t, binDir, "updates:\n  check: off\n")

	err := runUpdateNetwork(updateCmdFor(t, binDir))
	if err == nil || !strings.Contains(err.Error(), "запрещено политикой") {
		t.Fatalf("ждали отказ по политике, получили %v", err)
	}
}

// Офлайн-путь без контрольной суммы запрещён — это защита, а не формальность.
func TestUpdateOffline_RequiresChecksum(t *testing.T) {
	binDir := t.TempDir()
	archive := filepath.Join(t.TempDir(), "onebase.zip")
	if err := os.WriteFile(archive, []byte("архив"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runUpdateOffline(updateCmdFor(t, binDir, "--from", archive))
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("ждали требование --sha256, получили %v", err)
	}
}

// Служба не указана — это допустимый режим (десктопная установка с лаунчером),
// команда не должна на этом падать.
func TestResolveService_OptionalService(t *testing.T) {
	svc, healthz, err := resolveService(updateCmdFor(t, t.TempDir()))
	if err != nil {
		t.Fatalf("resolveService: %v", err)
	}
	if svc != "" {
		t.Fatalf("имя службы %q, ждали пустое", svc)
	}
	if !strings.Contains(healthz, "/healthz") {
		t.Fatalf("URL пробы %q", healthz)
	}
}

func writeUpdatePolicy(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, selfupdate.PolicyFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
