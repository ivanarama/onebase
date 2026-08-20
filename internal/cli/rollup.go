package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

var rollupCmd = &cobra.Command{
	Use:   "rollup",
	Short: "Свёртка базы: свернуть остатки регистров на дату",
	Long: `Свёртка информационной базы (план 151). На выбранную дату остатки регистров
накопления сворачиваются в опорные записи (синтетический регистратор), а старые
движения удаляются. Документы до даты по умолчанию удаляются (--keep-documents
оставляет их без проведения). Выставляется дата запрета проведения.

ОПЕРАЦИЯ НЕОБРАТИМА — сделайте резервную копию (onebase backup) перед запуском.

Примеры:
  onebase rollup --project ./trade --sqlite trade.db --date 2025-01-01 --dry-run
  onebase rollup --id <baseID> --date 2025-01-01 --keep-documents`,
	RunE: runRollup,
}

func init() {
	addBaseFlags(rollupCmd)
	rollupCmd.Flags().String("date", "", "дата свёртки ГГГГ-ММ-ДД (обязательно)")
	rollupCmd.Flags().String("registers", "", "регистры накопления через запятую (по умолчанию — все балансовые)")
	rollupCmd.Flags().String("aux-account", "", "код вспомогательного счёта для свёртки бухрегистра (иначе берётся «000»)")
	rollupCmd.Flags().Bool("dry-run", false, "только показать, что будет сделано")
	rollupCmd.Flags().Bool("keep-documents", false, "не удалять документы до даты, а снять с них проведение")
	rollupCmd.Flags().Bool("trim-info-registers", false, "обрезать периодические регистры сведений (оставить последний срез до даты; нестандартно для 1С)")
	rollupCmd.Flags().Bool("yes", false, "не запрашивать подтверждение")
	rootCmd.AddCommand(rollupCmd)
}

func runRollup(cmd *cobra.Command, _ []string) error {
	dateStr, _ := cmd.Flags().GetString("date")
	if dateStr == "" {
		return fmt.Errorf("укажите --date ГГГГ-ММ-ДД")
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return fmt.Errorf("неверная дата %q (ожидается ГГГГ-ММ-ДД): %w", dateStr, err)
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	keepDocs, _ := cmd.Flags().GetBool("keep-documents")
	trimInfo, _ := cmd.Flags().GetBool("trim-info-registers")
	assumeYes, _ := cmd.Flags().GetBool("yes")
	regsArg, _ := cmd.Flags().GetString("registers")

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	ctx := context.Background()
	db, err := bc.OpenDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	defer proj.Close()

	regNames := selectedRegisterNames(proj.Registers, regsArg)
	accNames := make([]string, 0, len(proj.AccountRegisters))
	for _, ar := range proj.AccountRegisters {
		accNames = append(accNames, ar.Name)
	}
	// Обрезка регистров сведений — только по явному флагу: все периодические.
	var infoNames []string
	if trimInfo {
		for _, ir := range proj.InfoRegisters {
			if ir.Periodic {
				infoNames = append(infoNames, ir.Name)
			}
		}
	}
	if len(regNames) == 0 && len(accNames) == 0 && len(infoNames) == 0 {
		return fmt.Errorf("нет регистров для свёртки")
	}

	if aux, _ := cmd.Flags().GetString("aux-account"); strings.TrimSpace(aux) != "" {
		if err := db.SaveRollupAuxAccount(ctx, aux); err != nil {
			return fmt.Errorf("сохранение вспомогательного счёта: %w", err)
		}
	}

	opts := storage.RollupOptions{
		Date:             date,
		Registers:        regNames,
		AccountRegisters: accNames,
		InfoRegisters:    infoNames,
		DeleteDocuments:  !keepDocs,
		BeforeDeleteDocument: func(ctx context.Context, entity *metadata.Entity, id uuid.UUID) error {
			return exchange.RegisterOnDelete(ctx, db, proj.ExchangePlans, entity, id)
		},
	}

	prev, err := db.RollupPreview(ctx, proj.Registers, proj.Entities, proj.AccountRegisters, proj.InfoRegisters, opts)
	if err != nil {
		return fmt.Errorf("предпросмотр свёртки: %w", err)
	}
	outln("Предпросмотр свёртки:")
	printRollupReport(prev, keepDocs)

	if dryRun {
		outln("\n(--dry-run: изменения не внесены)")
		return nil
	}

	if !assumeYes {
		outf("\nВНИМАНИЕ: операция необратима. Сделайте резервную копию (onebase backup).\nПродолжить свёртку на %s? [y/N]: ",
			date.Format("02.01.2006"))
		// Ошибку чтения обрабатываем тем же путём, что и любой ответ, кроме
		// «y»: при закрытом stdin (запуск из скрипта без --yes) ans остаётся
		// пустым, и необратимая свёртка отменяется. Это и есть нужное
		// поведение — сомнение трактуется как отказ.
		var ans string
		if _, serr := fmt.Scanln(&ans); serr != nil {
			ans = ""
		}
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			outln("Отменено.")
			return nil
		}
	}

	rep, err := db.Rollup(ctx, proj.Registers, proj.Entities, proj.AccountRegisters, proj.InfoRegisters, opts)
	if err != nil {
		return fmt.Errorf("свёртка: %w", err)
	}
	outln("\nГотово:")
	printRollupReport(rep, keepDocs)
	printReclaimHint(db, rep.FoldedMovements())
	return nil
}

