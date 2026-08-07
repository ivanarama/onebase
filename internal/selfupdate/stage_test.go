package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// writeTarGz собирает .tar.gz так же, как «Package (Unix)» в release.yml:
// содержимое лежит внутри каталога onebase-<goos>-<goarch>/.
func writeTarGz(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// packageEntries строит содержимое архива для текущей платформы: на Windows это
// два бинаря (консольный и GUI), на прочих — один.
func packageEntries(prefix string) map[string]string {
	out := make(map[string]string)
	for _, name := range PackageBinaries() {
		out[prefix+name] = "СОДЕРЖИМОЕ-" + name
	}
	out[prefix+"README.md"] = "не бинарь"
	return out
}

func TestStageAll_FromZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "onebase-windows-amd64.zip")
	writeZip(t, archive, packageEntries("onebase-windows-amd64/"))

	files, err := StageAll(archive, filepath.Join(dir, "stage"))
	if err != nil {
		t.Fatalf("StageAll: %v", err)
	}
	if len(files) != len(PackageBinaries()) {
		t.Fatalf("извлечено %d файлов, ждали %d: %v", len(files), len(PackageBinaries()), files)
	}
	for _, name := range PackageBinaries() {
		data, err := os.ReadFile(files[name]) //nolint:gosec // G304: путь из теста
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(data) != "СОДЕРЖИМОЕ-"+name {
			t.Fatalf("%s: содержимое %q", name, data)
		}
	}
}

func TestStageAll_FromTarGz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "onebase-linux-amd64.tar.gz")
	writeTarGz(t, archive, packageEntries("onebase-linux-amd64/"))

	files, err := StageAll(archive, filepath.Join(dir, "stage"))
	if err != nil {
		t.Fatalf("StageAll: %v", err)
	}
	if files[BinaryName()] == "" {
		t.Fatalf("основной бинарь не извлечён: %v", files)
	}
}

func TestStageAll_MissingMainBinary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.zip")
	writeZip(t, archive, map[string]string{"README.md": "только документация"})

	if _, err := StageAll(archive, filepath.Join(dir, "stage")); err == nil {
		t.Fatal("ждали ошибку об отсутствии основного бинаря")
	}
}

func TestStageAll_DuplicateBinaryRefused(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.zip")
	writeZip(t, archive, map[string]string{
		"a/" + BinaryName(): "первый",
		"b/" + BinaryName(): "второй",
	})

	if _, err := StageAll(archive, filepath.Join(dir, "stage")); err == nil {
		t.Fatal("два разных бинаря с одним именем — неоднозначность, ждали ошибку")
	}
}

func TestStageAll_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.rar")
	if err := os.WriteFile(archive, []byte("не архив"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StageAll(archive, filepath.Join(dir, "stage")); err == nil {
		t.Fatal("ждали отказ по формату архива")
	}
}

func TestStageAll_OversizedEntryRefused(t *testing.T) {
	// Заголовок tar заявляет размер больше лимита — распаковывать не начинаем.
	// Настоящий файл такого размера в тесте не создаём: проверяется именно
	// защита от заявленного объёма.
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "dist/" + BinaryName(), Mode: 0o755, Size: MaxBinaryBytes + 1, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	// Данные не пишем: tar останется незавершённым, но заголовок уже читается —
	// этого достаточно, ошибки закрытия здесь ожидаемы.
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	if _, err := StageAll(archive, filepath.Join(dir, "stage")); err == nil {
		t.Fatal("ждали отказ по превышению лимита размера")
	}
}
