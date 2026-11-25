package onec_forms

import (
	"path/filepath"
	"testing"
)

// density — настройка OneBase, а не 1С: в XML её нет, но YAML формы обязан
// сохранить её при чтении и записи (обмен формами, конфигуратор).
func TestFormYAMLKeepsDensity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "объекта.form.yaml")
	if err := WriteFormYAML(&IRForm{Name: "ФормаОбъекта", Kind: "object", Entity: "Звонок", Density: "compact"}, path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadFormYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Density != "compact" {
		t.Fatalf("Density после записи и чтения = %q", back.Density)
	}
	if ToFormModule(back).Density != "compact" {
		t.Fatal("Density потерян при переводе в модель формы")
	}
}
