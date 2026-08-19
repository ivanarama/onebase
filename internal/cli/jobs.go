package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Управление включённостью регламентных заданий из командной строки (#991).
//
// Административное решение «использования» задания живёт в базе, а не в YAML:
// оно переживает обновление конфигурации и действует на своей базе. В UI это
// тумблер /ui/admin/scheduled; здесь — то же самое для headless-окружения:
// скрипт установки, CI, база без запущенного сервера. Живой кейс — база с
// поставляемым выключенным ТелеграмПоллингом, которого надо включить при
// развёртывании, не трогая исходники конфигурации.
//
// Имя задания НЕ сверяется с конфигурацией: команда работает по базе без
// проекта (как settings), а орфанный ключ безвреден — планировщик просто не
// найдёт задание с таким именем. Сверка имён здесь добавила бы требование
// указывать --project, которое headless-сценарию чуждо.

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Регламентные задания: включить/выключить без UI",
	Long: "Административное включение и выключение регламентных заданий (issue #991).\n\n" +
		"Решение хранится в базе и переживает обновление конфигурации; конфигурация\n" +
		"задаёт лишь исходное состояние. Ручной запуск (run-now, procrun) работает\n" +
		"и для выключенного задания.",
}

var (
	jobsEnableCmd = &cobra.Command{
		Use:   "enable <имя задания>",
		Short: "Включить задание администратором",
		Args:  cobra.ExactArgs(1),
		RunE:  runJobsSet(true),
	}
	jobsDisableCmd = &cobra.Command{
		Use:   "disable <имя задания>",
		Short: "Выключить задание администратором",
		Args:  cobra.ExactArgs(1),
		RunE:  runJobsSet(false),
	}
	jobsResetCmd = &cobra.Command{
		Use:   "reset <имя задания>",
		Short: "Убрать решение — задание снова следует конфигурации",
		Args:  cobra.ExactArgs(1),
		RunE:  runJobsReset,
	}
	jobsStatusCmd = &cobra.Command{
		Use:   "status <имя задания>",
		Short: "Показать административное решение по заданию",
		Args:  cobra.ExactArgs(1),
		RunE:  runJobsStatus,
	}
)

func init() {
	for _, c := range []*cobra.Command{jobsEnableCmd, jobsDisableCmd, jobsResetCmd, jobsStatusCmd} {
		c.Flags().String("db", "", "database URL (overrides DATABASE_URL env)")
		c.Flags().String("sqlite", "", "path to SQLite database file (alternative to --db)")
	}
	jobsCmd.AddCommand(jobsEnableCmd, jobsDisableCmd, jobsResetCmd, jobsStatusCmd)
}

// jobNameOrEmpty возвращает чистое имя задания или ошибку для пустого.
func jobNameOrEmpty(arg string) (string, error) {
	name := strings.TrimSpace(arg)
	if name == "" {
		return "", fmt.Errorf("имя задания не задано")
	}
	return name, nil
}

func runJobsSet(on bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		name, err := jobNameOrEmpty(args[0])
		if err != nil {
			return err
		}
		db, err := openSettingsDB(cmd)
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.SaveScheduledEnabled(context.Background(), name, on); err != nil {
			return err
		}
		state := "выключено администратором"
		if on {
			state = "включено администратором"
		}
		outf("%s: %s\n", name, state)
		return nil
	}
}

func runJobsReset(cmd *cobra.Command, args []string) error {
	name, err := jobNameOrEmpty(args[0])
	if err != nil {
		return err
	}
	db, err := openSettingsDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.DeleteScheduledEnabled(context.Background(), name); err != nil {
		return err
	}
	outf("%s: административного решения нет, действует конфигурация\n", name)
	return nil
}

func runJobsStatus(cmd *cobra.Command, args []string) error {
	name, err := jobNameOrEmpty(args[0])
	if err != nil {
		return err
	}
	db, err := openSettingsDB(cmd)
	if err != nil {
		return err
	}
	defer db.Close()

	on, ok, err := db.GetScheduledEnabled(context.Background(), name)
	if err != nil {
		return err
	}
	switch {
	case ok && on:
		outf("%s: включено администратором\n", name)
	case ok:
		outf("%s: выключено администратором\n", name)
	default:
		outf("%s: административного решения нет, действует конфигурация\n", name)
	}
	return nil
}
