package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

func createRestoreTestDatabase(t *testing.T, path string, values ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteFileURI(path, "mode=rwc"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA synchronous=FULL"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE restore_values (value TEXT NOT NULL)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	for _, value := range values {
		if _, err := db.Exec("INSERT INTO restore_values(value) VALUES (?)", value); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func sqliteRestoreTestValues(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteFileURI(path, "mode=ro&immutable=1"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT value FROM restore_values ORDER BY rowid")
	if err != nil {
		_ = db.Close()
		t.Fatalf("read %s: %v", path, err)
	}
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			_ = db.Close()
			t.Fatal(err)
		}
		values = append(values, value)
	}
	err = errors.Join(rows.Err(), rows.Close(), db.Close())
	if err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	return values
}

func requireSQLiteRestoreTestValues(t *testing.T, path string, want ...string) {
	t.Helper()
	got := sqliteRestoreTestValues(t, path)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("values in %s = %q, want %q", path, got, want)
	}
}

func copyRestoreTestFile(source, destination string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, contents, 0o600) //nolint:gosec // G703: destination is a test-owned temp path passed explicitly by each caller
}

// createWALCrashImage copies a quiescent but still-open WAL database. The
// committed second value exists only in its WAL, just as after an abrupt
// process stop before the last-close checkpoint.
func createWALCrashImage(t *testing.T, destination, baseValue, walValue string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "wal-origin.db")
	db, err := sql.Open("sqlite", sqliteFileURI(origin, "mode=rwc"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE restore_values (value TEXT NOT NULL)",
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := db.Exec("INSERT INTO restore_values(value) VALUES (?)", baseValue); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO restore_values(value) VALUES (?)", walValue); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	walInfo, err := os.Stat(origin + "-wal")
	if err != nil || walInfo.Size() <= 32 {
		_ = db.Close()
		t.Fatalf("expected committed WAL pages: info=%v err=%v", walInfo, err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyRestoreTestFile(origin+suffix, destination+suffix); err != nil {
			_ = db.Close()
			t.Fatalf("copy crash image %s: %v", suffix, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Prove that the second row is not already present in the copied main file.
	mainOnly := destination + ".main-only"
	if err := copyRestoreTestFile(destination, mainOnly); err != nil {
		t.Fatal(err)
	}
	requireSQLiteRestoreTestValues(t, mainOnly, baseValue)
}

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

func TestRestoreSQLiteIncludesBackupWALInPublishedDatabase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "backup-with-wal.db")
	createRestoreTestDatabase(t, target, "old")
	createWALCrashImage(t, backupPath, "backup-main", "backup-wal")

	if err := RestoreSQLite(context.Background(), target, backupPath); err != nil {
		t.Fatalf("RestoreSQLite: %v", err)
	}
	requireSQLiteRestoreTestValues(t, target, "backup-main", "backup-wal")
	requireSQLiteRestoreTestValues(t, target+".old", "old")
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(target + suffix); !os.IsNotExist(err) {
			t.Fatalf("published database retained sidecar %s: %v", suffix, err)
		}
	}
}

func TestRestoreSQLiteOldSnapshotIncludesCommittedWAL(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "replacement.db")
	walImage := filepath.Join(dir, "stopped.db")
	createRestoreTestDatabase(t, target, "placeholder")
	createRestoreTestDatabase(t, backupPath, "new")
	createWALCrashImage(t, walImage, "old-main", "old-wal")

	injected := false
	ctx := withSQLiteRestoreCutpoint(context.Background(), func(name string) error {
		if name != sqliteRestoreAfterStage || injected {
			return nil
		}
		injected = true
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			_ = os.Remove(target + suffix)
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := copyRestoreTestFile(walImage+suffix, target+suffix); err != nil {
				return err
			}
		}
		return nil
	})
	if err := RestoreSQLite(ctx, target, backupPath); err != nil {
		t.Fatalf("RestoreSQLite: %v", err)
	}
	if !injected {
		t.Fatal("after-stage cutpoint was not reached")
	}
	requireSQLiteRestoreTestValues(t, target, "new")
	requireSQLiteRestoreTestValues(t, target+".old", "old-main", "old-wal")
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(target + ".old" + suffix); !os.IsNotExist(err) {
			t.Fatalf(".old is not standalone; sidecar %s remains: %v", suffix, err)
		}
	}
}

