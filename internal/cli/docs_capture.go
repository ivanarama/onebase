package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

//go:embed help_capture.cjs
var helpCaptureScript string

func newDocsCaptureCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "capture", Short: "Снять иллюстрации по JSON-сценарию (нужны Node.js и Playwright)", RunE: runDocsCapture}
	cmd.Flags().String("project", ".", "каталог конфигурации")
	cmd.Flags().String("scenario", "", "путь к JSON-сценарию, относительно конфигурации")
	cmd.Flags().String("storage-state", "", "состояние входа браузера (секретный файл вне репозитория)")
	cmd.Flags().String("playwright-module", "playwright", "модуль Playwright или абсолютный путь к нему")
	return cmd
}

func runDocsCapture(cmd *cobra.Command, _ []string) error {
	dir, _ := cmd.Flags().GetString("project")
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	scenario, _ := cmd.Flags().GetString("scenario")
	if scenario == "" {
		return fmt.Errorf("--scenario is required; use a demonstration base with synthetic data")
	}
	if !filepath.IsAbs(scenario) {
		scenario = filepath.Join(dir, scenario)
	}
	raw, err := os.ReadFile(scenario)
	if err != nil {
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("invalid screenshot scenario JSON")
	}
	state, _ := cmd.Flags().GetString("storage-state")
	if state != "" {
		state, err = filepath.Abs(state)
		if err != nil {
			return err
		}
	}
	module, _ := cmd.Flags().GetString("playwright-module")
	config, err := json.Marshal(map[string]any{"scenario": json.RawMessage(raw), "storageState": state,
		"module": module, "output": filepath.Join(dir, "docs", "help", "images")})
	if err != nil {
		return err
	}
	// Constant program and constant JS; scenario data is passed on stdin, never
	// interpolated into shell or JavaScript source. No subprocess shell is used.
	process := exec.CommandContext(cmd.Context(), "node", "-e", helpCaptureScript+"\nmodule.exports.run();") //nolint:gosec // G204: program is constant and JavaScript is embedded at build time; all user data is passed on stdin, never as source or shell commands.
	process.Dir = dir
	process.Stdin = bytes.NewReader(config)
	process.Stdout, process.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := process.Run(); err != nil {
		return fmt.Errorf("screenshot capture: %w; this does not affect configuration check, startup or release", err)
	}
	return nil
}
