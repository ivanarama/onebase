package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dbcheck"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// Команда `onebase doctor` (план 114) — аналог «Тестирования и исправления
// информационной базы» в 1С.
//
// Диагностика читает. Чинит только то, что администратор назвал в --fix явно:
// каждая починка меняет данные, и запускать их «за компанию» с проверкой нельзя.

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Проверить состояние базы и, по разрешению, исправить найденное",
	Long: `Проверяет информационную базу по нескольким осям и печатает отчёт.

По умолчанию НИЧЕГО не меняет. Чтобы исправить найденное, перечислите проверки
в --fix (или --fix all).

Проверки:
  integrity          физическая целостность (PRAGMA integrity_check на SQLite)
  schema             расхождение схемы таблиц с метаданными
  refs               ссылки на несуществующие объекты          [чинит: очистить]
  orphan-movements   движения с удалённым регистратором        [чинит: удалить]
  totals             итоги регистров накопления против движений [чинит: пересчёт]
  account-totals     итоги регистра бухгалтерии против проводок [чинит: пересчёт]
  blobs              бинарники, на которые никто не ссылается  [чинит: удалить]

Примеры:
  onebase doctor --project . --sqlite base.db
  onebase doctor --check refs,totals --sqlite base.db
  onebase doctor --fix refs --sqlite base.db
  onebase doctor --json --sqlite base.db

Код возврата: 0 — чисто или только предупреждения, 1 — найдены ошибки.`,
	RunE:          runDoctor,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	doctorCmd.Flags().String("project", ".", "путь к каталогу конфигурации")
	doctorCmd.Flags().String("db", "", "PostgreSQL DSN (или переменная DATABASE_URL)")
	doctorCmd.Flags().String("sqlite", "", "путь к файлу SQLite (альтернатива --db)")
	doctorCmd.Flags().String("config-source", "file", "источник конфигурации: file или database")
	doctorCmd.Flags().StringSlice("check", nil, "какие проверки выполнить (по умолчанию все)")
	doctorCmd.Flags().StringSlice("fix", nil, "какие проверки должны исправить найденное (all — все умеющие)")
	doctorCmd.Flags().StringSlice("forget-document", nil,
		"удалить движения документа, которого больше нет в конфигурации (укажите имя точно как в отчёте)")
	doctorCmd.Flags().Bool("dry-run", false, "показать объём удаления --forget-document, ничего не меняя")
	doctorCmd.Flags().Bool("json", false, "машинный отчёт")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	dir, _ := cmd.Flags().GetString("project")
	sqlitePath, _ := cmd.Flags().GetString("sqlite")
	configSource, _ := cmd.Flags().GetString("config-source")
	checkNames, _ := cmd.Flags().GetStringSlice("check")
	fixNames, _ := cmd.Flags().GetStringSlice("fix")
	asJSON, _ := cmd.Flags().GetBool("json")

	checks, unknown := dbcheck.Select(checkNames)
	if len(unknown) > 0 {
		return fmt.Errorf("неизвестные проверки: %s (см. onebase doctor --help)", strings.Join(unknown, ", "))
	}
	fix, unknownFix := parseFixList(fixNames)
	if len(unknownFix) > 0 {
		return fmt.Errorf("неизвестные проверки в --fix: %s", strings.Join(unknownFix, ", "))
	}

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

	// Движения документа, которого нет в конфигурации, платформа сама не
	// удаляет: то же расхождение даёт обычное переименование. Удалить их можно
	// только назвав документ поимённо — это и есть «я знаю, что он убран
	// навсегда».
	if forget, _ := cmd.Flags().GetStringSlice("forget-document"); len(forget) > 0 {
		// Проверка «неизвестности» типа: удаление необратимо, а сразу за ним идёт
		// пересчёт итогов — потеря не оставляет следа ни в отчёте, ни в проверках.
		// Опечатка в имени живого документа стёрла бы его историю по всем
		// регистрам, поэтому сверяем каждый тип со списком документов конфигурации
		// (proj под рукой) и отказываем, если документ ещё существует (#610).
		if inConfig := forgetTypesInConfig(proj, forget); len(inConfig) > 0 {
			return fmt.Errorf(
				"тип %s есть в конфигурации — это не осиротевшие движения; "+
					"--forget-document принимает только типы из отчёта проверки orphan-movements",
				strings.Join(inConfig, ", "))
		}
		if dryRun, _ := cmd.Flags().GetBool("dry-run"); dryRun {
			n := db.CountMovementsOfRecorderType(ctx, proj.Registers, forget)
			outf("Сухой прогон: к удалению движений документов %s: %d (ничего не изменено)\n",
				strings.Join(forget, ", "), n)
			outln("")
		} else {
			n := db.DeleteMovementsOfUnknownRecorderType(ctx, proj.Registers, forget)
			outf("Удалено движений документов %s: %d\n", strings.Join(forget, ", "), n)
			for _, reg := range proj.Registers {
				if !reg.TotalsUsable() {
					continue
				}
				if err := db.RecalcRegisterTotals(ctx, reg); err != nil {
					return fmt.Errorf("пересчёт итогов %s: %w", reg.Name, err)
				}
			}
			outln("Итоги регистров пересчитаны.")
			outln("")
		}
	}

	report := dbcheck.Run(ctx, dbcheck.FromProject(db, proj), checks, fix)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printDoctorReport(report, len(fix) > 0)
	}
	if report.Worst() == dbcheck.SeverityError {
		// Ошибка состояния базы — не ошибка команды: сообщение уже напечатано,
		// нужен только ненулевой код возврата для скриптов и CI.
		return errSilentExit
	}
	return nil
}

