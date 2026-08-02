package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Проверить конфигурацию (синтаксис .os, YAML-схема, запросы)",
	Long: `Валидирует конфигурацию без запуска: синтаксис модулей .os, вызовы
неизвестных функций, схему YAML всех объектов и компиляцию запросов
виджетов/отчётов. Выводит проблемы в формате file:line:col: message.
Завершается с ненулевым кодом, если найдены ошибки — пригодно для pre-commit/CI.
Флаг --lint добавляет рекомендательные предупреждения: неизвестные YAML-ключи,
неиспользуемые переменные DSL, недостижимые процедуры и объекты без прав в ролях.

Примеры:
  onebase check --project C:\Projects\OneBaseConfs\PuT
  onebase check --project ./examples/trade --lint
  onebase check --id <baseID> --json`,
	RunE:          runCheck,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Показать рекомендательные предупреждения конфигурации",
	Long: `Запускает полный onebase check и добавляет lint-предупреждения поверх
него: неизвестные YAML-ключи, неиспользуемые переменные DSL, недостижимые
процедуры и объекты без прав в ролях. Предупреждения не меняют ok/exit code;
ненулевой код возвращается только при ошибках обычного check.`,
	RunE:          runLint,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(checkCmd)
	checkCmd.Flags().Bool("json", false, "вывод в JSON")
	checkCmd.Flags().Bool("lint", false, "добавить рекомендательные lint-предупреждения")
	rootCmd.AddCommand(checkCmd)

	addBaseFlags(lintCmd)
	lintCmd.Flags().Bool("json", false, "вывод в JSON")
	rootCmd.AddCommand(lintCmd)
}

func runCheck(cmd *cobra.Command, _ []string) error {
	lint, _ := cmd.Flags().GetBool("lint")
	return runCheckWithOptions(cmd, configcheck.Options{Lint: lint})
}

func runLint(cmd *cobra.Command, _ []string) error {
	return runCheckWithOptions(cmd, configcheck.Options{Lint: true})
}

func runCheckWithOptions(cmd *cobra.Command, opts configcheck.Options) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	res := configcheck.RunFullWithOptions(bc.Dir, opts)

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		// В отличие от человекочитаемого вывода, этот JSON разбирает CI:
		// недописанный отчёт там превратится в ошибку разбора на чужой
		// стороне. Лучше упасть здесь, с понятной причиной.
		if err := enc.Encode(res); err != nil {
			return err
		}
	} else {
		printIssuesText(res)
	}

	if !res.OK {
		bc.Cleanup() // defer не выполнится при os.Exit
		os.Exit(1)
	}
	return nil
}

func printIssuesText(res configcheck.Result) {
	printIssueList := func(list []configcheck.Issue) {
		for _, is := range list {
			loc := is.File
			if loc == "" {
				loc = "(конфигурация)"
			}
			if is.Line > 0 {
				loc = fmt.Sprintf("%s:%d:%d", loc, is.Line, is.Column)
			}
			prefix := ""
			if is.Kind != "" {
				prefix = "[" + is.Kind + "] "
			}
			if is.Code != "" {
				prefix += "[" + is.Code + "] "
			}
			outf("%s: %s%s\n", loc, prefix, is.Message)
			if is.SuggestedFix != "" {
				outf("  подсказка: %s\n", is.SuggestedFix)
			}
		}
	}

	if len(res.Warnings) > 0 {
		outf("Предупреждения:\n")
		printIssueList(res.Warnings)
	}

	if res.OK {
		if len(res.Warnings) > 0 {
			outf("OK: ошибок не найдено (%d предупреждений)\n", len(res.Warnings))
		} else {
			outln("OK: ошибок не найдено")
		}
		return
	}
	printIssueList(res.Issues)
	fmt.Fprintf(os.Stderr, "\nНайдено ошибок: %d\n", res.Total)
}
