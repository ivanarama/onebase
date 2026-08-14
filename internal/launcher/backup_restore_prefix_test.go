package launcher

import (
	"context"
	"testing"

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
