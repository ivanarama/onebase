package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/onec_forms"
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
      - { name: Ошибка, type: numericExtra }
elements: []`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	issues := formAttrTypeIssues(CheckLintProject(dir, proj, nil))
	if len(issues) != 1 {
		t.Fatalf("ожидалось 1 предупреждение формы обработки, получено %d: %+v", len(issues), issues)
	}
	if !strings.Contains(issues[0].Message, "Ошибка") {
		t.Errorf("предупреждение не относится к неизвестному типу: %+v", issues)
	}
	if !strings.Contains(issues[0].File, "forms/проверка/") {
		t.Errorf("файл = %q, ожидался путь формы обработки", issues[0].File)
	}
	if strings.Contains(issues[0].Message, "Период") {
		t.Errorf("dateSuffix типизируется runtime как дата и не должен считаться строкой: %+v", issues)
	}
}

func TestCheckLintFormAttrTypes_AcceptsCanonicalImportedDecimal(t *testing.T) {
	importedType := onec_forms.Type1CToOneBase("xs:decimal", 15, 0, "")
	if importedType != "decimal(15)" {
		t.Fatalf("канонический импорт xs:decimal = %q, ожидался decimal(15)", importedType)
	}

	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "processors", "проверка.yaml"), `name: Проверка
params: []`)
	mkFile(t, filepath.Join(dir, "forms", "проверка", "основная.form.yaml"), `schema: onebase.form/v1
form:
  name: Основная
  kind: object
  entity: Проверка
attributes:
  - name: Сумма
    type: "`+importedType+`"
    save: false
elements: []`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()

	if issues := formAttrTypeIssues(CheckLintProject(dir, proj, nil)); len(issues) != 0 {
		t.Fatalf("канонический decimal(15) импорта ошибочно признан строкой: %+v", issues)
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