func TestRestoreSQLiteCutpointsLeaveOldOrNewStandaloneDatabase(t *testing.T) {
	stop := errors.New("simulated crash boundary")
	tests := []struct {
		name       string
		cutpoint   string
		targetWant string
		oldExists  bool
	}{
		{name: "staged", cutpoint: sqliteRestoreAfterStage, targetWant: "old"},
		{name: "old-published", cutpoint: sqliteRestoreAfterOldPublished, targetWant: "old", oldExists: true},
		{name: "checkpointed", cutpoint: sqliteRestoreAfterCheckpoint, targetWant: "old", oldExists: true},
		{name: "sidecars-hidden", cutpoint: sqliteRestoreAfterSidecarsHidden, targetWant: "old", oldExists: true},
		{name: "target-published", cutpoint: sqliteRestoreAfterTargetPublish, targetWant: "new", oldExists: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "live.db")
			backupPath := filepath.Join(dir, "replacement.db")
			createRestoreTestDatabase(t, target, "old")
			createRestoreTestDatabase(t, backupPath, "new")
			ctx := withSQLiteRestoreCutpoint(context.Background(), func(name string) error {
				if name == test.cutpoint {
					return stop
				}
				return nil
			})

			err := RestoreSQLite(ctx, target, backupPath)
			if !errors.Is(err, stop) {
				t.Fatalf("RestoreSQLite error = %v, want cutpoint error", err)
			}
			requireSQLiteRestoreTestValues(t, target, test.targetWant)
			if test.oldExists {
				requireSQLiteRestoreTestValues(t, target+".old", "old")
			} else if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
				t.Fatalf("unexpected .old: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.Contains(entry.Name(), ".restore-") {
					t.Errorf("temporary restore artifact remains: %s", entry.Name())
				}
			}
		})
	}
}

func TestRestoreSQLiteRestoresQuarantinedSidecarOnPrePublishError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "replacement.db")
	createRestoreTestDatabase(t, target, "old")
	createRestoreTestDatabase(t, backupPath, "new")
	journal := target + "-journal"
	marker := []byte("sidecar must return")
	stop := errors.New("stop before publication")
	ctx := withSQLiteRestoreCutpoint(context.Background(), func(name string) error {
		switch name {
		case sqliteRestoreAfterCheckpoint:
			return os.WriteFile(journal, marker, 0o600)
		case sqliteRestoreAfterSidecarsHidden:
			if _, err := os.Stat(journal); !os.IsNotExist(err) {
				return errors.New("journal was not quarantined")
			}
			return stop
		default:
			return nil
		}
	})

	err := RestoreSQLite(ctx, target, backupPath)
	if !errors.Is(err, stop) {
		t.Fatalf("RestoreSQLite error = %v, want stop", err)
	}
	got, err := os.ReadFile(journal)
	if err != nil {
		t.Fatalf("read restored sidecar: %v", err)
	}
	if !bytes.Equal(got, marker) {
		t.Fatalf("restored sidecar = %q, want %q", got, marker)
	}
	requireSQLiteRestoreTestValues(t, target, "old")
}

