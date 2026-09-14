package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Заявка #1356: локатор предупреждения печатался по `form.Name`, а не по имени
// файла. Для `name: ФормаОбъекта` в `объекта.form.yaml` получался путь
// `forms/инвентаризация/ФормаОбъекта.form.yaml`, которого на диске нет.
//
// Проверка сильнее сравнения строк: каждый напечатанный локатор формы обязан
// открываться как файл. Ровно это и есть польза от `onebase check`.

func TestRunFullFormLocatorsOpenAsFiles(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "инвентаризация.yaml"), `name: Инвентаризация
fields:
  - name: Наименование
    type: string
`)
	// Имя файла и имя формы намеренно разные, каталог — в исходном регистре.
	mkFile(t, filepath.Join(dir, "forms", "Инвентаризация", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Инвентаризация
elements:
  - kind: ПолеВвода
    name: Наименование
    data_path: Объект.Наименование
    mask: "00.00.00"
`)
	mkFile(t, filepath.Join(dir, "processors", "загрузка.yaml"), `name: ЗагрузкаПрайса
title: Загрузка прайса
`)
	mkFile(t, filepath.Join(dir, "forms", "ЗагрузкаПрайса", "главная.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбработки
  kind: object
elements:
  - kind: ПолеВвода
    name: Файл
    mask: "00.00.00"
`)

	res := RunFullWithOptions(dir, Options{})

	var seen []string
	for _, issue := range append(append([]Issue{}, res.Issues...), res.Warnings...) {
		if !strings.HasSuffix(issue.File, ".form.yaml") {
			continue
		}
		seen = append(seen, issue.File)
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(issue.File))); err != nil {
			t.Errorf("локатор %q не открывается как файл: %v\n  (code=%s: %s)",
				issue.File, err, issue.Code, issue.Message)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("ожидались локаторы формы сущности и формы обработки, получили %v", seen)
	}

	// Проверяем и сам вид пути: он обязан сохранять регистр каталога, иначе на
	// case-sensitive файловой системе после ExportToDir файл не найдётся.
	wantEntity := "forms/Инвентаризация/объекта.form.yaml"
	wantProc := "forms/ЗагрузкаПрайса/главная.form.yaml"
	for _, want := range []string{wantEntity, wantProc} {
		found := false
		for _, got := range seen {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("среди локаторов нет %q: %v", want, seen)
		}
	}
}

// Форма без файла на диске — автоформа из `src/*.form.os` или собранная в
// памяти — сохраняет прежний синтезированный вид: открывать там нечего, и
// менять этот вид незачем.
func TestFormFileLabelFallsBackWithoutSourcePath(t *testing.T) {
	ent := &metadata.Entity{Name: "ВходящееПисьмо"}
	if got := formFileLabel(ent, &metadata.FormModule{Name: "объекта"}); got != "forms/входящееписьмо/объекта.form.yaml" {
		t.Errorf("fallback сломан: %q", got)
	}
	if got := formFileLabel(ent, &metadata.FormModule{}); got != "forms/входящееписьмо/объекта.form.yaml" {
		t.Errorf("форма без имени должна давать прежний вид: %q", got)
	}
	if got := procFormFileLabel("ЗагрузкаПрайса", &metadata.FormModule{Name: "объекта"}); got != "forms/загрузкапрайса/объекта.form.yaml" {
		t.Errorf("fallback обработки сломан: %q", got)
	}
}

// Путь из загрузчика сильнее синтеза, даже если имя формы совпадает с именем
// файла: источник истины один.
func TestFormFileLabelPrefersSourcePath(t *testing.T) {
	form := &metadata.FormModule{Name: "объекта", SourcePath: "forms/Инвентаризация/объекта.form.yaml"}
	if got := formFileLabel(&metadata.Entity{Name: "Инвентаризация"}, form); got != form.SourcePath {
		t.Errorf("локатор не взят из SourcePath: %q", got)
	}
}
