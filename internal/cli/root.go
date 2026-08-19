package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/version"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "onebase",
	Short:   "onebase — metadata-driven business platform",
	Version: version.String(),
	RunE:    runStart,
}

// errSilentExit — команда уже всё сообщила пользователю сама и просит лишь
// ненулевой код возврата. Так `onebase doctor` отличает «нашёл проблемы в базе»
// от «сама команда сломалась»: первое не должно печататься как ошибка запуска.
var errSilentExit = errors.New("команда завершилась с ненулевым кодом")

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, errSilentExit) {
			os.Exit(1)
		}
		showError(fmt.Sprintf("Ошибка запуска onebase:\n\n%s", err.Error()))
		os.Exit(1)
	}
}

func init() {
	// Ошибку печатает showError (единая точка: stderr + при необходимости окно),
	// поэтому cobra не должен печатать её сам — иначе вывод дублируется.
	rootCmd.SilenceErrors = true
	rootCmd.PersistentFlags().BoolVar(&noGUI, "no-gui", false,
		"не открывать GUI; ошибки остаются в stderr — для скриптов и CI (также ONEBASE_NO_GUI=1)")
	rootCmd.AddCommand(initCmd, devCmd, runCmd, migrateCmd, buildCmd, startCmd, ibasesCmd, convertCmd, backupCmd, restoreCmd, demoResetCmd, deployCmd, serviceCmd, benchCmd, generateCmd, recalcTotalsCmd, reindexCmd, updateCmd, settingsCmd, jobsCmd, deviceAgentCmd)
}
