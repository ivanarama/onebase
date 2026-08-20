package cli

// Гейт ревизии схемы (issue #1057) проверяется через ту же дверь, в которую
// входит пользователь, — команду `onebase migrate`. Дёргать CheckSchemaRevision
// напрямую здесь нельзя: ровно так в #611 зелёный тест покрывал код, который в
// продовом пути не вызывался вовсе.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/storage"
)

// migrateGateFixture — минимальная конфигурация: одного справочника достаточно,
// чтобы миграция была настоящей.
func migrateGateFixture(t *testing.T, dir string) {
	t.Helper()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: gate-test\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Товар.yaml",
		"name: Товар\nfields:\n  - name: Наименование\n    type: string\n")
}

// openGateDB открывает файл базы напрямую, минуя гейт: тесту надо и подсмотреть
// ревизию, и подделать её.
func openGateDB(t *testing.T, dbPath string) *storage.DB {
	t.Helper()
	db, err := storage.ConnectSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// forgeSchemaRevision выдаёт базу за обслуженную платформой другой версии.
func forgeSchemaRevision(t *testing.T, dbPath string, revision int) {
	t.Helper()
	db := openGateDB(t, dbPath)
	ctx := context.Background()
	if err := db.EnsureSchemaRevisionSchema(ctx); err != nil {
		t.Fatalf("таблица ревизии: %v", err)
	}
	q := `INSERT INTO _schema_revision (id, revision, updated_at, updated_by)
		VALUES (1, ?, (datetime('now')), ?)
		ON CONFLICT (id) DO UPDATE SET revision = excluded.revision, updated_by = excluded.updated_by`
	if _, err := db.Exec(ctx, q, revision, "onebase v9.9.9 (linux/amd64)"); err != nil {
		t.Fatalf("подделка ревизии: %v", err)
	}
}

// TestMigrateStampsSchemaRevision — обычная миграция проставляет базе ревизию
// этого бинаря. Без этого шага гейт защищает пустоту: отказывать будет нечему.
func TestMigrateStampsSchemaRevision(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "stamp.db")
	migrateGateFixture(t, dir)

	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("runMigrate: %v", err)
	}

	rev, known, by, err := openGateDB(t, dbPath).SchemaRevisionOf(context.Background())
	if err != nil {
		t.Fatalf("SchemaRevisionOf: %v", err)
	}
	if !known || rev != storage.SchemaRevision {
		t.Fatalf("после migrate ревизия (%d, %v), ожидалась (%d, true)", rev, known, storage.SchemaRevision)
	}
	if by == "" {
		t.Error("не записано, чем база обслужена")
	}
}

// TestMigrateRefusesNewerSchema — база из будущего останавливает команду до
// первой правки схемы, и текст отказа сам объясняет выход.
func TestMigrateRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "newer.db")
	migrateGateFixture(t, dir)

	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("первичная миграция: %v", err)
	}
	future := storage.SchemaRevision + 5
	forgeSchemaRevision(t, dbPath, future)

	_, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) })
	if err == nil {
		t.Fatal("команда отработала на базе, обслуженной платформой новее")
	}
	if !errors.Is(err, storage.ErrNewerSchema) {
		t.Fatalf("отказ не опознаётся как ErrNewerSchema: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{fmt.Sprint(future), "--allow-newer-schema", "v9.9.9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("в тексте отказа нет %q:\n%s", want, msg)
		}
	}
}

// TestAllowNewerSchemaOpensBaseAndKeepsRevision — осознанный обход открывает
// базу, но не понижает её ревизию: иначе один запуск старым бинарём снимал бы
// защиту со всех последующих.
func TestAllowNewerSchemaOpensBaseAndKeepsRevision(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "allowed.db")
	migrateGateFixture(t, dir)

	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("первичная миграция: %v", err)
	}
	future := storage.SchemaRevision + 5
	forgeSchemaRevision(t, dbPath, future)

	// Флаг снимается ровно так, как его задаёт пользователь: через постоянный
	// флаг корневой команды. Проверять вместо этого свою переменную значило бы
	// проверять тест, а не проводку флага.
	t.Setenv(storage.AllowNewerSchemaEnv, "")
	if err := rootCmd.PersistentFlags().Set("allow-newer-schema", "true"); err != nil {
		t.Fatalf("флаг --allow-newer-schema не зарегистрирован: %v", err)
	}
	t.Cleanup(func() {
		allowNewerSchema = false
		if err := rootCmd.PersistentFlags().Set("allow-newer-schema", "false"); err != nil {
			t.Fatalf("сброс флага: %v", err)
		}
	})

	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("с --allow-newer-schema база должна открываться: %v", err)
	}
	rev, _, _, err := openGateDB(t, dbPath).SchemaRevisionOf(context.Background())
	if err != nil {
		t.Fatalf("SchemaRevisionOf: %v", err)
	}
	if rev != future {
		t.Fatalf("обход понизил ревизию до %d, база была на %d", rev, future)
	}
}

