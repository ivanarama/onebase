package project

import (
	"os"
	"path/filepath"
)

// Scaffold creates a minimal onebase project structure in dir.
func Scaffold(dir, name string) error {
	dirs := []string{"config", "catalogs", "documents", "registers", "reports", "src"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}

	files := map[string]string{
		filepath.Join(dir, "config", "app.yaml"): "name: " + name + "\nversion: \"1.0\"\n",
		filepath.Join(dir, "catalogs", "контрагент.yaml"): "name: Контрагент\nfields:\n" +
			"  - name: Наименование\n    type: string\n" +
			"  - name: ИНН\n    type: string\n",
		filepath.Join(dir, "documents", "счёт.yaml"): "name: Счёт\nfields:\n" +
			"  - name: Номер\n    type: string\n" +
			"  - name: Дата\n    type: date\n" +
			"  - name: Контрагент\n    type: reference:Контрагент\n",
		filepath.Join(dir, "src", "счёт.os"): "Процедура ПриЗаписи()\n" +
			"  Если this.Номер = \"\" Тогда\n" +
			"    Error(\"Номер обязателен\");\n" +
			"  КонецЕсли;\n" +
			"КонецПроцедуры\n",
	}

	for path, content := range files {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
