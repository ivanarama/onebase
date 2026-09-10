package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Регистр в имени файла модуля формы (issue #1452).
//
// Имя строилось как strings.ToLower(entityName)+".form.os" и сразу открывалось,
// поэтому на регистрозависимой файловой системе `Заказы.form.os` молча не
// подхватывался: форма оставалась без модуля, а обработчики просто не
// срабатывали. #1317 чинил ровно ту же переносимость для каталога управляемых
// форм — сопоставлением по факту вместо вычисленного имени.
//
// Тест идёт через project.Load — тот же путь, которым проект читают `onebase
// check`, `run` и `procrun`, а не через сам загрузчик форм.

func writeFormCaseProject(t *testing.T, moduleFile string) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"catalogs", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "catalogs", "заказы.yaml"),
		[]byte("name: Заказы\nfields:\n  - name: Наименование\n    type: string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := "Процедура ПриОткрытииФормы()\n  Сообщить(\"открыто\");\nКонецПроцедуры\n"
	if err := os.WriteFile(filepath.Join(dir, "src", moduleFile), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// entityForm возвращает единственный модуль формы справочника Заказы.
func entityForm(t *testing.T, dir string) (string, []string) {
	t.Helper()
	proj, err := Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(proj.Close)
	for _, ent := range proj.Entities {
		if !strings.EqualFold(ent.Name, "Заказы") {
			continue
		}
		if len(ent.Forms) != 1 {
			t.Fatalf("ожидалась одна форма, получено %d", len(ent.Forms))
		}
		var procs []string
		for name := range ent.Forms[0].Procedures {
			procs = append(procs, name)
		}
		sort.Strings(procs)
		return ent.Forms[0].Name, procs
	}
	t.Fatalf("справочник Заказы не загрузился")
	return "", nil
}

func entityFormNames(t *testing.T, dir string) []string {
	t.Helper()
	proj, err := Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(proj.Close)
	for _, ent := range proj.Entities {
		if !strings.EqualFold(ent.Name, "Заказы") {
			continue
		}
		var names []string
		for _, f := range ent.Forms {
			names = append(names, f.Name)
		}
		return names
	}
	t.Fatalf("справочник Заказы не загрузился")
	return nil
}

func TestLoad_FormModuleFileNotLowercase(t *testing.T) {
	// Имя файла не в нижнем регистре: до правки модуль молча терялся.
	dir := writeFormCaseProject(t, "Заказы.form.os")
	names := entityFormNames(t, dir)
	if len(names) != 1 || names[0] != "ФормаОбъекта" {
		t.Errorf("модуль формы не подхватился: %v", names)
	}
}

func TestLoad_FormModuleLowercaseStillWorks(t *testing.T) {
	// Канонический регистр обязан работать как раньше.
	dir := writeFormCaseProject(t, "заказы.form.os")
	names := entityFormNames(t, dir)
	if len(names) != 1 || names[0] != "ФормаОбъекта" {
		t.Errorf("модуль формы в нижнем регистре потерялся: %v", names)
	}
}

func TestLoad_FormModuleCustomFormKeepsNameCase(t *testing.T) {
	// Произвольная форма: регистр имени формы берётся из файла, а не из
	// приведённого к нижнему регистру имени, иначе «ПечатьСчёта» стала бы
	// «Печатьсчёта».
	dir := writeFormCaseProject(t, "Заказы_ПечатьСчёта.form.os")
	names := entityFormNames(t, dir)
	if len(names) != 1 || names[0] != "ПечатьСчёта" {
		t.Errorf("имя произвольной формы искажено: %v", names)
	}
}

func TestLoad_FormModuleCaseCollisionPrefersLowercase(t *testing.T) {
	// Два файла, различающихся только регистром, — разные файлы. Побеждает
	// каноническое нижнее имя: проект, который грузился до правки, обязан
	// грузить тот же самый модуль.
	dir := writeFormCaseProject(t, "заказы.form.os")
	upper := filepath.Join(dir, "src", "Заказы.form.os")
	if err := os.WriteFile(upper, []byte("Процедура ПередЗаписью()\n  Сообщить(\"не тот файл\");\nКонецПроцедуры\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(upper); err != nil {
		t.Skip("файловая система не различает регистр в именах файлов")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Skip("файловая система не различает регистр в именах файлов")
	}
	// Файлы различаются процедурой, поэтому видно, какой из них прочитан.
	name, procs := entityForm(t, dir)
	if name != "ФормаОбъекта" {
		t.Fatalf("форма загрузилась как %q", name)
	}
	if len(procs) != 1 || procs[0] != "ПриОткрытииФормы" {
		t.Errorf("прочитан не канонический файл: процедуры %v", procs)
	}
}
