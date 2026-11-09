package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
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
	migrateCmd.Flags().String("adopt-indexes", "", "JSON-манифест явного принятия legacy-индексов после резервной копии")
	migrateCmd.Flags().Bool("dry-run", false, "показать план реструктуризации и выйти, ничего не меняя")
	migrateCmd.Flags().Bool("allow-destructive", false, "разрешить удаление колонок вместе с данными")
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	adoptPath, _ := cmd.Flags().GetString("adopt-indexes")
	if dryRun {
		if adoptPath != "" {
			return fmt.Errorf("--adopt-indexes cannot be combined with --dry-run")
		}
		return runMigrationPreview(cmd)
	}
	adoptions, err := readIndexManifest(adoptPath)
	if err != nil {
		return err
	}
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	configSource, _ := cmd.Flags().GetString("config-source")

	ctx := context.Background()
	var (
		db *storage.DB
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

	// Отчёт о реструктуризации печатается по ходу: администратор должен видеть,
	// что миграция сделала с его данными, а не только «migration complete».
	allowDestructive, _ := cmd.Flags().GetBool("allow-destructive")
	var skipped int
	db.SetSchemaOptions(storage.SchemaOptions{
		AllowDestructive: allowDestructive,
		AdoptIndexes:     adoptions,
		Warn:             func(w storage.SchemaTypeWarning) { outln("  ПРЕДУПРЕЖДЕНИЕ: " + w.String()) },
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

// migrationPlanRefusal is a stable, read-only diagnostic (not an I/O failure).
// It may be printed only after the preview opener verifies its snapshot.
type migrationPlanRefusal struct{ error }

// migrationPlanText buffers output until preview verifies its stable read.
func migrationPlanText(ctx context.Context, db *storage.DB, proj *project.Project) (string, error) {
	var text strings.Builder
	if err := db.CheckEntityIndexIdentity(ctx, proj.Entities); err != nil {
		return "", err
	}
	warnings, err := db.UntrackedFieldTypeWarnings(ctx, proj.Entities, proj.Registers, proj.InfoRegisters)
	if err != nil {
		return "", err
	}
	for _, warning := range warnings {
		fmt.Fprintln(&text, "  ПРЕДУПРЕЖДЕНИЕ: "+warning.String())
	}
	if err := printLongIdentPlan(ctx, db, proj, &text); err != nil {
		return "", err
	}
	plan, err := db.PlanMigration(ctx, proj.Entities, proj.Registers, proj.InfoRegisters)
	if err != nil {
		return "", err
	}
	refs := storage.UsersReferences(proj.Entities, proj.Registers, proj.InfoRegisters, proj.AccountRegisters)
	usersPlan, err := db.PlanUsersReferenceConstraints(ctx, refs)
	if err != nil {
		return "", err
	}
	for _, e := range proj.Entities {
		if err := db.CheckEntityColumnMigration(ctx, e); err != nil {
			fmt.Fprintln(&text, "  БЛОКИРОВКА:", err)
		}
	}
	actions, manifest, err := db.PlanEntityIndexes(ctx, proj.Entities)
	if err != nil {
		return "", err
	}
	for _, a := range actions {
		fmt.Fprintf(&text, "  Индекс %s.%s: %s (%s), unique=%t, columns=%v\n", a.Index.Table, a.Index.Name, a.Action, a.Ownership, a.Index.Unique, a.Index.Columns)
		if a.Index.Definition != "" {
			fmt.Fprintln(&text, "    "+a.Index.Definition)
		}
	}
	if len(manifest.Indexes) > 0 {
		raw, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return "", err
		}
		fmt.Fprintln(&text, "Манифест кандидатов: проверьте определения, сделайте backup; примите только нужные индексы через --adopt-indexes.")
		fmt.Fprintln(&text, string(raw))
	}
	destructive := 0
	if len(plan) == 0 {
		fmt.Fprintln(&text, "Реструктуризация не требуется для полей с устойчивым id: план изменений полей пуст.")
		fmt.Fprintln(&text, "(Новые таблицы и колонки создаст обычный `onebase migrate` — в плане их нет.)")
	} else {
		fmt.Fprintf(&text, "План реструктуризации (%d изменен.):\n", len(plan))
		for _, c := range plan {
			fmt.Fprintln(&text, "  "+c.String())
			if c.Note != "" {
				fmt.Fprintln(&text, "      "+c.Note)
			}
			if c.Destructive() {
				destructive++
			}
		}
		fmt.Fprintln(&text, "")
	}
	var broken int64
	if len(usersPlan) > 0 {
		fmt.Fprintln(&text, "Ссылки на учётные записи (_users), read-only preflight:")
		for _, p := range usersPlan {
			fmt.Fprintln(&text, "  "+p.String())
			if db.IsSQLite() && (p.State == "missing-fk" || p.State == "incompatible-fk") {
				fmt.Fprintln(&text, "      Установка FK на существующую колонку потребует перестройки SQLite.")
			}
			if p.BrokenCount > 0 {
				fmt.Fprintf(&text, "      Битых ссылок: %d; идентификаторы исходных записей (до 5): %q\n", p.BrokenCount, p.RecordIDs)
				broken += p.BrokenCount
			}
		}
		fmt.Fprintln(&text, "Этот срез только проверяет ссылки; отсутствующие FK обычный migrate пока не довносит.")
	}
	fmt.Fprintln(&text, "Это пробный прогон — база не изменена.")
	if destructive > 0 {
		fmt.Fprintf(&text, "Изменений с потерей данных: %d — они выполнятся только с --allow-destructive.\n", destructive)
	}
	if broken > 0 {
		return text.String(), &migrationPlanRefusal{fmt.Errorf("preflight _users: битых ссылок: %d; исправьте данные вручную и повторите dry-run; автоматическая очистка запрещена", broken)}
	}
	return text.String(), nil
}

// applyAllMigrations приводит схему БД в соответствие метаданным проекта:
// справочники/документы, регистры, константы, план счетов и регистры
// бухгалтерии, таблицы вложений/blob'ов. Идемпотентно. Используется командой
// migrate и раннером тестов (onebase test), которому нужна готовая схема на
// свежей (в т.ч. :memory:) базе.
func applyAllMigrations(ctx context.Context, db *storage.DB, proj *project.Project) error {
	if err := db.CheckLongIdents(ctx, proj.Entities, proj.Registers, proj.InfoRegisters, proj.AccountRegisters); err != nil {
		return err
	}
	if err := db.CheckEntityIndexIdentity(ctx, proj.Entities); err != nil {
		return err
	}
	// Реквизиты reference:_users создают FK на auth-таблицу. Подготовить её
	// нужно до сущностей, в том числе на свежей базе migrate/test.
	if err := auth.NewRepo(db).EnsureSchema(ctx); err != nil {
		return fmt.Errorf("auth schema: %w", err)
	}
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

// printLongIdentPlan — инвентаризация длинных имён (#1946): какие таблицы и
// колонки миграция переименует и где откажет. Печатается, только если есть
// что показать; без длинных имён вывод dry-run прежний.
func printLongIdentPlan(ctx context.Context, db *storage.DB, proj *project.Project, text *strings.Builder) error {
	plan, err := db.PlanLongIdents(ctx, proj.Entities, proj.Registers, proj.InfoRegisters, proj.AccountRegisters)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return nil
	}
	conflicts := 0
	fmt.Fprintf(text, "Длинные имена (#1946, docs/long-identifiers.md), %d:\n", len(plan))
	for _, a := range plan {
		fmt.Fprintln(text, "  "+a.String())
		if a.Conflict != "" {
			conflicts++
		}
	}
	if conflicts > 0 {
		fmt.Fprintf(text, "Конфликтов: %d — миграция на них откажет, ничего не переименовав в этих таблицах.\n", conflicts)
	} else {
		fmt.Fprintln(text, "Переименование — ALTER … RENAME, данные не копируются. Снимите резервную копию: старая платформа переименованную базу не прочитает.")
	}
	fmt.Fprintln(text, "")
	return nil
}
