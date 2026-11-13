package onec_forms

import (
	"path/filepath"
	"testing"
)

// group_titles — настройка OneBase, а не 1С: в XML её нет, но YAML формы
// обязан сохранить её при чтении и записи (обмен формами, конфигуратор).
func TestFormYAMLKeepsGroupTitles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "объекта.form.yaml")
	if err := WriteFormYAML(&IRForm{Name: "ФормаОбъекта", Kind: "object", Entity: "Звонок", GroupTitles: "bold"}, path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadFormYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.GroupTitles != "bold" {
		t.Fatalf("GroupTitles после записи и чтения = %q", back.GroupTitles)
	}
	if ToFormModule(back).GroupTitles != "bold" {
		t.Fatal("GroupTitles потерян при переводе в модель формы")
	}
}
