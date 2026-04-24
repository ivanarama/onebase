package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "onebase",
	Short: "onebase — metadata-driven business platform",
	RunE:  runStart,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		showError(fmt.Sprintf("Ошибка запуска onebase:\n\n%s", err.Error()))
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(initCmd, devCmd, runCmd, migrateCmd, buildCmd, startCmd, ibasesCmd, convertCmd)
}