// printReclaimHint напоминает про место после массового удаления. Само место не
// возвращается: SQLite не укорачивает файл, а PostgreSQL оставляет старую
// статистику планировщика (план 114).
func printReclaimHint(db *storage.DB, deleted int) {
	if deleted <= 0 {
		return
	}
	outln("")
	if db.IsSQLite() {
		outln("Файл базы не уменьшился: SQLite не возвращает место после удаления.")
	} else {
		outln("Статистика планировщика не пересчитана после массового удаления.")
	}
	outln("Обслуживание: onebase vacuum (см. onebase vacuum --help — на SQLite база будет заблокирована).")
}

// selectedRegisterNames — имена регистров для свёртки: из --registers (через
// запятую) или все регистры накопления конфигурации.
func selectedRegisterNames(all []*metadata.Register, arg string) []string {
	if strings.TrimSpace(arg) == "" {
		names := make([]string, 0, len(all))
		for _, r := range all {
			if r.IsTurnover() {
				continue // оборотные регистры не сворачиваются
			}
			names = append(names, r.Name)
		}
		return names
	}
	var out []string
	for _, n := range strings.Split(arg, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Отчёт всегда идёт в stdout — печатаем через outf/outln, где решение об
// ошибках записи принято один раз (см. output.go).
func printRollupReport(rep storage.RollupReport, keepDocs bool) {
	outf("  Дата свёртки: %s\n", rep.Cutoff.Format("02.01.2006"))
	for _, r := range rep.Registers {
		outf("  %-32s движений: %6d  опорных остатков: %5d\n",
			r.Name, r.FoldedMovements, r.OpeningRows)
	}
	for _, r := range rep.AccountRegisters {
		if r.Note != "" {
			outf("  %-32s движений: %6d  — %s\n", r.Name, r.FoldedMovements, r.Note)
		} else {
			outf("  %-32s движений: %6d  опорных проводок: %5d\n",
				r.Name, r.FoldedMovements, r.OpeningRows)
		}
	}
	for _, r := range rep.InfoRegisters {
		if r.Note != "" {
			outf("  %-32s — %s\n", r.Name, r.Note)
		} else {
			outf("  %-32s обрезано строк: %6d  срезов оставлено: %5d\n",
				r.Name, r.FoldedMovements, r.OpeningRows)
		}
	}
	if keepDocs {
		outln("  Документы: снять проведение (не удалять)")
	} else {
		outf("  Документы к удалению: %d\n", rep.DeletedDocs)
		if rep.DanglingRefs > 0 {
			outf("  ⚠ на удаляемые документы ссылается сохраняемых записей: %d — свёртка будет отменена (используйте --keep-documents)\n", rep.DanglingRefs)
		}
	}
}
