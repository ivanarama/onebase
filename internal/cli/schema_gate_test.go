package cli

// Гейт ревизии схемы (issue #1057) проверяется через ту же дверь, в которую
// входит пользователь, — команду `onebase migrate`. Дёргать CheckSchemaRevision
// напрямую здесь нельзя: ровно так в #611 зелёный тест покрывал код, который в
// продовом пути не вызывался вовсе.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

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
