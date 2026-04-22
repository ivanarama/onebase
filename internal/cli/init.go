package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Scaffold a new onebase project",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	dirs := []string{"config", "catalogs", "documents", "src"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	counterparty := filepath.Join(dir, "catalogs", "контрагент.yaml")
	if _, err := os.Stat(counterparty); os.IsNotExist(err) {
		content := "name: Контрагент\nfields:\n  - name: Наименование\n    type: string\n  - name: ИНН\n    type: string\n"
		if err := os.WriteFile(counterparty, []byte(content), 0o644); err != nil {
			return err
		}
	}
	invoice := filepath.Join(dir, "documents", "счёт.yaml")
	if _, err := os.Stat(invoice); os.IsNotExist(err) {
		content := "name: Счёт\nfields:\n  - name: Номер\n    type: string\n  - name: Дата\n    type: date\n  - name: Контрагент\n    type: reference:Контрагент\n"
		if err := os.WriteFile(invoice, []byte(content), 0o644); err != nil {
			return err
		}
	}
	src := filepath.Join(dir, "src", "счёт.os")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		content := "Процедура ПриЗаписи()\n  Если this.Номер = \"\" Тогда\n    Error(\"Номер обязателен\");\n  КонецЕсли;\nКонецПроцедуры\n"
		if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stdout, "project initialized in %s\n", dir)
	return nil
}
