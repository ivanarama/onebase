package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/installtest"
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

func TestApplyStagedBlocksGenerationBoundRecovery(t *testing.T) {
	// Каталог установки обязан быть приватным: обычный t.TempDir() лежит в
	// общем /tmp, и selfupdate законно отказывается такое обновлять — тест
	// падал на подготовке, а выглядело это как дефект продукта (#924).
	binDir := installtest.PrivateInstallDir(t)
	cmd := updateCmdFor(t, binDir)
	if err := selfupdate.SaveState(selfupdate.State{
		RestartRecords: []selfupdate.RestartRecord{{ID: "base-1", Generation: "ct1:pending"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := applyStaged(cmd, updateContext{targetDir: binDir}, selfupdate.StagedInfo{Tag: "build-999", Verified: true})
	if err == nil || !strings.Contains(err.Error(), "восстановлен") {
		t.Fatalf("generation-bound recovery did not block CLI apply: %v", err)
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

// Отказ должен советовать выполнимое. Общая установка не лечится правами:
// правило смотрит на расположение каталога, поэтому «обратитесь к
// администратору» здесь — ложный след, стоивший пользователю похода за правами.
func TestUpdateTargetRefusal_ExplainsReasonSeparately(t *testing.T) {
	const dir = `C:\Projects\onebase`

	shared := updateTargetRefusal("обновление платформы", dir,
		fmt.Errorf("%w: %s is outside the private user profile", selfupdate.ErrTargetShared, dir)).Error()
	for _, want := range []string{dir, "общий каталог", "Запуск от администратора этого не меняет", "распаковав архив выпуска"} {
		if !strings.Contains(shared, want) {
			t.Errorf("в отказе общей установке нет %q: %s", want, shared)
		}
	}

	notWritable := updateTargetRefusal("обновление платформы", dir,
		fmt.Errorf("%w: %s", selfupdate.ErrTargetNotWritable, dir)).Error()
	if !strings.Contains(notWritable, "нет прав на запись") {
		t.Errorf("отказ по правам должен называть права: %s", notWritable)
	}
	if strings.Contains(notWritable, "общий каталог") {
		t.Errorf("причины перепутаны: %s", notWritable)
	}
}
