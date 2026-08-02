package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply database schema from project metadata",
	RunE:  runMigrate,
}

func init() {
	migrateCmd.Flags().String("project", ".", "path to project directory")
	migrateCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	migrateCmd.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	migrateCmd.Flags().String("config-source", "file", "configuration source: file or database")
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	configSource, _ := cmd.Flags().GetString("config-source")

	ctx := context.Background()
	var (
		db  *storage.DB
		err error
	)
	if sqlitePath != "" {
		db, err = storage.ConnectSQLite(ctx, sqlitePath)
	} else {
		db, err = storage.Connect(ctx, dsnFromFlags(cmd))
	}
	if err != nil {
		return err
	}
	defer db.Close()

	var proj *project.Project
	if configSource == "database" {
		cfgRepo := configdb.New(db)
		if err := cfgRepo.EnsureSchema(ctx); err != nil {
			return fmt.Errorf("configdb schema: %w", err)
		}
		proj, err = project.LoadFromDB(ctx, cfgRepo)
	} else {
		proj, err = project.Load(dir)
	}
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	if err := applyAllMigrations(ctx, db, proj); err != nil {
		return err
	}
	outln("migration complete")
	return nil
}

// applyAllMigrations приводит схему БД в соответствие метаданным проекта:
// справочники/документы, регистры, константы, план счетов и регистры
// бухгалтерии, таблицы вложений/blob'ов. Идемпотентно. Используется командой
// migrate и раннером тестов (onebase test), которому нужна готовая схема на
// свежей (в т.ч. :memory:) базе.
func applyAllMigrations(ctx context.Context, db *storage.DB, proj *project.Project) error {
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		return err
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		return err
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		return err
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		return err
	}
	// План счетов и регистры бухгалтерии: таблица _accounts + синк счетов из YAML
	// и таблицы акк_<имя>. Без этого проводки и запросы остатков падают на
	// «no such table» (как run.go).
	if err := db.EnsureAccountsTable(ctx); err != nil {
		return err
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		return err
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		return err
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		return err
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		return err
	}
	return nil
}

func dsnFromFlags(cmd *cobra.Command) string {
	if dsn, _ := cmd.Flags().GetString("db"); dsn != "" {
		return dsn
	}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://localhost/onebase"
}
