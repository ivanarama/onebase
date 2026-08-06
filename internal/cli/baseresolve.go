package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// baseConfig — разрешённый источник конфигурации + параметры БД из CLI-флагов.
// Используется командами procrun / check / describe.
type baseConfig struct {
	Dir        string // каталог конфигурации (для db-config — временный, см. Cleanup)
	DBType     string // "sqlite" или "" (postgres)
	SQLitePath string
	DSN        string
	// ConfigInDB — конфигурация живёт в самой базе, а Dir лишь временная
	// выгрузка. Командам, которые советуют пользователю поправить файл, знать
	// это обязательно: файла нет, а путь из Dir исчезнет вместе с Cleanup.
	ConfigInDB bool
	cleanup    func()
}

// Cleanup убирает временный каталог (для db-config). Идемпотентна — безопасно
// вызвать явно перед os.Exit и через defer.
func (bc *baseConfig) Cleanup() {
	if bc != nil && bc.cleanup != nil {
		bc.cleanup()
		bc.cleanup = nil
	}
}

// OpenDB открывает подключение к БД базы по разрешённым параметрам.
func (bc *baseConfig) OpenDB(ctx context.Context) (*storage.DB, error) {
	if bc.DBType == "sqlite" {
		if bc.SQLitePath == "" {
			return nil, fmt.Errorf("для SQLite укажите путь к файлу базы")
		}
		return storage.ConnectSQLite(ctx, bc.SQLitePath)
	}
	return storage.Connect(ctx, bc.DSN)
}

// addBaseFlags регистрирует стандартные флаги выбора базы.
func addBaseFlags(cmd *cobra.Command) {
	cmd.Flags().String("id", "", "ID базы из реестра ibases")
	cmd.Flags().String("project", ".", "путь к каталогу конфигурации")
	cmd.Flags().String("sqlite", "", "путь к файлу SQLite (альтернатива --db)")
	cmd.Flags().String("db", "", "PostgreSQL DSN (или переменная DATABASE_URL)")
}

// resolveBase превращает CLI-флаги в каталог конфигурации + параметры БД.
// Для баз с config_source=database конфигурация экспортируется во временный
// каталог (вызовите Cleanup() для удаления).
func resolveBase(cmd *cobra.Command) (*baseConfig, error) {
	bc := &baseConfig{}
	if baseID, _ := cmd.Flags().GetString("id"); baseID != "" {
		store, err := launcher.NewStore()
		if err != nil {
			return nil, fmt.Errorf("ibases store: %w", err)
		}
		base, err := store.Get(baseID)
		if err != nil {
			return nil, fmt.Errorf("база не найдена: %w", err)
		}
		bc.DBType, bc.SQLitePath, bc.DSN = base.DBType, base.DBPath, base.DB
		if base.ConfigSource == "database" {
			dir, cleanup, err := bc.materializeDBConfig(cmd.Context())
			if err != nil {
				return nil, fmt.Errorf("экспорт конфигурации из БД: %w", err)
			}
			bc.Dir, bc.cleanup, bc.ConfigInDB = dir, cleanup, true
		} else {
			bc.Dir = base.Path
		}
		return bc, nil
	}
	bc.Dir, _ = cmd.Flags().GetString("project")
	bc.DSN = dsnFromFlags(cmd)
	bc.SQLitePath, _ = cmd.Flags().GetString("sqlite")
	if bc.SQLitePath != "" {
		bc.DBType = "sqlite"
	}
	return bc, nil
}

// materializeDBConfig выгружает конфигурацию из БД во временный каталог, чтобы
// project.Load / configcheck.CheckDir могли работать с файлами.
func (bc *baseConfig) materializeDBConfig(ctx context.Context) (string, func(), error) {
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return "", nil, err
	}
	tmp, err := os.MkdirTemp("", "onebase-cli-")
	if err != nil {
		db.Close()
		return "", nil, err
	}
	if err := configdb.New(db).ExportToDir(ctx, tmp); err != nil {
		db.Close()
		removeTemp(tmp)
		return "", nil, err
	}
	return tmp, func() { db.Close(); removeTemp(tmp) }, nil
}
