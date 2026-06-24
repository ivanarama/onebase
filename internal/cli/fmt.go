package cli

import (
	"fmt"
	"os"

	cfgfmt "github.com/ivantit66/onebase/internal/configfmt"
	"github.com/spf13/cobra"
)

var fmtCmd = &cobra.Command{
	Use:   "fmt [path ...]",
	Short: "Отформатировать YAML-файлы конфигурации",
	Long: `Форматирует YAML-артефакты конфигурации в канонический вид:
фиксированный порядок ключей, раскрытые mapping/list блоки и отступ 2 пробела.
Если пути не указаны, форматируется каталог из --project.

Примеры:
  onebase fmt --project examples/minimal
  onebase fmt --check examples/minimal
  onebase fmt catalogs/ documents/`,
	RunE:          runFmt,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	fmtCmd.Flags().String("project", ".", "путь к каталогу конфигурации")
	fmtCmd.Flags().Bool("check", false, "только проверить, не изменяя файлы")
	rootCmd.AddCommand(fmtCmd)
}

func runFmt(cmd *cobra.Command, args []string) error {
	projectDir, _ := cmd.Flags().GetString("project")
	checkOnly, _ := cmd.Flags().GetBool("check")
	if len(args) == 0 {
		args = []string{projectDir}
	}

	files, err := cfgfmt.CollectYAMLFiles(args)
	if err != nil {
		return err
	}
	var changed []string
	for _, file := range files {
		var didChange bool
		if checkOnly {
			didChange, err = cfgfmt.CheckFile(file)
		} else {
			didChange, err = cfgfmt.FormatFile(file)
		}
		if err != nil {
			return cfgfmt.FormatError(file, err)
		}
		if didChange {
			changed = append(changed, file)
			fmt.Fprintln(os.Stdout, file)
		}
	}

	if checkOnly && len(changed) > 0 {
		return fmt.Errorf("fmt: требуется форматирование YAML-файлов: %d", len(changed))
	}
	if len(changed) == 0 {
		fmt.Fprintf(os.Stdout, "OK: YAML-файлов проверено: %d\n", len(files))
	}
	return nil
}
