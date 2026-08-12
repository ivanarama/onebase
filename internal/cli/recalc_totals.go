package cli

import (
	"context"
	"fmt"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// recalcTotalsCmd — полный пересчёт предрасчитанных итогов регистров (план 80).
// Обычно итоги поддерживаются автоматически при проведении; команда нужна после
// массовой правки данных в обход движка или для первичного наполнения.
var recalcTotalsCmd = &cobra.Command{
	Use:   "recalc-totals",
	Short: "Пересчитать итоги регистров (накопления и бухгалтерии) из движений",
	RunE:  runRecalcTotals,
}

func init() {
	recalcTotalsCmd.Flags().String("project", ".", "path to project directory")
	recalcTotalsCmd.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
	recalcTotalsCmd.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	recalcTotalsCmd.Flags().String("config-source", "file", "configuration source: file or database")
	recalcTotalsCmd.Flags().String("register", "", "пересчитать только указанный регистр (по умолчанию — все с итогами)")
}

func runRecalcTotals(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	configSource, _ := cmd.Flags().GetString("config-source")
	only, _ := cmd.Flags().GetString("register")

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

	// Итоговые таблицы должны существовать (на случай первого запуска).
	if err := db.MigrateRegisters(ctx, proj.Registers); err != nil {
		return fmt.Errorf("migrate registers: %w", err)
	}
	if err := db.MigrateAccountRegisters(ctx, proj.AccountRegisters); err != nil {
		return fmt.Errorf("migrate account registers: %w", err)
	}

	var done, skipped int
	for _, reg := range proj.Registers {
		if only != "" && reg.Name != only {
			continue
		}
		if !reg.TotalsUsable() {
			skipped++
			continue
		}
		if err := db.RecalcRegisterTotals(ctx, reg); err != nil {
			return fmt.Errorf("recalc totals %s: %w", reg.Name, err)
		}
		outf("итоги пересчитаны: %s (%s)\n", reg.Name, metadata.RegisterTotalsTableName(reg.Name))
		done++
	}
	for _, ar := range proj.AccountRegisters {
		if only != "" && ar.Name != only {
			continue
		}
		if !ar.TotalsUsable() {
			skipped++
			continue
		}
		if err := db.RecalcAccountRegisterTotals(ctx, ar); err != nil {
			return fmt.Errorf("recalc account totals %s: %w", ar.Name, err)
		}
		outf("итоги пересчитаны: %s (%s)\n", ar.Name, metadata.AccountRegTotalsTableName(ar.Name))
		done++
	}
	outf("готово: пересчитано %d, пропущено без итогов %d\n", done, skipped)
	return nil
}
