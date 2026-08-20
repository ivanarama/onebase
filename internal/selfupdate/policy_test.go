package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writePolicy(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, PolicyFileName), []byte(body), 0o644); err != nil { //nolint:gosec // G306: файл политики читают все, права как у fsmode.File
		t.Fatal(err)
	}
}

func TestLoadPolicy_AbsentAllowsEverything(t *testing.T) {
	p := LoadPolicy(t.TempDir())
	if !p.UIAllowed() || !p.CheckAllowed() {
		t.Fatalf("без файла политики всё должно быть разрешено: %+v", p)
	}
	if p.ChannelLocked() {
		t.Fatal("канал не должен быть зафиксирован")
	}
	if p.RepoOr("") != DefaultRepo {
		t.Fatalf("репозиторий по умолчанию %q", p.RepoOr(""))
	}
}

func TestLoadPolicy_DisablesUI(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "updates:\n  ui: false\n")

	p := LoadPolicy(dir)
	if p.UIAllowed() {
		t.Fatal("ui: false должно прятать средства обновления")
	}
	if !p.CheckAllowed() {
		t.Fatal("check не задан — проверки остаются разрешёнными")
	}
}

// Ключевой случай: yaml.v3 следует YAML 1.2, где off/on/yes/no — строки, а не
// булевы. Администратор пишет привычное `check: off` и обязан получить
// выключенную проверку, а не молча проигнорированную настройку.
func TestLoadPolicy_OffIsBoolean(t *testing.T) {
	for _, val := range []string{"off", "no", "false", "0", "OFF"} {
		dir := t.TempDir()
		writePolicy(t, dir, "updates:\n  check: "+val+"\n")
		if LoadPolicy(dir).CheckAllowed() {
			t.Fatalf("check: %s должно запрещать сетевые проверки", val)
		}
	}
	for _, val := range []string{"on", "yes", "true", "1"} {
		dir := t.TempDir()
		writePolicy(t, dir, "updates:\n  check: "+val+"\n")
		if !LoadPolicy(dir).CheckAllowed() {
			t.Fatalf("check: %s должно разрешать проверки", val)
		}
	}
}

func TestLoadPolicy_ChannelAndRepo(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "updates:\n  channel: stable\n  repo: myorg/onebase-dist\n")

	p := LoadPolicy(dir)
	if p.ChannelOr(ChannelBuild) != ChannelStable {
		t.Fatalf("канал %q, ждали stable", p.ChannelOr(ChannelBuild))
	}
	if !p.ChannelLocked() {
		t.Fatal("канал задан политикой — он должен считаться зафиксированным")
	}
	if p.RepoOr(DefaultRepo) != "myorg/onebase-dist" {
		t.Fatalf("репозиторий %q", p.RepoOr(DefaultRepo))
	}
}

// Битая политика трактуется как запрет: администратор с опечаткой должен
// увидеть пропавшую кнопку, а не разрешённое обновление.
func TestLoadPolicy_BrokenYAMLDenies(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "updates:\n  ui: [не булево\n")

	p := LoadPolicy(dir)
	if p.UIAllowed() || p.CheckAllowed() {
		t.Fatalf("битая политика должна запрещать обновление: %+v", p)
	}
}

func TestLoadPolicy_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "updates:\n  ui: true\n  check: true\n")
	t.Setenv(EnvUpdates, "off")

	p := LoadPolicy(dir)
	if p.UIAllowed() || p.CheckAllowed() {
		t.Fatalf("ONEBASE_UPDATES=off должен перекрывать файл: %+v", p)
	}
}

func TestCanWriteBinaryDir(t *testing.T) {
	dir := t.TempDir()
	if !CanWriteBinaryDir(dir) {
		t.Fatal("во временный каталог писать можно")
	}

	if runtime.GOOS == "windows" {
		// На Windows права каталога через chmod не выставить, а разбирать ACL
		// в тесте бессмысленно: проверка и в бою работает пробой записи.
		t.Skip("проверка запрета на запись не воспроизводится на Windows")
	}
	ro := filepath.Join(dir, "readonly")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("под root права каталога не ограничивают запись")
	}
	if CanWriteBinaryDir(ro) {
		t.Fatal("в каталог без прав на запись обновляться нельзя")
	}
}

// Причина отказа должна быть машиночитаемой: интерфейс и CLI формулируют
// «нет прав» и «общая установка» по-разному, потому что лечатся они разным.
func TestValidateBinaryUpdateTarget_NotWritableIsTypedReason(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого-каталога")
	err := ValidateBinaryUpdateTarget(missing)
	if !errors.Is(err, ErrTargetNotWritable) {
		t.Fatalf("ValidateBinaryUpdateTarget(%q) = %v, ждали ErrTargetNotWritable", missing, err)
	}
	if errors.Is(err, ErrTargetShared) {
		t.Error("отсутствие каталога выдано за общую установку")
	}
}
