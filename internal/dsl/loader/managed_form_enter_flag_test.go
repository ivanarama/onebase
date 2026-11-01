package loader

import (
	"os"
	"path/filepath"
	"testing"
)

// Ключ enter_submits_form обязан доезжать до рантайма из YAML формы: иначе
// настройка есть только в Go-структуре, а в конфигурации задать её нечем
// (#1486). Умолчание — false: платформа переводит Enter в навигацию.
func TestManagedFormLoader_EnterSubmitsForm(t *testing.T) {
	for _, c := range []struct {
		name string
		yaml string
		want bool
	}{
		{"ключ не задан", "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Контрагент\n", false},
		{"ключ включён", "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Контрагент\n  enter_submits_form: true\n", true},
		{"ключ выключен явно", "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Контрагент\n  enter_submits_form: false\n", false},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ФормаОбъекта.form.yaml")
			if err := os.WriteFile(path, []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			form, err := NewManagedFormLoader().LoadFormFile(path, "Контрагент")
			if err != nil {
				t.Fatalf("загрузка формы: %v", err)
			}
			if form.EnterSubmitsForm != c.want {
				t.Fatalf("EnterSubmitsForm = %v, ожидалось %v", form.EnterSubmitsForm, c.want)
			}
		})
	}
}
