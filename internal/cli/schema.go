package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ivantit66/onebase/internal/configschema"
	"github.com/spf13/cobra"
)

var schemaCmd = &cobra.Command{
	Use:   "schema [kind]",
	Short: "Показать JSON Schema для YAML-артефактов конфигурации",
	Long: `Печатает JSON Schema для выбранного вида YAML-файла конфигурации.
Схемы рассчитаны на редакторы, MCP-клиенты и ИИ-инструменты: ключевые поля
описаны строго, а unknown keys разрешены для обратной совместимости.

Примеры:
  onebase schema --list
  onebase schema document
  onebase schema form`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          runSchema,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	schemaCmd.Flags().Bool("list", false, "показать доступные виды схем")
	rootCmd.AddCommand(schemaCmd)
}

func runSchema(cmd *cobra.Command, args []string) error {
	list, _ := cmd.Flags().GetBool("list")
	if list || len(args) == 0 {
		fmt.Fprintln(os.Stdout, strings.Join(configschema.Kinds(), "\n"))
		return nil
	}
	s, ok := configschema.Get(args[0])
	if !ok {
		return fmt.Errorf("неизвестный вид схемы %q\nдоступно:\n%s", args[0], strings.Join(configschema.Kinds(), "\n"))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}
