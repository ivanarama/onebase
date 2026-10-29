package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hint_from в форме обработки проверяется полным checker'ом (тем же, что
// onebase check): опечатка в имени реквизита — ошибка, верное имя реквизита
// формы или параметра обработки — без ошибок (замечание ревью #2367).
func TestRunFull_HintFromInProcessorForm(t *testing.T) {
	write := func(t *testing.T, dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	project := func(t *testing.T, hintFrom string) string {
		dir := t.TempDir()
		write(t, dir, "config/app.yaml", "name: hintfrom\n")
		write(t, dir, "processors/ЗагрузкаПрайса.yaml", `name: ЗагрузкаПрайса
title: Загрузка прайса
params:
  - name: Файл
    type: string
`)
		write(t, dir, "forms/загрузкапрайса/ФормаОбработки.form.yaml", `schema: onebase.form/v1
form:
  name: ФормаОбработки
  kind: object
attributes:
  - name: Примечание
    type: string
elements:
  - kind: Кнопка
    name: КнЗагрузить
    title:
      ru: Загрузить
    hint_from: `+hintFrom+`
`)
		return dir
	}
	hintIssues := func(dir string) []string {
		var out []string
		for _, is := range RunFull(dir).Issues {
			if is.Code == "yaml.invalid" {
				t.Fatalf("мини-конфигурация не загрузилась: %s", is.Message)
			}
			if is.Code == "form.hint-from" {
				out = append(out, is.Message)
			}
		}
		return out
	}
	for _, ok := range []string{"Примечание", "Форма.Примечание", "Файл"} {
		if got := hintIssues(project(t, ok)); len(got) != 0 {
			t.Fatalf("hint_from %q: ложная ошибка: %v", ok, got)
		}
	}
	got := hintIssues(project(t, "Примичание"))
	if len(got) != 1 || !strings.Contains(got[0], "Примичание") {
		t.Fatalf("опечатка в hint_from формы обработки не найдена: %v", got)
	}
}
