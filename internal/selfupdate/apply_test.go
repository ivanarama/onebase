package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// installation готовит «установку» из двух бинарей и staging с новыми версиями.
// Имена намеренно свои, а не из PackageBinaries: Apply работает по списку
// staged.Files, и тест не должен зависеть от платформы.
func installation(t *testing.T) (targetDir string, staged StagedInfo) {
	t.Helper()
	targetDir = filepath.Join(t.TempDir(), "bin")
	stageDir := filepath.Join(t.TempDir(), "stage")
	for _, d := range []string{targetDir, stageDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"onebase-a", "onebase-b"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(targetDir, n), []byte("СТАРЫЙ-"+n), 0o755); err != nil { //nolint:gosec // G306: это исполняемый файл
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stageDir, n), []byte("НОВЫЙ-"+n), 0o755); err != nil { //nolint:gosec // G306: это исполняемый файл
			t.Fatal(err)
		}
	}
	return targetDir, StagedInfo{Tag: "build-672", Dir: stageDir, Files: names, Verified: true}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: путь из теста
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApply_ReplacesAllBinariesAndKeepsPrev(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)

	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(targetDir, n)); got != "НОВЫЙ-"+n {
			t.Fatalf("%s не заменён: %q", n, got)
		}
	}

	prev, err := PrevDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(prev, n)); got != "СТАРЫЙ-"+n {
			t.Fatalf("%s не сохранён для отката: %q", n, got)
		}
	}
	// После успешного применения staging не нужен — он весит десятки мегабайт.
	if _, err := os.Stat(staged.Dir); !os.IsNotExist(err) {
		t.Fatalf("каталог обновления не очищен (err=%v)", err)
	}
	// Резервных копий рядом с бинарями остаться не должно.
	if _, err := os.Stat(filepath.Join(targetDir, "onebase-a.old")); !os.IsNotExist(err) {
		t.Fatal(".old остался рядом с бинарём")
	}
}

func TestRollbackPrev_RestoresPreviousBinaries(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := RollbackPrev(targetDir); err != nil {
		t.Fatalf("RollbackPrev: %v", err)
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(targetDir, n)); got != "СТАРЫЙ-"+n {
			t.Fatalf("%s не откачен: %q", n, got)
		}
	}

	// Откат одноразовый: второй вызов обязан честно сказать, что возвращаться
	// уже некуда, а не отчитаться успехом о той же самой версии.
	if err := RollbackPrev(targetDir); err == nil {
		t.Fatal("повторный откат должен отказывать — предыдущей версии больше нет")
	}
}

func TestRollbackPrev_WithoutPrevFails(t *testing.T) {
	isolatedHome(t)
	targetDir, _ := installation(t)

	if err := RollbackPrev(targetDir); err == nil {
		t.Fatal("откатываться некуда — ждали ошибку")
	}
}

func TestApply_UnverifiedRefused(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	staged.Verified = false

	if err := Apply(staged, targetDir); err == nil {
		t.Fatal("непроверенное обновление применять нельзя")
	}
	if got := read(t, filepath.Join(targetDir, "onebase-a")); got != "СТАРЫЙ-onebase-a" {
		t.Fatalf("бинарь всё-таки подменили: %q", got)
	}
}

// Установка без GUI-бинаря: обновление не должно добавлять файлы, которых у
// пользователя не было.
func TestApply_SkipsBinariesAbsentInInstallation(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	if err := os.Remove(filepath.Join(targetDir, "onebase-b")); err != nil {
		t.Fatal(err)
	}

	if err := Apply(staged, targetDir); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "onebase-b")); !os.IsNotExist(err) {
		t.Fatal("обновление добавило бинарь, которого не было в установке")
	}
}

// Платформа из двух разных версий хуже, чем неудавшееся обновление: если второй
// бинарь заменить не удалось, первый обязан вернуться.
func TestApply_RollsBackOnPartialFailure(t *testing.T) {
	isolatedHome(t)
	targetDir, staged := installation(t)
	// Файл заявлен в обновлении, но в staging его нет — подмена сорвётся на
	// втором шаге, когда первый бинарь уже заменён.
	if err := os.Remove(filepath.Join(staged.Dir, "onebase-b")); err != nil {
		t.Fatal(err)
	}

	if err := Apply(staged, targetDir); err == nil {
		t.Fatal("ждали ошибку применения")
	}
	for _, n := range staged.Files {
		if got := read(t, filepath.Join(targetDir, n)); got != "СТАРЫЙ-"+n {
			t.Fatalf("%s остался от сорвавшегося обновления: %q", n, got)
		}
	}
}

func TestApply_NothingToReplace(t *testing.T) {
	isolatedHome(t)
	_, staged := installation(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Apply(staged, empty); err == nil {
		t.Fatal("в каталоге нет бинарей платформы — ждали ошибку")
	}
}

// Fetch обязан убедиться, что скачанный бинарь — той версии, за которую себя
// выдаёт: иначе обновление применит непонятно что.
func TestFetch_RefusesVersionMismatch(t *testing.T) {
	isolatedHome(t)

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "onebase.zip")
	entries := map[string]string{}
	for _, name := range PackageBinaries() {
		entries["dist/"+name] = "БИНАРЬ-" + name
	}
	writeZip(t, archivePath, entries)
	body, err := os.ReadFile(archivePath) //nolint:gosec // G304: путь из теста
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)

	mux := http.NewServeMux()
	mux.HandleFunc("/a.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/a.zip.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  a.zip\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	orig := binaryVersion
	t.Cleanup(func() { binaryVersion = orig })
	binaryVersion = func(string) (string, error) { return "build-100", nil }

	rel := Release{Tag: "build-672", AssetName: "a.zip", AssetURL: srv.URL + "/a.zip", SHAURL: srv.URL + "/a.zip.sha256"}
	if _, err := Fetch(context.Background(), rel); err == nil {
		t.Fatal("версия бинаря не совпала с тегом релиза — ждали отказ")
	}

	// Успешный путь: версия сошлась — обновление помечено готовым.
	binaryVersion = func(string) (string, error) { return "build-672", nil }
	staged, err := Fetch(context.Background(), rel)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !staged.Verified || staged.Tag != "build-672" {
		t.Fatalf("staged не готов: %+v", staged)
	}
	st, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !st.StagedReady() {
		t.Fatalf("состояние не запомнило скачанное обновление: %+v", st.Staged)
	}
}
