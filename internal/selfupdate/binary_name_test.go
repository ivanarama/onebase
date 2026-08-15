package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// privateInstallDirLocal — каталог установки, который проверка приватности
// признаёт своим. После мержа #931 заменяется на общий installtest.
func privateInstallDirLocal(t *testing.T) string {
	t.Helper()
	if root := windowsTestPrivateInstallRoot(); root != "" {
		dir, err := os.MkdirTemp(root, "onebase-name-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) }) //nolint:gosec // G703: точный результат MkdirTemp внутри профиля
		return dir
	}
	root := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // G302: 0700 и есть приватная граница
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: моделируем реальную установку
		t.Fatal(err)
	}
	return dir
}

// Бинарь, названный не onebase[.exe], обязан работать (#831).
//
// Гейт поколения запускал `<каталог>/onebase[.exe] --version` по жёстко зашитому
// имени. Для сборки под другим именем (`go build -o my-onebase`, хранение
// версий рядом: onebase-0.9.8.exe) этого файла попросту нет — и НИ ОДНА команда
// не выполнялась, включая check и run, которые к обновлению отношения не имеют.
// Сообщение при этом уводило в сторону: «binary package is unavailable during
// update recovery», хотя обновление никто не запускал.
//
// Проверяется именно то, ЧТО спрашивают: путь, переданный в определение версии,
// должен быть текущим исполняемым файлом, а не именем из каталога установки.
func TestConsumerLease_ПроверяетПоколениеТекущегоБинаря(t *testing.T) {
	dir := privateInstallDirLocal(t)
	self := filepath.Join(dir, "ob-2026-08-13.exe")

	oldPath, oldVersion := currentBinaryPath, consumerBinaryVersion
	t.Cleanup(func() { currentBinaryPath, consumerBinaryVersion = oldPath, oldVersion })

	currentBinaryPath = func() (string, error) { return self, nil }
	var asked string
	consumerBinaryVersion = func(path string) (string, error) {
		asked = path
		return "build-1", nil
	}

	lease, err := acquireConsumerLease(dir, "build-1")
	if err != nil {
		t.Fatalf("лизинг не выдан бинарю под своим именем: %v", err)
	}
	t.Cleanup(func() { _ = lease.Release() })

	if asked != self {
		t.Fatalf("версию спросили у %q, а надо у текущего бинаря %q", asked, self)
	}
	if strings.HasSuffix(asked, BinaryName()) {
		t.Errorf("гейт снова смотрит на жёстко зашитое имя %q", BinaryName())
	}
}
