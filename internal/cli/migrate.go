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
	Long: `Приводит схему базы в соответствие метаданным конфигурации.

Реквизиты с устойчивым id (план 81) реструктурируются, а не только
добавляются: переименование реквизита переименовывает колонку вместе с
данными, смена типа преобразует значения, а удалённый реквизит оставляет
колонку осиротевшей — удалить её можно только явным --allow-destructive.

  --dry-run             показать план реструктуризации и ничего не делать
  --allow-destructive   разрешить удаление колонок вместе с данными`,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().String("project", ".", "path to project directory")
	migrateCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	migrateCmd.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	migrateCmd.Flags().String("config-source", "file", "configuration source: file or database")
	migrateCmd.Flags().Bool("dry-run", false, "показать план реструктуризации и выйти, ничего не меняя")
	migrateCmd.Flags().Bool("allow-destructive", false, "разрешить удаление колонок вместе с данными")
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
		db, err = openCLIStorage(ctx, "sqlite", sqlitePath, "")
	} else {
		db, err = openCLIStorage(ctx, "postgres", "", dsnFromFlags(cmd))
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

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		return printMigrationPlan(ctx, db, proj)
	}

	// Отчёт о реструктуризации печатается по ходу: администратор должен видеть,
	// что миграция сделала с его данными, а не только «migration complete».
	allowDestructive, _ := cmd.Flags().GetBool("allow-destructive")
	var skipped int
	db.SetSchemaOptions(storage.SchemaOptions{
		AllowDestructive: allowDestructive,
		Report: func(c storage.SchemaChange, applied bool) {
			if applied {
				outln("  " + c.String())
				return
			}
			skipped++
			outf("  ПРОПУЩЕНО: %s\n", c.String())
		},
	})

	if err := applyAllMigrations(ctx, db, proj); err != nil {
		return err
	}
	if skipped > 0 {
		outf("\nНе применено изменений: %d — они удаляют данные.\n", skipped)
		outln("Колонки остались в базе нетронутыми. Чтобы удалить их вместе с данными,")
		outln("повторите с флагом --allow-destructive (сначала снимите резервную копию).")
	}
	outln("migration complete")
	return nil
}

// printMigrationPlan показывает, что сделает реструктуризация, ничего не меняя.
func printMigrationPlan(ctx context.Context, db *storage.DB, proj *project.Project) error {
	plan, err := db.PlanMigration(ctx, proj.Entities, proj.Registers, proj.InfoRegisters)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		outln("Реструктуризация не требуется: схема существующих таблиц соответствует метаданным.")
		outln("(Новые таблицы и колонки создаст обычный `onebase migrate` — в плане их нет.)")
		return nil
	}
	outf("План реструктуризации (%d изменен.):\n", len(plan))
	destructive := 0
	for _, c := range plan {
		outln("  " + c.String())
		if c.Note != "" {
			outln("      " + c.Note)
		}
		if c.Destructive() {
			destructive++
		}
	}
	outln("")
	outln("Это пробный прогон — база не изменена.")
	if destructive > 0 {
		outf("Изменений с потерей данных: %d — они выполнятся только с --allow-destructive.\n", destructive)
	}
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
	if err := db.EnsurePublicFilesSchema(ctx); err != nil {
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
