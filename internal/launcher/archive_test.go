package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

func zipNames(t *testing.T, raw []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("архив не читается: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

// Файловая конфигурация попадает в архив целиком, каталог backups/ — нет
// (иначе каждая следующая копия вкладывала бы предыдущую).
func TestAddConfigToZipPacksFilesAndSkipsBackups(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"app.yaml", "catalogs/Контрагент.yaml", "backups/old.obz"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("содержимое "+rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	b := &Base{ID: "b", ConfigSource: "file", Path: dir}
	if err := addConfigToZip(context.Background(), zw, b, "config/"); err != nil {
		t.Fatalf("addConfigToZip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got := zipNames(t, buf.Bytes())
	want := []string{"config/app.yaml", "config/catalogs/Контрагент.yaml"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("состав архива = %v, ожидался %v", got, want)
	}
}

// Главный случай. Раньше полный экспорт игнорировал недоступность конфигурации
// в БД: .obz собирался вообще без папки config/ и отдавался с HTTP 200.
// Резервная копия без конфигурации выглядит нормальной ровно до попытки
// восстановиться из неё, поэтому теперь это ошибка, а не пустой архив.
func TestAddConfigToZipFailsWhenConfigUnreadable(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "no-config-table.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close() // база есть, таблицы _onebase_config в ней нет
	t.Cleanup(CloseAuthPools)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	b := &Base{ID: "b", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}

	err = addConfigToZip(ctx, zw, b, "config/")
	if err == nil {
		t.Fatal("недоступная конфигурация должна быть ошибкой, а не пустым архивом")
	}
	if !strings.Contains(err.Error(), "_onebase_config") {
		t.Errorf("ошибка должна называть недоступный источник, получено: %v", err)
	}
}

// Восстановление раскладывает файлы, создавая вложенные каталоги.
func TestRestoreConfigDirWritesTree(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "catalogs", "К.yaml"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "app.yaml"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := restoreConfigDir(src, dst); err != nil {
		t.Fatalf("restoreConfigDir: %v", err)
	}
	for rel, want := range map[string]string{"app.yaml": "a", "catalogs/К.yaml": "k"} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel))) //nolint:gosec // G304: путь собран здесь же, в тесте
		if err != nil {
			t.Fatalf("%s не восстановлен: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, ожидалось %q", rel, got, want)
		}
	}
}

// Второй главный случай. Раньше сбой записи пропускался (`return nil` на чтении,
// непроверенные MkdirAll и WriteFile), восстановление доходило до конца и
// показывало «Полное восстановление выполнено: база данных + конфигурация».
//
// Сбой воспроизводится без игр с правами: каталог базы — обычный файл, поэтому
// MkdirAll под него не может создать путь.
func TestRestoreConfigDirReportsWriteFailure(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.yaml"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "не-каталог")
	if err := os.WriteFile(blocker, []byte("я файл"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := restoreConfigDir(src, blocker); err == nil {
		t.Fatal("сбой записи при восстановлении не должен выдаваться за успех")
	}
}