func TestRestoreSQLitePublishesOldWithoutStaleSidecars(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "replacement.db")
	oldPath := target + ".old"
	createRestoreTestDatabase(t, target, "current-old")
	createRestoreTestDatabase(t, backupPath, "new")
	createRestoreTestDatabase(t, oldPath, "obsolete-old")
	if err := os.WriteFile(oldPath+"-wal", []byte("stale wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("inspect published old")
	ctx := withSQLiteRestoreCutpoint(context.Background(), func(name string) error {
		if name == sqliteRestoreAfterOldPublished {
			return stop
		}
		return nil
	})

	if err := RestoreSQLite(ctx, target, backupPath); !errors.Is(err, stop) {
		t.Fatalf("RestoreSQLite error = %v, want stop", err)
	}
	requireSQLiteRestoreTestValues(t, target, "current-old")
	requireSQLiteRestoreTestValues(t, oldPath, "current-old")
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(oldPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("stale .old sidecar %s survived: %v", suffix, err)
		}
	}
}

func TestPublishSQLiteBackupCollisionNeverReplacesExistingBackup(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, time.August, 12, 12, 0, 0, 123, time.Local)
	base := "collision"
	existing := filepath.Join(dir, sqliteBackupFilename(base, stamp))
	if err := os.WriteFile(existing, []byte("first-success"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged.db")
	if err := os.WriteFile(staged, []byte("second-success"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := publishSQLiteBackup(context.Background(), staged, dir, base, stamp)
	if err != nil {
		t.Fatal(err)
	}
	wantPublished := filepath.Join(dir, sqliteBackupFilename(base, stamp.Add(time.Nanosecond)))
	if published != wantPublished {
		t.Fatalf("published path = %s, want %s", published, wantPublished)
	}
	first, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "first-success" || string(second) != "second-success" {
		t.Fatalf("backup contents changed: first=%q second=%q", first, second)
	}
	if _, _, ok := splitBackupName(filepath.Base(published)); !ok {
		t.Fatalf("collision-safe name is not rotation-compatible: %s", published)
	}
}

func TestPublishSQLiteBackupConcurrentCollisionProducesDistinctBackups(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, time.August, 12, 12, 0, 0, 456, time.Local)
	const count = 8
	type result struct {
		path string
		err  error
	}
	results := make([]result, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range count {
		staged := filepath.Join(dir, "staged-"+string(rune('a'+i))+".db")
		if err := os.WriteFile(staged, []byte{byte(i)}, 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(index int, path string) {
			defer wg.Done()
			<-start
			results[index].path, results[index].err = publishSQLiteBackup(context.Background(), path, dir, "parallel", stamp)
		}(i, staged)
	}
	close(start)
	wg.Wait()

	paths := make([]string, 0, count)
	contents := make([]int, 0, count)
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("publish failed: %v", result.err)
		}
		paths = append(paths, result.path)
		content, err := os.ReadFile(result.path)
		if err != nil {
			t.Fatal(err)
		}
		if len(content) != 1 {
			t.Fatalf("content in %s = %q", result.path, content)
		}
		contents = append(contents, int(content[0]))
	}
	sort.Strings(paths)
	for i := 1; i < len(paths); i++ {
		if paths[i] == paths[i-1] {
			t.Fatalf("publishers returned duplicate path %s", paths[i])
		}
	}
	sort.Ints(contents)
	for i, content := range contents {
		if content != i {
			t.Fatalf("published contents = %v", contents)
		}
	}
}

func TestPublishSQLiteBackupSyncFailureKeepsPublishedBackup(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged.db")
	if err := os.WriteFile(staged, []byte("durable-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, time.August, 12, 12, 0, 0, 789, time.Local)
	syncFailure := errors.New("directory sync failed")
	published, err := publishSQLiteBackupWithSync(
		context.Background(), staged, dir, "sync-failure", stamp,
		func(string) error { return syncFailure },
	)
	if !errors.Is(err, syncFailure) {
		t.Fatalf("publish error = %v, want sync failure", err)
	}
	if published == "" {
		t.Fatal("published path was lost on post-publication sync failure")
	}
	contents, readErr := os.ReadFile(published)
	if readErr != nil || string(contents) != "durable-file" {
		t.Fatalf("published backup lost: contents=%q err=%v", contents, readErr)
	}
}
