package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/storage"
)

// Восстановление копии через ЛАУНЧЕР обязано гасить префикс базы (#871).
//
// Сброс (117D) был написан только в CLI (`internal/cli/backup.go`), а
// `restoreForBase` лаунчера звал backup.RestoreSQLite напрямую. Копия,
// восстановленная в другую базу через веб-интерфейс, сохраняла префикс
// оригинала и начинала выдавать его коды — ровно сценарий «обмен склеил бы
// разные объекты», ради предотвращения которого префикс и заводился.
//
// Проверка идёт через публичный хендлер восстановления, а не через функцию
// сброса: дефект был не в ней, а в том, что её никто не звал.
func setBasePrefix(t *testing.T, dbPath, prefix string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.SaveBasePrefix(ctx, prefix); err != nil {
		t.Fatalf("SaveBasePrefix: %v", err)
	}
}

func basePrefixOf(t *testing.T, dbPath string) string {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	return db.GetBasePrefix(ctx)
}

func TestBackupRestore_СбрасываетПрефиксБазы(t *testing.T) {
	h, b, dbPath := adoptedBase(t, "prefix-reset", false)
	stubExePath(t)

	// В копии префикс уже есть — снимаем её вместе с ним.
	setBasePrefix(t, dbPath, "Ф-")
	file := makeBackup(t, h, b)

	resp := postRestore(t, h, b, file)
	if errText, ok := resp["error"].(string); ok && errText != "" {
		t.Fatalf("восстановление не удалось: %s", errText)
	}

	if got := basePrefixOf(t, dbPath); got != "" {
		t.Fatalf("после восстановления префикс остался %q — клон будет выдавать коды оригинала", got)
	}

}

// Обратная сторона: когда префикса не было, восстановление ничего не трогает и
// не сочиняет сообщений.
func TestBackupRestore_БезПрефиксаНичегоНеМеняет(t *testing.T) {
	h, b, dbPath := adoptedBase(t, "prefix-none", false)
	stubExePath(t)

	file := makeBackup(t, h, b)
	resp := postRestore(t, h, b, file)
	if errText, ok := resp["error"].(string); ok && errText != "" {
		t.Fatalf("восстановление не удалось: %s", errText)
	}
	if got := basePrefixOf(t, dbPath); got != "" {
		t.Errorf("префикс появился из ниоткуда: %q", got)
	}
}

// Полный экспорт использует universal-ветку backupFullImport, которая
// возвращается раньше старого binary-кода. Поэтому обычный restore-тест выше
// не защищает этот второй публичный вход от повторения #871.
func TestBackupFullImportUniversal_СбрасываетПрефиксБазы(t *testing.T) {
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := storage.ConnectSQLite(ctx, sourcePath)
	if err != nil {
		t.Fatalf("ConnectSQLite source: %v", err)
	}
	if err := source.SaveBasePrefix(ctx, "ИСТ-"); err != nil {
		source.Close()
		t.Fatalf("SaveBasePrefix source: %v", err)
	}
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "app.yaml"), []byte("name: Prefix restore test\n"), 0o600); err != nil {
		source.Close()
		t.Fatalf("write config: %v", err)
	}
	var archive bytes.Buffer
	if err := backup.ExportUniversal(ctx, source, "file", configDir, "", "source", &archive); err != nil {
		source.Close()
		t.Fatalf("ExportUniversal: %v", err)
	}
	source.Close()

	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("open universal archive: %v", err)
	}
	entries := make([]fullImportTestEntry, 0, len(zr.File))
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var data bytes.Buffer
		if _, err := data.ReadFrom(r); err != nil {
			closeErr := r.Close()
			t.Fatalf("read %s: %v (close: %v)", f.Name, err, closeErr)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("close %s: %v", f.Name, err)
		}
		entries = append(entries, fullImportTestEntry{name: f.Name, data: data.Bytes()})
	}

	h, b, targetPath := adoptedBase(t, "prefix-reset-universal", false)
	// Universal-архив намеренно не переносит instance-local base.prefix, но
	// импорт до исправления сохранял префикс целевой базы. Контракт 117D после
	// любого restore требует погасить и его: восстановленная копия начинает
	// новую идентичность, а не продолжает нумерацию прежнего инстанса.
	setBasePrefix(t, targetPath, "ЦЕЛЬ-")
	resp := postFullImport(t, h, b, entries)
	if errText, ok := resp["error"].(string); ok && errText != "" {
		t.Fatalf("полное восстановление не удалось: %s", errText)
	}
	if got := basePrefixOf(t, targetPath); got != "" {
		t.Fatalf("после universal-восстановления префикс остался %q — полный импорт обошёл защиту", got)
	}
}
