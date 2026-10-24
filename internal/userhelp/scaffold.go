package userhelp

import (
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/fsmode"
)

// Scaffold adds a minimal handbook without replacing any existing file.
func Scaffold(dir string) error {
	base := filepath.Join(dir, filepath.FromSlash(Directory))
	if err := os.MkdirAll(filepath.Join(base, "images"), fsmode.Dir); err != nil {
		return err
	}
	files := map[string]string{
		"index.yaml":      "title: Руководство пользователя\nenabled: true\nauto_update: true\nchapters:\n  - id: introduction\n    title: Начало работы\n    file: introduction.md\n",
		"introduction.md": "# Начало работы\n\nРуководство описывает работу с этой конфигурацией.\n\nПри разработке дополняйте его инструкциями по реализованным сценариям.\n",
	}
	for name, content := range files {
		f, err := os.OpenFile(filepath.Join(base, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fsmode.File)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(content)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
