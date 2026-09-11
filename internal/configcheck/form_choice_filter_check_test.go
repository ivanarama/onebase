package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func choiceFilterProject(filter map[string]string, dataPath string) *project.Project {
	contractor := &metadata.Entity{Name: "Контрагент", Kind: metadata.KindCatalog}
	contract := &metadata.Entity{
		Name: "Договор", Kind: metadata.KindCatalog, Owner: "Контрагент",
		Fields: []metadata.Field{
			{Name: metadata.StandardOwnerField, Type: "reference:Контрагент", RefEntity: "Контрагент"},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Контрагент", Type: "reference:Контрагент", RefEntity: "Контрагент"},
			{Name: "Договор", Type: "reference:Договор", RefEntity: "Договор"},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", LayoutKind: "managed",
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementField, Name: "ПолеДоговор",
			DataPath: dataPath, ChoiceFilter: filter,
		}},
	}}
	return &project.Project{Entities: []*metadata.Entity{contractor, contract, order}}
}

// Рабочая связь замечаний не даёт.
func TestChoiceFilterValid(t *testing.T) {
	issues := CheckFormChoiceFilter(choiceFilterProject(
		map[string]string{metadata.StandardOwnerField: "Объект.Контрагент"}, "Объект.Договор"))
	if len(issues) != 0 {
		t.Fatalf("ждали тишину, получили %+v", issues)
	}
}

// Опечатка в имени реквизита-приёмника не видна ничем: отбор просто не
// применяется, и подбор показывает весь справочник. Поэтому — ошибка сборки.
func TestChoiceFilterUnknownTargetField(t *testing.T) {
	issues := CheckFormChoiceFilter(choiceFilterProject(
		map[string]string{"Владлец": "Объект.Контрагент"}, "Объект.Договор"))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "Владлец") {
		t.Fatalf("ждали ошибку про неизвестный реквизит, получили %+v", issues)
	}
}

// Источник значения обязан существовать на форме: иначе отбор всегда пуст, а
// пользователь видит пустой подбор без объяснений.
func TestChoiceFilterUnknownSource(t *testing.T) {
	issues := CheckFormChoiceFilter(choiceFilterProject(
		map[string]string{metadata.StandardOwnerField: "Объект.Поставщик"}, "Объект.Договор"))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "Поставщик") {
		t.Fatalf("ждали ошибку про источник значения, получили %+v", issues)
	}
}

// Отбор на нессылочном поле бессмыслен.
func TestChoiceFilterOnNonReferenceField(t *testing.T) {
	issues := CheckFormChoiceFilter(choiceFilterProject(
		map[string]string{metadata.StandardOwnerField: "Объект.Контрагент"}, "Объект.Комментарий"))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "не ссылается на справочник") {
		t.Fatalf("ждали ошибку про нессылочное поле, получили %+v", issues)
	}
}
