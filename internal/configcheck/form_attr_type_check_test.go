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
  - name: ПоискЧисло
    type: decimal(15,2)
    save: false
  - name: Строки
    type: ValueTable
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

	issues := CheckLintFormAttrTypes(proj)
	if len(issues) != 1 {
		t.Fatalf("ожидалось 1 предупреждение (только «Дата»), получено %d: %+v", len(issues), issues)
	}
	got := issues[0]
	if !strings.Contains(got.Message, "ПоискДатаС") || !strings.Contains(got.Message, "Дата") {
		t.Errorf("сообщение должно называть реквизит и его тип, получено %q", got.Message)
	}
	if got.Code != "form.attr-type" {
		t.Errorf("код = %q, ожидался form.attr-type", got.Code)
	}
	if !strings.Contains(got.File, "forms/заявка/") {
		t.Errorf("файл = %q, ожидался путь формы", got.File)
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

	if issues := CheckLintFormAttrTypes(proj); len(issues) != 0 {
		t.Fatalf("ожидалось 0 предупреждений, получено %d: %+v", len(issues), issues)
	}
}
