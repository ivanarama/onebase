package loader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedFormLoader_TextBold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "объекта.form.yaml")
	src := "schema: onebase.form/v1\nform:\n  entity: Заявка\n  name: ФормаОбъекта\n  kind: object\nelements:\n  - kind: ПолеВвода\n    name: ПолеКлиент\n    data_path: Объект.Клиент\n    text_bold: true\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	form, err := NewManagedFormLoader().LoadFormFile(path, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Elements) != 1 || !form.Elements[0].TextBold {
		t.Fatalf("text_bold не прочитан: %+v", form.Elements)
	}
}
