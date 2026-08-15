// Package installtest — общий тест-инструментарий для проверок, которым нужен
// каталог установки, пригодный для самообновления.
//
// Зачем отдельный пакет. Самообновление намеренно отказывается работать в
// «общей» установке: каталог не должен быть доступен на запись группе и
// остальным, а между ним и корнем файловой системы обязана быть приватная
// граница (на Windows — профиль пользователя, см. selfupdate). Обычный
// t.TempDir() этому не удовлетворяет: /tmp открыт всем, а umask 002 делает
// сам каталог ещё и групповым на запись.
//
// Из-за этого шесть тестов в трёх пакетах (internal/cli, internal/launcher,
// internal/selfupdate) падали ВСЕГДА — и локально, и на первом же прогоне
// windows-раннера в CI (#924). Падение выглядело как дефект продукта, хотя
// продукт вёл себя правильно: это фикстура обещала то, чего не давала.
//
// Живёт отдельным пакетом, а не в internal/selfupdate, по той же причине, что
// dbtest: собственные тесты selfupdate лежат в package selfupdate и не могут
// импортировать пакет, который импортирует selfupdate.
package installtest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// PrivateInstallDir создаёт каталог установки, который selfupdate признаёт
// приватным, и возвращает путь к нему.
//
// На Windows каталог создаётся в НАСТОЯЩЕМ профиле пользователя: проверка
// намеренно не доверяет USERPROFILE/HOME (их можно направить в Program Files и
// обойти границу), поэтому подменой переменных окружения тут не обойтись.
// На остальных ОС — приватный (0700) предок внутри t.TempDir().
func PrivateInstallDir(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		profile, err := privateRoot()
		if err != nil || profile == "" {
			t.Skipf("не удалось определить профиль пользователя: %v", err)
		}
		dir, err := os.MkdirTemp(profile, "onebase-install-test-")
		if err != nil {
			t.Fatalf("создание приватного каталога установки: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) }) //nolint:gosec // G703: dir — точный результат MkdirTemp внутри профиля, а не путь от пользователя
		return dir
	}

	// Приватная граница — на предке: сам каталог установки остаётся обычным,
	// как у настоящей установки в домашнем каталоге пользователя.
	root := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("создание приватного корня: %v", err)
	}
	// t.TempDir() под umask 002 отдаёт 0775 — групповая запись, которую
	// selfupdate отвергает отдельной проверкой. Чиним явно, а не надеемся на
	// umask машины разработчика.
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // G302: 0700 и есть приватная граница, ради которой каталог создаётся
		t.Fatalf("права приватного корня: %v", err)
	}
	// 0755 намеренно: настоящая установка читаема и исполняема для всех, и
	// именно такую selfupdate обязан признавать своей. Права строже сделали бы
	// фикстуру непохожей на то, что она моделирует; приватность даёт предок.
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: см. комментарий выше — моделируем реальную установку
		t.Fatalf("создание каталога установки: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: то же
		t.Fatalf("права каталога установки: %v", err)
	}
	return dir
}

// CanonicalTempDir — t.TempDir(), приведённый к каноническому виду.
//
// Нужен там, где ожидание теста сравнивается с путём, который продукт уже
// канонизировал. На Windows-раннере TEMP приходит коротким именем 8.3
// (C:\Users\RUNNER~1\…), а канонизация разворачивает его в длинное
// (C:\Users\runneradmin\…); на macOS та же история с /var → /private/var.
// Сравнение с сырым t.TempDir() выглядит как дефект продукта, хотя продукт
// прав: канонический путь обязан быть каноническим.
func CanonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// PrivateHome — домашний каталог, от которого можно вычислить пригодную для
// самообновления установку.
//
// Отличается от PrivateInstallDir тем, что возвращает КОРЕНЬ (его подставляют в
// HOME/USERPROFILE), а не подкаталог bin: платформа складывает своё хозяйство
// внутрь сама.
func PrivateHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		profile, err := privateRoot()
		if err != nil || profile == "" {
			t.Skipf("не удалось определить профиль пользователя: %v", err)
		}
		dir, err := os.MkdirTemp(profile, "onebase-home-test-")
		if err != nil {
			t.Fatalf("создание приватного дома: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) }) //nolint:gosec // G703: dir — точный результат MkdirTemp внутри профиля
		return dir
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("создание приватного дома: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: 0700 — смысл приватного дома
		t.Fatalf("права приватного дома: %v", err)
	}
	return dir
}
