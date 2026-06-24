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

Примеры:
  onebase check --project C:\Projects\OneBaseConfs\PuT
  onebase check --id <baseID> --json`,
	RunE:          runCheck,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(checkCmd)
	checkCmd.Flags().Bool("json", false, "вывод в JSON")
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, _ []string) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	res := configcheck.RunFull(bc.Dir)

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(res)
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
			if is.Code != "" {
				prefix += "[" + is.Code + "] "
			}
			if is.Kind != "" {
				prefix += "[" + is.Kind + "] "
			}
			fmt.Fprintf(os.Stdout, "%s: %s%s\n", loc, prefix, is.Message)
			if is.SuggestedFix != "" {
				fmt.Fprintf(os.Stdout, "  fix: %s\n", is.SuggestedFix)
			}
		}
	}

	if len(res.Warnings) > 0 {
		fmt.Fprintf(os.Stdout, "Предупреждения:\n")
		printIssueList(res.Warnings)
	}

	if res.OK {
		if len(res.Warnings) > 0 {
			fmt.Fprintf(os.Stdout, "OK: ошибок не найдено (%d предупреждений)\n", len(res.Warnings))
		} else {
			fmt.Fprintln(os.Stdout, "OK: ошибок не найдено")
		}
		return
	}
	printIssueList(res.Issues)
	fmt.Fprintf(os.Stderr, "\nНайдено ошибок: %d\n", res.Total)
}
