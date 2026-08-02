package cli

import (
	"fmt"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

var (
	backupDB     string
	backupSQLite string
	backupOut    string
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a backup of the database (PostgreSQL → .sql.gz, SQLite → .db)",
	Example: "  onebase backup --db postgres://localhost/mydb --out ./backups/\n" +
		"  onebase backup --sqlite ./docflow.db --out ./backups/",
	RunE: runBackup,
}

func init() {
	backupCmd.Flags().StringVar(&backupDB, "db", "", "PostgreSQL connection string")
	backupCmd.Flags().StringVar(&backupSQLite, "sqlite", "", "path to the SQLite database file")
	backupCmd.Flags().StringVar(&backupOut, "out", ".", "output directory for the backup file")
}

// requireOneDBTarget проверяет, что задан ровно один источник БД: --db или --sqlite.
func requireOneDBTarget(db, sqlite string) error {
	switch {
	case db == "" && sqlite == "":
		return fmt.Errorf("укажите --db (PostgreSQL) или --sqlite (файл SQLite)")
	case db != "" && sqlite != "":
		return fmt.Errorf("--db и --sqlite взаимоисключающи; укажите только один")
	default:
		return nil
	}
}

func runBackup(cmd *cobra.Command, args []string) error {
	if err := requireOneDBTarget(backupDB, backupSQLite); err != nil {
		return err
	}
	outDir, err := filepath.Abs(backupOut)
	if err != nil {
		return err
	}
	outf("Создание бэкапа в %s ...\n", outDir)
	var path string
	if backupSQLite != "" {
		// SQLite бэкапится атомарным VACUUM INTO в обычный .db — восстановление
		// простым копированием файла (см. internal/backup/sqlite.go).
		path, err = backup.DumpSQLite(cmd.Context(), backupSQLite, outDir)
	} else {
		path, err = backup.Dump(cmd.Context(), backupDB, outDir)
	}
	if err != nil {
		return err
	}
	outf("Бэкап сохранён: %s\n", path)
	return nil
}

var (
	restoreDB     string
	restoreSQLite string
	restoreFile   string
	restoreForce  bool
)

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore database from a backup file",
	Example: "  onebase restore --db postgres://localhost/mydb --file ./backups/backup_mydb_2026-05-06_14-30.sql.gz\n" +
		"  onebase restore --sqlite ./docflow.db --file ./backups/backup_docflow_2026-05-06_14-30.db",
	RunE: runRestore,
}

func init() {
	restoreCmd.Flags().StringVar(&restoreDB, "db", "", "PostgreSQL connection string")
	restoreCmd.Flags().StringVar(&restoreSQLite, "sqlite", "", "path to the SQLite database file to restore into")
	restoreCmd.Flags().StringVar(&restoreFile, "file", "", "path to the backup file (required)")
	restoreCmd.Flags().BoolVar(&restoreForce, "force", false, "confirm that the target service is stopped and allow destructive SQLite restore")
	_ = restoreCmd.MarkFlagRequired("file")
}

func runRestore(cmd *cobra.Command, args []string) error {
	if err := requireOneDBTarget(restoreDB, restoreSQLite); err != nil {
		return err
	}
	outf("Восстановление из %s ...\n", restoreFile)
	if restoreSQLite != "" {
		if !restoreForce {
			return fmt.Errorf("SQLite restore требует остановленного сервиса; повторите с --force после его остановки")
		}
		// Файл БД перезаписывается целиком — сервис базы должен быть остановлен.
		if err := backup.RestoreSQLite(cmd.Context(), restoreSQLite, restoreFile); err != nil {
			return err
		}
	} else {
		if err := backup.Restore(cmd.Context(), restoreDB, restoreFile); err != nil {
			return err
		}
	}
	outln("Восстановление завершено.")
	return nil
}

var (
	demoResetDB   string
	demoResetFile string
)

var demoResetCmd = &cobra.Command{
	Use:   "demo-reset",
	Short: "Restore demo business data from a .obz backup (keeps users, roles and sessions)",
	Long: "Восстанавливает бизнес-данные из .obz, сохраняя таблицы авторизации " +
		"(_users, _sessions, _roles, _user_roles). Та же операция, что выполняет " +
		"регламентное задание DemoReset по расписанию — но запускается немедленно. " +
		"Удобно дёргать из деплой-скрипта после заливки свежего .obz.",
	Example: "  onebase demo-reset --db postgres://localhost/mydb --file ./demo.obz",
	RunE:    runDemoReset,
}

func init() {
	demoResetCmd.Flags().StringVar(&demoResetDB, "db", "", "PostgreSQL connection string (required)")
	demoResetCmd.Flags().StringVar(&demoResetFile, "file", "", "path to the .obz backup file (required)")
	_ = demoResetCmd.MarkFlagRequired("db")
	_ = demoResetCmd.MarkFlagRequired("file")
}

func runDemoReset(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	db, err := storage.Connect(ctx, demoResetDB)
	if err != nil {
		return err
	}
	defer db.Close()

	outf("Сброс демо-данных из %s ...\n", demoResetFile)
	report, err := backup.DemoReset(ctx, db, demoResetFile)
	if err != nil {
		return err
	}
	rows := 0
	for _, n := range report.Tables {
		rows += n
	}
	outf("Готово: таблиц %d, строк %d.\n", len(report.Tables), rows)
	return nil
}