// forgetTypesInConfig возвращает те переданные --forget-document типы, которые
// всё ещё есть в конфигурации как документы. Движения осиротевшими становятся
// только когда сам документ убран из конфигурации; удаление истории живого
// документа — почти наверняка опечатка. Сравнение регистронезависимое
// (strings.EqualFold корректно работает с кириллицей, в отличие от SQLite LOWER):
// имя, отличающееся лишь регистром, точного удаления бы не задело, но отказ с
// внятной ошибкой лучше молчаливого «удалено 0».
func forgetTypesInConfig(proj *project.Project, forget []string) []string {
	var bad []string
	for _, t := range forget {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		for _, e := range proj.Entities {
			if e.Kind == metadata.KindDocument && strings.EqualFold(e.Name, t) {
				bad = append(bad, t)
				break
			}
		}
	}
	return bad
}

// parseFixList разбирает --fix. «all» означает все проверки, которые вообще
// умеют чинить, — перечислять их руками неудобно и легко забыть новую.
func parseFixList(names []string) (map[string]bool, []string) {
	fix := map[string]bool{}
	var unknown []string
	for _, raw := range names {
		n := strings.ToLower(strings.TrimSpace(raw))
		if n == "" {
			continue
		}
		if n == "all" {
			for _, c := range dbcheck.All() {
				if c.CanFix() {
					fix[c.Name()] = true
				}
			}
			continue
		}
		known := false
		for _, c := range dbcheck.All() {
			if c.Name() == n {
				known = true
				fix[n] = true
				break
			}
		}
		if !known {
			unknown = append(unknown, n)
		}
	}
	return fix, unknown
}

func printDoctorReport(rep dbcheck.Report, fixing bool) {
	marks := map[dbcheck.Severity]string{
		dbcheck.SeverityOK:    "  ok  ",
		dbcheck.SeverityWarn:  " ВНИМ ",
		dbcheck.SeverityError: "ОШИБКА",
	}
	problems := 0
	for _, r := range rep.Results {
		outf("[%s] %s — %s\n", marks[r.Severity], r.Title, r.Summary)
		if r.Error != "" {
			outln("         " + r.Error)
		}
		for _, f := range r.Findings {
			line := "         " + f.Object + ": " + f.Detail
			if f.Count > 0 {
				line += fmt.Sprintf(" (%d)", f.Count)
			}
			outln(line)
			if len(f.Examples) > 0 {
				outln("             например: " + strings.Join(f.Examples, ", "))
			}
		}
		if r.Severity != dbcheck.SeverityOK {
			problems++
			if r.FixHint != "" && r.Fixed == 0 {
				outln("         → " + r.FixHint)
			}
		}
		if r.Fixed > 0 {
			outf("         исправлено: %d\n", r.Fixed)
		}
	}
	outln("")
	switch {
	case problems == 0:
		outln("Проблем не найдено.")
	case fixing:
		outf("Проверок с проблемами: %d. Что не исправлено — см. подсказки выше.\n", problems)
	default:
		outf("Проверок с проблемами: %d. Ничего не изменено — это диагностика.\n", problems)
		outln("Исправить: onebase doctor --fix <проверка>[,...] (или --fix all).")
		outln("Перед исправлением снимите резервную копию: onebase backup.")
	}
}
