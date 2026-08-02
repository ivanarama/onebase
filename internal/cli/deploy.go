package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy a project to a server database (imports config + runs migrations)",
	Long: `Deploy loads a local project folder into PostgreSQL so the server
can run in database mode without needing the project files on disk.

Steps performed:
  1. Create the database if it does not exist
  2. Create platform schema (_onebase_config, _users, _sessions, ...)
  3. Import YAML configuration files into _onebase_config
  4. Run DDL migrations (CREATE TABLE IF NOT EXISTS for all entities)
  5. Store one configuration version for this release

After deploy, start the server with:
  onebase run --config-source database --db <DSN> --port <PORT>`,
	Example: `  onebase deploy --project ./myapp --db "postgres://user:pass@server/mydb?sslmode=disable" --message "release 1.4.0"`,
	RunE:    runDeploy,
}

func init() {
	deployCmd.Flags().String("project", ".", "path to local project directory")
	deployCmd.Flags().String("db", "", "PostgreSQL connection string (required)")
	deployCmd.Flags().String("message", "", "configuration version message for this release")
	_ = deployCmd.MarkFlagRequired("db")
}

func runDeploy(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	messageFlag, _ := cmd.Flags().GetString("message")
	dsn := dsnFromFlags(cmd)
	ctx := context.Background()

	appCfg, err := project.LoadConfig(dir)
	if err != nil {
		return fmt.Errorf("load app config: %w", err)
	}

	outln("→ Проверка / создание базы данных...")
	if err := storage.EnsureDatabase(ctx, dsn); err != nil {
		return fmt.Errorf("создание БД: %w", err)
	}

	outln("→ Подключение к PostgreSQL...")
	db, err := storage.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("подключение к БД: %w", err)
	}
	defer db.Close()

	outln("→ Инициализация схемы платформы...")
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("auth schema: %w", err)
	}
	if err := db.EnsureAuditSchema(ctx); err != nil {
		return fmt.Errorf("audit schema: %w", err)
	}
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		return fmt.Errorf("exchange schema: %w", err)
	}
	if err := db.EnsureIntakeSchema(ctx); err != nil {
		return fmt.Errorf("intake schema: %w", err)
	}
	if err := db.EnsureScheduledRunsTable(ctx); err != nil {
		return fmt.Errorf("scheduled runs: %w", err)
	}
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		return fmt.Errorf("attachments: %w", err)
	}
	if err := db.EnsureBlobTable(ctx); err != nil {
		return fmt.Errorf("blobs: %w", err)
	}
	if err := db.EnsureAccountsTable(ctx); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}

	cfgRepo := configdb.New(db)
	if err := cfgRepo.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("configdb schema: %w", err)
	}

	outf("→ Загрузка конфигурации из %s...\n", dir)
	if err := cfgRepo.ImportFromDir(ctx, dir); err != nil {
		return fmt.Errorf("импорт конфигурации: %w", err)
	}

	outln("→ Загрузка метаданных...")
	if err := cfgRepo.MigrateContent(ctx); err != nil {
		return fmt.Errorf("configdb migrate content: %w", err)
	}
	proj, err := project.LoadFromDB(ctx, cfgRepo)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	outln("→ Применение DDL-миграций...")
	if err := db.Migrate(ctx, proj.Entities); err != nil {
		return fmt.Errorf("migrate entities: %w", err)
	}
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		return fmt.Errorf("migrate registers: %w", err)
	}
	if err := db.MigrateInfoRegisters(ctx, proj.InfoRegisters); err != nil {
		return fmt.Errorf("migrate inforegs: %w", err)
	}
	if err := db.MigrateConstants(ctx, proj.Constants); err != nil {
		return fmt.Errorf("migrate constants: %w", err)
	}
	if err := db.SyncAccounts(ctx, proj.ChartsOfAccounts); err != nil {
		return fmt.Errorf("sync accounts: %w", err)
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		return fmt.Errorf("migrate account registers: %w", err)
	}
	if err := db.SyncAllPredefined(ctx, proj.Entities); err != nil {
		return fmt.Errorf("sync predefined: %w", err)
	}

	versionMessage := deployVersionMessage(dir, messageFlag, appCfg)
	version, err := cfgRepo.CreateVersion(ctx, configdb.VersionOptions{Message: versionMessage})
	if err != nil {
		return fmt.Errorf("config version: %w", err)
	}

	appName := "myapp"
	if appCfg != nil && appCfg.Name != "" {
		appName = appCfg.Name
	}

	outln("")
	outln("✓ Деплой завершён успешно!")
	outln("")
	outf("  Приложение: %s\n", appName)
	outf("  База данных: %s\n", dsn)
	outf("  Версия конфигурации: %s (%s)\n", version.ID, version.Message)
	outln("")
	outln("Запустите сервер командой:")
	outf("  onebase run --config-source database --db \"%s\" --port 8080\n", dsn)
	return nil
}

func deployVersionMessage(dir, explicit string, cfg *project.AppConfig) string {
	if msg := strings.TrimSpace(explicit); msg != "" {
		return msg
	}
	if cfg != nil {
		name := strings.TrimSpace(cfg.Name)
		version := strings.TrimSpace(cfg.Version)
		switch {
		case name != "" && version != "":
			return "release " + name + " " + version
		case version != "":
			return "release " + version
		case name != "":
			return "release " + name
		}
	}
	base := filepath.Base(filepath.Clean(dir))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "release"
	}
	return "release " + base
}
