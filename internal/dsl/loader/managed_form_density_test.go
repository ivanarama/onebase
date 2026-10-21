package loader

import (
	"os"
	"path/filepath"
	"testing"
)

// Ключ density доезжает из YAML формы до модели: иначе плотную раскладку
// было бы нечем задать в конфигурации.
func TestManagedFormLoader_Density(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ФормаОбъекта.form.yaml")
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\n  density: compact\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	form, err := NewManagedFormLoader().LoadFormFile(path, "Звонок")
	if err != nil {
		t.Fatalf("загрузка формы: %v", err)
	}
	if form.Density != "compact" {
		t.Fatalf("Density = %q, ожидалось compact", form.Density)
	}
}
