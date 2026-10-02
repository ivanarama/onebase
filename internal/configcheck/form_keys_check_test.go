package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func formKeysProject(elements ...*metadata.FormElement) *project.Project {
	city := &metadata.Entity{
		Name: "АдресныйКлассификатор", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	flat := &metadata.Entity{
		Name: "Филиал", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	request := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "НаселённыйПункт", Type: "reference:АдресныйКлассификатор", RefEntity: "АдресныйКлассификатор"},
			{Name: "Филиал", Type: "reference:Филиал", RefEntity: "Филиал"},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}}}},
	}
	request.Forms = []*metadata.FormModule{{
		Name: "Объекта", LayoutKind: metadata.FormLayoutManaged, Elements: elements,
	}}
	return &project.Project{Entities: []*metadata.Entity{city, flat, request}}
}

func messages(issues []Issue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, issue.Code+": "+issue.Message)
	}
	return strings.Join(parts, " | ")
}

func TestCheckFormAdminOnlyRejectsUnenforceablePlacement(t *testing.T) {
	// Поле-реквизит записи: сервер отбросит присланное значение, запрет настоящий.
	ok := formKeysProject(&metadata.FormElement{
		Kind: metadata.FormElementField, Name: "ПолеКомментарий",
		DataPath: "Объект.Комментарий", EditableAdminOnly: true,
	})
	if issues := CheckFormAdminOnly(ok); len(issues) != 0 {
		t.Fatalf("корректное объявление отклонено: %s", messages(issues))
	}

	tests := []struct {
		name    string
		element *metadata.FormElement
		want    string
	}{
		{"колонка табличной части", &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "КолонкаЦена",
			DataPath: "Объект.Строки.Цена", EditableAdminOnly: true,
		}, "data_path"},
		{"элемент без data_path", &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеБезПути", EditableAdminOnly: true,
		}, "data_path"},
		{"реквизит формы", &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеЗаметка",
			DataPath: "Форма.Заметка", EditableAdminOnly: true,
		}, "Объект.<Реквизит>"},
		{"не поле ввода", &metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнОК", EditableAdminOnly: true,
		}, "только у kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := CheckFormAdminOnly(formKeysProject(test.element))
			if len(issues) == 0 || !strings.Contains(messages(issues), test.want) {
				t.Fatalf("ожидалась ошибка про %q, получено: %s", test.want, messages(issues))
			}
			for _, issue := range issues {
				if issue.Code != "form.admin-only" {
					t.Fatalf("неожиданный код %q", issue.Code)
				}
			}
		})
	}
}

func TestCheckFormKeyPlacementWarnsAboutIgnoredKeys(t *testing.T) {
	collapsed := false
	tests := []struct {
		name    string
		element *metadata.FormElement
		want    string
	}{
		{"прокрутка вне горизонтальной группы", &metadata.FormElement{
			Kind: metadata.FormElementGroupBox, Name: "Группа", ScrollX: true,
		}, "orientation: horizontal"},
		{"акцент вне кнопки", &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеКомментарий", DataPath: "Объект.Комментарий", Primary: true,
		}, "только у kind"},
		{"ключи подбора у строкового поля", &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеКомментарий", DataPath: "Объект.Комментарий", ChoiceDropdown: &collapsed,
		}, "не выбирает ссылку"},
		{"группы у неиерархического справочника", &metadata.FormElement{
			Kind: metadata.FormElementField, Name: "ПолеФилиал", DataPath: "Объект.Филиал", ChoiceFolders: true,
		}, "нет иерархии"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			warns := CheckFormKeyPlacement(formKeysProject(test.element))
			if len(warns) == 0 || !strings.Contains(messages(warns), test.want) {
				t.Fatalf("ожидалось предупреждение про %q, получено: %s", test.want, messages(warns))
			}
		})
	}

	good := formKeysProject(
		&metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "Действия", Orientation: "horizontal", ScrollX: true},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнОК", Primary: true},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеГород", DataPath: "Объект.НаселённыйПункт", ChoiceFolders: true, ChoiceDropdown: &collapsed},
	)
	if warns := CheckFormKeyPlacement(good); len(warns) != 0 {
		t.Fatalf("корректные объявления получили предупреждение: %s", messages(warns))
	}
}

func TestRunFullRejectsChoiceFoldersWithoutStableID(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "адресныйклассификатор.yaml"), `name: АдресныйКлассификатор
hierarchical: true
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
  - {name: НаселённыйПункт, type: "reference:АдресныйКлассификатор"}
`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: Объекта
  kind: object
  entity: Заявка
elements:
  - kind: ПолеВвода
    name: ПолеГород
    data_path: Объект.НаселённыйПункт
    choice: true
    choice_folders: true
`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if result.OK || !strings.Contains(messages(result.Issues), "form.choice-folders") {
		t.Fatalf("check пропустил неработающий choice_folders без id: %+v", result)
	}
}

// Ключ, которого не знает линтер, на обязательном гейте CI становится ошибкой:
// конфигурация, использующая поддержанный ключ, обязана проходить свою же
// проверку.
func TestRunFullAcceptsFormKeysWithoutUnknownKeyWarning(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "адресныйклассификатор.yaml"), `name: АдресныйКлассификатор
hierarchical: true
fields:
  - {name: Наименование, type: string}
`)
	mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
  - {name: НаселённыйПункт, type: "reference:АдресныйКлассификатор"}
  - {name: ТипЗвонка, type: string}
`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: Объекта
  kind: object
  entity: Заявка
elements:
  - kind: ГруппаФормы
    name: Действия
    orientation: horizontal
    scroll_x: true
    children:
      - kind: Кнопка
        name: КнОК
        primary: true
  - id: city
    kind: ПолеВвода
    name: ПолеГород
    data_path: Объект.НаселённыйПункт
    choice: true
    choice_folders: true
    choice_dropdown: false
  - kind: ПолеВвода
    name: ПолеТипЗвонка
    data_path: Объект.ТипЗвонка
    editable_admin_only: true
`)
	result := RunFullWithOptions(dir, Options{Lint: true})
	if !result.OK {
		t.Fatalf("корректная конфигурация не прошла check: %+v", result.Issues)
	}
	for _, warning := range result.Warnings {
		if warning.Code != "metadata.unvalidated-key" {
			continue
		}
		for _, key := range []string{"scroll_x", "primary", "editable_admin_only", "choice_folders", "choice_dropdown"} {
			if strings.Contains(warning.Message, key) {
				t.Fatalf("поддержанный ключ объявлен неизвестным: %+v", warning)
			}
		}
	}
}
