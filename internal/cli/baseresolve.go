package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
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
	// materializedDB keeps the shared lifetime lease used to export DB-backed
	// configuration. The first OpenDB reuses this exact handle so configuration
	// and data cannot cross restore generations between resolve and execution.
	materializedDB *storage.DB
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
	if bc.materializedDB != nil {
		db := bc.materializedDB
		bc.materializedDB = nil
		return db, nil
	}
	if bc.DBType == "sqlite" {
		if bc.SQLitePath == "" {
			return nil, fmt.Errorf("для SQLite укажите путь к файлу базы")
		}
		return openCLIStorage(ctx, "sqlite", bc.SQLitePath, "")
	}
	return openCLIStorage(ctx, "postgres", "", bc.DSN)
}

// openCLIStorage is the fail-closed entry point for ordinary CLI consumers.
// Recovery-capable commands (run/restore) deliberately use the raw storage
// constructors and resolve the durable intent before serving or mutating data.
func openCLIStorage(ctx context.Context, dbType, sqlitePath, dsn string) (*storage.DB, error) {
	var (
		db     *storage.DB
		lease  dblock.Lease
		err    error
		target = sqlitePath
	)
	if dbType == "sqlite" {
		lease, target, err = dblock.AcquireSQLiteSharedTarget(sqlitePath)
	} else {
		lease, err = dblock.AcquirePostgresShared(ctx, dsn)
	}
	if err != nil {
		return nil, fmt.Errorf("database lifetime lock: %w", err)
	}
	if dbType == "sqlite" {
		db, err = storage.ConnectSQLite(ctx, target)
	} else {
		db, err = storage.Connect(ctx, dsn)
	}
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	db.AddCloseHook(lease.Close)
	if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database has an interrupted restore: %w", err)
	}
	// Ревизия схемы (#1057) — после маркера восстановления и до любой работы с
	// данными: прерванное восстановление старше по важности, а всё остальное в
	// этой базе для старого бинаря уже небезопасно.
	if err := guardSchemaRevision(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
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
	var db *storage.DB
	var err error
	if bc.DBType == "sqlite" {
		db, err = openCLIStorage(ctx, "sqlite", bc.SQLitePath, "")
	} else {
		db, err = openCLIStorage(ctx, "postgres", "", bc.DSN)
	}
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
	bc.materializedDB = db
	return tmp, func() {
		// OpenDB may already have handed this handle to the command. Close is
		// idempotent, so Cleanup safely backstops both consumed and config-only use.
		db.Close()
		removeTemp(tmp)
	}, nil
}