// TestAllowNewerSchemaByEnv — у лаунчера и GUI флагов нет, поэтому обход обязан
// работать и переменной окружения, названной в тексте отказа.
func TestAllowNewerSchemaByEnv(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "env.db")
	migrateGateFixture(t, dir)

	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("первичная миграция: %v", err)
	}
	forgeSchemaRevision(t, dbPath, storage.SchemaRevision+5)

	t.Setenv(storage.AllowNewerSchemaEnv, "1")
	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("с %s=1 база должна открываться: %v", storage.AllowNewerSchemaEnv, err)
	}
}

func TestAllowNewerSchemaEnvFailsClosed(t *testing.T) {
	allowNewerSchema = false
	t.Cleanup(func() { allowNewerSchema = false })
	for _, value := range []string{"", "0", "false", " FALSE ", " ", "typo"} {
		t.Run(fmt.Sprintf("value_%q", value), func(t *testing.T) {
			t.Setenv(storage.AllowNewerSchemaEnv, value)
			if newerSchemaAllowed() {
				t.Fatalf("%s=%q unexpectedly enabled the safety bypass", storage.AllowNewerSchemaEnv, value)
			}
		})
	}
	for _, value := range []string{"1", "true", " TRUE "} {
		t.Run(fmt.Sprintf("truthy_%q", value), func(t *testing.T) {
			t.Setenv(storage.AllowNewerSchemaEnv, value)
			if !newerSchemaAllowed() {
				t.Fatalf("%s=%q did not enable the explicit bypass", storage.AllowNewerSchemaEnv, value)
			}
		})
	}
}

func TestSchemaGateRefusesBeforeSQLiteJournalMutation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "future-delete.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSchemaRevisionSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _schema_revision (id, revision, updated_at, updated_by)
		VALUES (1, ?, datetime('now'), ?)
		ON CONFLICT (id) DO UPDATE SET revision=excluded.revision, updated_by=excluded.updated_by`,
		storage.SchemaRevision+5, "future"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	raw, err := sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(storage.AllowNewerSchemaEnv, "")
	allowNewerSchema = false

	opened, err := openCLIStorage(ctx, "sqlite", dbPath, "")
	if opened != nil {
		opened.Close()
		t.Fatal("future database unexpectedly opened")
	}
	if !errors.Is(err, storage.ErrNewerSchema) {
		t.Fatalf("open error = %v, want ErrNewerSchema", err)
	}
	raw, err = sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer closeSchemaGateTestResource(t, raw)
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("late schema gate changed journal mode to %q, want delete", mode)
	}
}

func TestReadOnlySchemaGateRejectsIncompleteMarkerWithoutSQLiteSetup(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "incomplete-read-only.db")
	prepareIncompleteSQLiteRevision(t, dbPath)
	t.Setenv(storage.AllowNewerSchemaEnv, "")
	allowNewerSchema = false

	opened, err := openCLIStorageReadOnly(ctx, "sqlite", dbPath, "")
	if opened != nil {
		opened.Close()
		t.Fatal("read-only gate unexpectedly opened an incomplete marker")
	}
	if !errors.Is(err, storage.ErrSchemaRevisionIncomplete) {
		t.Fatalf("read-only gate error = %v, want ErrSchemaRevisionIncomplete", err)
	}
	assertIncompleteSQLiteRevisionUnchanged(t, dbPath)
}

func TestMutatingSchemaGateRepairsIncompleteMarkerOnlyAfterExclusiveDrain(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "incomplete-upgrade.db")
	prepareIncompleteSQLiteRevision(t, dbPath)
	t.Setenv(storage.AllowNewerSchemaEnv, "")
	allowNewerSchema = false

	existing, _, err := dblock.AcquireSQLiteSharedTarget(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openCLIStorage(ctx, "sqlite", dbPath, "")
	if opened != nil {
		opened.Close()
	}
	if !errors.Is(err, dblock.ErrLocked) {
		_ = existing.Close()
		t.Fatalf("incomplete repair with an active consumer error = %v, want ErrLocked", err)
	}
	assertIncompleteSQLiteRevisionUnchanged(t, dbPath)
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}

	opened, err = openCLIStorage(ctx, "sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("repair after consumer drain: %v", err)
	}
	defer opened.Close()
	state, err := opened.SchemaRevisionStateOf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Known || state.Revision != storage.SchemaRevision {
		t.Fatalf("repaired state = %+v, want known revision %d", state, storage.SchemaRevision)
	}
}

func prepareIncompleteSQLiteRevision(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSchemaRevisionSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	raw, err := sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertIncompleteSQLiteRevisionUnchanged(t *testing.T, dbPath string) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer closeSchemaGateTestResource(t, raw)
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("failed gate changed journal mode to %q, want delete", mode)
	}
	var rows int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM _schema_revision WHERE id=1`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("failed gate repaired incomplete marker: rows=%d", rows)
	}
}

