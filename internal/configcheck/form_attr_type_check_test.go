package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
)

// Нераспознанный тип реквизита формы не ошибка и не пустое место: значение
// приходит в обработчик СТРОКОЙ. «Дата» по-русски выглядит рабочей, а ведёт
// себя как строка — предупреждение делает это видимым.
func TestCheckLintFormAttrTypes(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
  - name: Улица
    type: string`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "формаобъекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
attributes:
  - name: ПоискУлица
    type: string
    save: false
  - name: ПоискДатаС
    type: Дата
    save: false
  - name: ПоискСклад
    type: CatalogRef.Заявка
    save: false
  - name: ОпечаткаСсылки
    type: catalogref.Заявка
    save: false
  - name: ПоискЧисло
    type: decimal(15,2)
    save: false
  - name: Строки
    type: ValueTable
    save: false
  - name: ПочтиТаблица
    type: ValueTableExtra
    save: false
elements:
  - kind: ПолеВвода
    name: ПолеУлица
    data_path: Объект.Улица`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	issues := formAttrTypeIssues(CheckLintProject(dir, proj, nil))
	if len(issues) != 3 {
		t.Fatalf("ожидалось 3 предупреждения, получено %d: %+v", len(issues), issues)
	}
	for _, want := range []string{"ПоискДатаС", "ОпечаткаСсылки", "ПочтиТаблица"} {
		found := false
		for _, got := range issues {
			if strings.Contains(got.Message, want) {
				found = true
				if !strings.Contains(got.File, "forms/заявка/") {
					t.Errorf("файл = %q, ожидался путь формы", got.File)
				}
			}
		}
		if !found {
			t.Errorf("нет предупреждения для %s: %+v", want, issues)
		}
	}
}

func TestCheckLintFormAttrTypes_ProcessorAndValueTableColumns(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "проверка.yaml"), `name: Проверка
params: []`)
	mkFile(t, filepath.Join(dir, "forms", "проверка", "основная.form.yaml"), `schema: onebase.form/v1
form:
  name: Основная
  kind: object
  entity: Проверка
attributes:
  - name: Период
    type: dateSuffix
    save: false
  - name: Строки
    type: ValueTable
    save: false
    columns:
      - { name: Текст, type: string }
      - { name: Количество, type: number }
      - { name: Цена, type: "decimal(15,2)" }
      - { name: Ссылка, type: CatalogRef.Товары }
      - { name: Ошибка, type: numberExtra }
elements: []`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	issues := formAttrTypeIssues(CheckLintProject(dir, proj, nil))
	if len(issues) != 2 {
		t.Fatalf("ожидалось 2 предупреждения формы обработки, получено %d: %+v", len(issues), issues)
	}
	for _, want := range []string{"Период", "Ошибка"} {
		found := false
		for _, got := range issues {
			if strings.Contains(got.Message, want) {
				found = true
				if !strings.Contains(got.File, "forms/проверка/") {
					t.Errorf("файл = %q, ожидался путь формы обработки", got.File)
				}
			}
		}
		if !found {
			t.Errorf("нет предупреждения для %s: %+v", want, issues)
		}
	}
}

// Пустой тип пропускаем: его отдельно разбирает импорт форм 1С, и дублировать
// то же предупреждение из линта незачем.
func TestCheckLintFormAttrTypes_EmptyTypeSilent(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
  - name: Улица
    type: string`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "формаобъекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
attributes:
  - name: БезТипа
    save: false
elements:
  - kind: ПолеВвода
    name: ПолеУлица
    data_path: Объект.Улица`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	if issues := formAttrTypeIssues(CheckLintProject(dir, proj, nil)); len(issues) != 0 {
		t.Fatalf("ожидалось 0 предупреждений, получено %d: %+v", len(issues), issues)
	}
}

func formAttrTypeIssues(issues []Issue) []Issue {
	var result []Issue
	for _, issue := range issues {
		if issue.Code == "form.attr-type" {
			result = append(result, issue)
		}
	}
	return result
}
