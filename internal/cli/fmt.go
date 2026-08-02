package cli

import (
	stdfmt "fmt"

	"github.com/ivantit66/onebase/internal/configfmt"
	"github.com/spf13/cobra"
)

var fmtCmd = &cobra.Command{
	Use:   "fmt [path...]",
	Short: "Отформатировать YAML-конфигурацию OneBase канонически",
	Long: `Форматирует YAML-файлы стабильным writer'ом: сортирует ключи mapping-узлов,
задаёт одинаковые отступы и сохраняет детерминированный вывод. DSL .os пока не
форматируется, потому что в платформе ещё нет AST-writer'а для языка.`,
	Args:          cobra.ArbitraryArgs,
	RunE:          runFmt,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	fmtCmd.Flags().String("project", ".", "путь к каталогу конфигурации, если path не задан")
	fmtCmd.Flags().Bool("check", false, "только проверить, что файлы уже отформатированы")
	rootCmd.AddCommand(fmtCmd)
}

func runFmt(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		projectDir, _ := cmd.Flags().GetString("project")
		args = []string{projectDir}
	}
	check, _ := cmd.Flags().GetBool("check")
	files, err := configfmt.CollectYAMLFiles(args)
	if err != nil {
		return err
	}
	changed := 0
	for _, path := range files {
		ok, err := configfmt.FormatYAMLFile(path, check)
		if err != nil {
			return err
		}
		if !ok {
			changed++
			if check {
				outf("%s: требует форматирования\n", path)
			} else {
				outf("%s: отформатирован\n", path)
			}
		}
	}
	if check && changed > 0 {
		return stdfmt.Errorf("найдено неотформатированных YAML-файлов: %d", changed)
	}
	if changed == 0 {
		outln("OK: YAML уже в каноническом формате")
	}
	return nil
}
