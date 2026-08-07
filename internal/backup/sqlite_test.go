package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestDumpRestoreSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	backupDir := filepath.Join(dir, "backups")

	// 1) Create live DB with one row.
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t(name) VALUES('alpha')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	// 2) Dump (VACUUM INTO).
	outPath, err := DumpSQLite(ctx, dbPath, backupDir)
	if err != nil {
		t.Fatalf("DumpSQLite: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// 3) Modify live DB.
	db, _ = storage.ConnectSQLite(ctx, dbPath)
	if _, err := db.Exec(ctx, "INSERT INTO t(name) VALUES('beta')"); err != nil {
		t.Fatalf("insert beta: %v", err)
	}
	var n int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n)
	if n != 2 {
		t.Fatalf("live before restore: count = %d, want 2", n)
	}
	db.Close()

	// 4) Restore — must replace file, dropping the second row.
	if err := RestoreSQLite(ctx, dbPath, outPath); err != nil {
		t.Fatalf("RestoreSQLite: %v", err)
	}
	db, _ = storage.ConnectSQLite(ctx, dbPath)
	defer db.Close()
	_ = db.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n)
	if n != 1 {
		t.Fatalf("after restore: count = %d, want 1", n)
	}
	var name string
	_ = db.QueryRow(ctx, "SELECT name FROM t").Scan(&name)
	if name != "alpha" {
		t.Fatalf("after restore: name = %q, want alpha", name)
	}
	db.Close()

	// Previous live image is retained for operator rollback.
	oldDB, err := storage.ConnectSQLite(ctx, dbPath+".old")
	if err != nil {
		t.Fatalf("open .old: %v", err)
	}
	defer oldDB.Close()
	if err := oldDB.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil || n != 2 {
		t.Fatalf("old database count=%d err=%v, want 2", n, err)
	}
}

func TestRestoreSQLiteRejectsSameFileWithoutTruncating(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "same.db")
	db, err := storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(ctx, "CREATE TABLE t (v TEXT)")
	_, _ = db.Exec(ctx, "INSERT INTO t VALUES ('kept')")
	db.Close()

	err = RestoreSQLite(ctx, path, path)
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("expected same-file rejection, got %v", err)
	}
	db, err = storage.ConnectSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow(ctx, "SELECT v FROM t").Scan(&got); err != nil || got != "kept" {
		t.Fatalf("target changed after rejection: value=%q err=%v", got, err)
	}
}

func TestRestoreSQLiteRejectsCorruptBackupAndPreservesTarget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "live.db")
	bad := filepath.Join(dir, "bad.db")
	db, err := storage.ConnectSQLite(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(ctx, "CREATE TABLE t (v TEXT)")
	_, _ = db.Exec(ctx, "INSERT INTO t VALUES ('live')")
	db.Close()
	if err := os.WriteFile(bad, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSQLite(ctx, target, bad); err == nil {
		t.Fatal("corrupt backup must be rejected")
	}
	db, err = storage.ConnectSQLite(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow(ctx, "SELECT v FROM t").Scan(&got); err != nil || got != "live" {
		t.Fatalf("live target changed: value=%q err=%v", got, err)
	}
}

// Восстановление поверх открытой базы уничтожает её WAL и подменяет inode под
// живым процессом. Останавливать базу обязан вызывающий, но проверка занятости
// здесь — последний рубеж на случай, если очередной обработчик спросил живость
// не той функцией (issue #627).
func TestRestoreSQLiteRefusesWhileDatabaseInUse(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")

	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t(name) VALUES('alpha')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Копию снимаем на живой базе — это разрешено (VACUUM INTO не блокирует).
	outPath, err := DumpSQLite(ctx, dbPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("DumpSQLite: %v", err)
	}

	err = RestoreSQLite(ctx, dbPath, outPath)
	if err == nil {
		db.Close()
		t.Fatal("восстановление выполнено поверх открытой базы")
	}
	if !strings.Contains(err.Error(), "открыта другим процессом") {
		db.Close()
		t.Fatalf("ожидался отказ по занятости базы, получено: %v", err)
	}
	if _, statErr := os.Stat(dbPath + ".old"); statErr == nil {
		db.Close()
		t.Error("создана копия .old — восстановление дошло до подмены файла")
	}
	// База осталась рабочей: соединение живо и файл на месте.
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
		db.Close()
		t.Fatalf("база после отказа нечитаема: %v", err)
	}
	db.Close()

	// После остановки базы то же восстановление обязано пройти — проверка не
	// должна запирать нормальный сценарий.
	if err := RestoreSQLite(ctx, dbPath, outPath); err != nil {
		t.Fatalf("RestoreSQLite на остановленной базе: %v", err)
	}
}
