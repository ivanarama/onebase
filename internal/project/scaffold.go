package project

import (
	"github.com/ivantit66/onebase/internal/fsmode"
	"os"
	"path/filepath"
)

// Scaffold creates a minimal onebase project structure in dir.
func Scaffold(dir, name string) error {
	dirs := []string{"config", "catalogs", "documents", "registers", "reports", "src"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), fsmode.Dir); err != nil {
			return err
		}
	}

	// Создаём только пустую валидную конфигурацию: каталог структуры + app.yaml.
	// Демонстрационные объекты (Контрагент/Счёт) намеренно НЕ создаются — раньше
	// они подмешивались как «рудименты» в новые и импортируемые конфигурации
	// (issue #16). Пустая конфигурация — чистый старт, как пустая ИБ в 1С.
	files := map[string]string{
		filepath.Join(dir, "config", "app.yaml"): "name: " + name + "\nversion: \"1.0\"\n",
	}

	for path, content := range files {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), fsmode.File); err != nil {
				return err
			}
		}
	}
	return nil
}
