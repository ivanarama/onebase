package configcheck

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestCheckFormReadOnlyWhen_RejectsOnlyUnsupportedContainers(t *testing.T) {
	for _, kind := range []metadata.FormElementType{
		metadata.FormElementGroupBox,
		metadata.FormElementPages,
		metadata.FormElementPage,
	} {
		t.Run(string(kind), func(t *testing.T) {
			issues := CheckFormReadOnlyWhen(projWithElement(&metadata.FormElement{
				Kind: kind, Name: "Контейнер", ReadOnlyWhen: "  Истина  ",
			}))
			if len(issues) != 1 {
				t.Fatalf("ожидалась 1 ошибка, получили %d: %+v", len(issues), issues)
			}
			if issues[0].Code != "form.readonly-when-container" {
				t.Errorf("Code = %q", issues[0].Code)
			}
			if !strings.Contains(issues[0].Message, string(kind)) || !strings.Contains(issues[0].Message, "не распространяется") {
				t.Errorf("сообщение не объясняет ограничение %s: %q", kind, issues[0].Message)
			}
			if issues[0].SuggestedFix == "" {
				t.Error("нет подсказки по исправлению")
			}
		})
	}
}

func TestCheckFormReadOnlyWhen_AllowsSupportedPlacements(t *testing.T) {
	elements := []*metadata.FormElement{
		{Kind: metadata.FormElementGroupBox, Name: "СкрываемаяГруппа", HiddenWhen: "Истина"},
		{Kind: metadata.FormElementPages, Name: "СкрываемыеСтраницы", HiddenWhen: "Истина"},
		{Kind: metadata.FormElementPage, Name: "СкрываемаяСтраница", HiddenWhen: "Истина"},
		{Kind: metadata.FormElementField, Name: "Поле", ReadOnlyWhen: "Истина"},
		{Kind: metadata.FormElementTablePart, Name: "Таблица", ReadOnlyWhen: "Истина"},
		{Kind: metadata.FormElementTable, Name: "ОбычнаяТаблица", ReadOnlyWhen: "Истина"},
		{Kind: metadata.FormElementCommandBar, Name: "Панель", ReadOnlyWhen: "Истина"},
		{Kind: metadata.FormElementGroupBox, Name: "СтатическиЗапрещённаяГруппа", ReadOnly: true},
	}
	root := &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "Корень", Children: elements}
	if issues := CheckFormReadOnlyWhen(projWithElement(root)); len(issues) != 0 {
		t.Fatalf("поддерживаемые размещения не должны блокировать check: %+v", issues)
	}
}

func TestRunFull_FormReadonlyWhenContainersBlockEntityAndProcessor(t *testing.T) {
	dir := readonlyWhenProject(t)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: объекта
  kind: object
  entity: Заказ
elements:
  - kind: ГруппаФормы
    name: Группа
    readonly_when: 'Состояние = "Закрыт"'
  - kind: СтраницыФормы
    name: Страницы
    readonly_when: 'Состояние = "Закрыт"'
    children:
      - kind: Страница
        name: Страница
        readonly_when: 'Состояние = "Закрыт"'
`)
	mkFile(t, filepath.Join(dir, "forms", "проверкаформы", "основная.form.yaml"), `schema: onebase.form/v1
form:
  name: основная
  kind: custom
  entity: ПроверкаФормы
elements:
  - kind: ГруппаФормы
    name: ГруппаОбработки
    readonly_when: 'Состояние = "Закрыт"'
`)

	res := RunFull(dir)
	if res.OK {
		t.Fatal("RunFull принял readonly_when на контейнерах")
	}
	var got []Issue
	for _, issue := range res.Issues {
		if issue.Code == "form.readonly-when-container" {
			got = append(got, issue)
		}
	}
	if len(got) != 4 {
		t.Fatalf("ошибок form.readonly-when-container = %d, ожидалось 4: %+v", len(got), res.Issues)
	}
	wantFiles := map[string]int{
		"forms/заказ/объекта.form.yaml":          3,
		"forms/проверкаформы/основная.form.yaml": 1,
	}
	for _, issue := range got {
		wantFiles[issue.File]--
	}
	for file, left := range wantFiles {
		if left != 0 {
			t.Errorf("для %s остаток ожидаемых ошибок = %d; got=%+v", file, left, got)
		}
	}
}

func TestRunFull_FormReadonlyWhenSupportedPlacementsPass(t *testing.T) {
	dir := readonlyWhenProject(t)
	allowed := `schema: onebase.form/v1
form:
  name: %s
  kind: custom
  entity: %s
elements:
  - kind: ГруппаФормы
    name: СкрываемаяГруппа
    hidden_when: 'Состояние = "Закрыт"'
    children:
      - kind: ПолеВвода
        name: РедактируемоеПоле
        readonly_when: 'Состояние = "Закрыт"'
  - kind: ТабличнаяЧасть
    name: Таблица
    readonly_when: 'Состояние = "Закрыт"'
`
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), fmt.Sprintf(allowed, "объекта", "Заказ"))
	mkFile(t, filepath.Join(dir, "forms", "проверкаформы", "основная.form.yaml"), fmt.Sprintf(allowed, "основная", "ПроверкаФормы"))

	res := RunFull(dir)
	if !res.OK {
		t.Fatalf("RunFull отклонил поддерживаемые readonly_when/hidden_when: %+v", res.Issues)
	}
}

func readonlyWhenProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "заказ.yaml"), `name: Заказ
fields:
  - name: Состояние
    type: string
`)
	mkFile(t, filepath.Join(dir, "processors", "проверкаформы.yaml"), `name: ПроверкаФормы
params:
  - name: Состояние
    type: string
`)
	return dir
}
