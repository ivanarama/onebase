package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ivantit66/onebase/internal/aicontract"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/spf13/cobra"
)

var describeCmd = &cobra.Command{
	Use:   "describe",
	Short: "Выгрузить структуру конфигурации в JSON или компактный текст",
	Long: `Сериализует метаданные конфигурации в машинный контракт для ИИ,
MCP и внешних инструментов. По умолчанию печатает JSON schemaVersion=2.
Флаг --compact печатает короткий текстовый срез для prompt-контекста.

Примеры:
  onebase describe --project C:\Projects\OneBaseConfs\PuT
  onebase describe --compact --id <baseID>
  onebase describe --id <baseID> | jq .processors`,
	RunE:          runDescribe,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(describeCmd)
	describeCmd.Flags().Bool("compact", false, "вывести компактный текстовый срез вместо JSON")
	describeCmd.Flags().Bool("full", false, "явно вывести полный JSON-контракт (поведение по умолчанию)")
	rootCmd.AddCommand(describeCmd)
}

func runDescribe(cmd *cobra.Command, _ []string) error {
	compact, _ := cmd.Flags().GetBool("compact")
	full, _ := cmd.Flags().GetBool("full")
	if compact && full {
		return fmt.Errorf("флаги --compact и --full взаимоисключающие")
	}

	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return err
	}
	defer proj.Close()

	if compact {
		fmt.Fprint(os.Stdout, aicontract.ProjectSchemaText(proj))
		return nil
	}

	out, err := aicontract.Build(bc.Dir, proj)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