func TestRecoveryGateRefusesFuturePendingBeforeSQLiteSetup(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "future-pending.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.RaiseSchemaRevision(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE _schema_revision SET revision=?, updated_by=? WHERE id=1`,
		storage.SchemaRevision+5, "future"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _settings(key,value) VALUES (?,?)`,
		"onebase.internal.restore.intent.v1", `{}`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	raw, err := sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	lease, target, err := dblock.AcquireSQLiteTarget(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close() //nolint:errcheck // test cleanup
	t.Setenv(storage.AllowNewerSchemaEnv, "")
	allowNewerSchema = false

	opened, err := openExclusiveRecoveryStorage(ctx, "sqlite", target, "", "")
	if opened != nil {
		opened.Close()
		t.Fatal("future pending database unexpectedly opened")
	}
	if !errors.Is(err, storage.ErrNewerSchema) {
		t.Fatalf("recovery open error = %v, want ErrNewerSchema", err)
	}
	raw, err = sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer closeSchemaGateTestResource(t, raw)
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("recovery gate changed journal mode to %q, want delete", mode)
	}
	var pending int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM _settings WHERE key=?`,
		"onebase.internal.restore.intent.v1").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("recovery gate consumed future restore intent: count=%d", pending)
	}
}

func TestSchemaRevisionUpgradeRequiresExclusiveLifetimeLease(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fenced.db")
	existing, _, err := dblock.AcquireSQLiteSharedTarget(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	db, err := openCLIStorage(ctx, "sqlite", dbPath, "")
	if db != nil {
		db.Close()
	}
	if !errors.Is(err, dblock.ErrLocked) {
		_ = existing.Close()
		t.Fatalf("upgrade with an active old consumer error = %v, want ErrLocked", err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openCLIStorage(ctx, "sqlite", dbPath, "")
	if err != nil {
		t.Fatalf("upgrade after old consumer stopped: %v", err)
	}
	db.Close()
}

func TestStampRejectsRevisionThatAdvancedConcurrently(t *testing.T) {
	ctx := context.Background()
	db := openGateDB(t, filepath.Join(t.TempDir(), "stamp-race.db"))
	if _, err := db.RaiseSchemaRevision(ctx); err != nil {
		t.Fatal(err)
	}
	future := storage.SchemaRevision + 5
	q := `UPDATE _schema_revision SET revision=?, updated_by=? WHERE id=1`
	if _, err := db.Exec(ctx, q, future, "future"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(storage.AllowNewerSchemaEnv, "")
	allowNewerSchema = false
	if err := stampSchemaRevision(ctx, db); !errors.Is(err, storage.ErrNewerSchema) {
		t.Fatalf("stamp after concurrent future revision error = %v, want ErrNewerSchema", err)
	}
}

func TestPersistentAllowFlagPublishesOverrideForChildren(t *testing.T) {
	t.Setenv(storage.AllowNewerSchemaEnv, "0")
	allowNewerSchema = true
	t.Cleanup(func() { allowNewerSchema = false })
	if err := rootCmd.PersistentPreRunE(rootCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !storage.AllowNewerSchemaByEnv() {
		t.Fatal("persistent flag was not published for launcher/reload child processes")
	}
}

func closeSchemaGateTestResource(t *testing.T, closer interface{ Close() error }) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Errorf("close schema-gate test resource: %v", err)
	}
}
