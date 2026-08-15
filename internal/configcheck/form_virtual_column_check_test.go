package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// Виртуальная колонка живёт только на форме: неверный путь не ломает ни запись,
// ни схему — колонка просто не появится или окажется пустой. Поэтому ошибку
// обязан назвать check, иначе искать причину пришлось бы в браузере (#845, тот
// же вывод, что и в #830).
func virtualColumnProject(vcs ...metadata.FormVirtualColumn) *project.Project {
	client := &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Код", Type: metadata.FieldTypeString},
		},
	}
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Клиент", Type: metadata.FieldType("reference:Клиент"), RefEntity: "Клиент"},
				{Name: "Комментарий", Type: metadata.FieldTypeString},
			},
		}},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind: metadata.FormElementTablePart, Name: "Строки", DataPath: "Объект.Строки",
			VirtualColumns: vcs,
		}},
	}}
	return &project.Project{Entities: []*metadata.Entity{client, order}}
}

func TestCheckFormVirtualColumns(t *testing.T) {
	cases := []struct {
		name     string
		column   metadata.FormVirtualColumn
		contains string // пусто — объявление корректно
	}{
		{"корректная колонка", metadata.FormVirtualColumn{Name: "КодКлиента", DataPath: "Клиент.Код"}, ""},
		{"путь из одного сегмента", metadata.FormVirtualColumn{Name: "Код", DataPath: "Клиент"}, "двух сегментов"},
		{"путь из трёх сегментов", metadata.FormVirtualColumn{Name: "Код", DataPath: "Клиент.Менеджер.Код"}, "двух сегментов"},
		{"нет такого реквизита в ТЧ", metadata.FormVirtualColumn{Name: "Код", DataPath: "Покупатель.Код"}, "нет реквизита \"Покупатель\""},
		{"реквизит не ссылочный", metadata.FormVirtualColumn{Name: "Код", DataPath: "Комментарий.Код"}, "не ссылочный"},
		{"нет реквизита у цели", metadata.FormVirtualColumn{Name: "ИНН", DataPath: "Клиент.ИНН"}, "нет реквизита \"ИНН\""},
		{"имя совпадает с реквизитом ТЧ", metadata.FormVirtualColumn{Name: "Комментарий", DataPath: "Клиент.Код"}, "совпадает с реквизитом"},
		{"без имени", metadata.FormVirtualColumn{DataPath: "Клиент.Код"}, "без name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := CheckFormVirtualColumns(virtualColumnProject(c.column))
			if c.contains == "" {
				if len(issues) != 0 {
					t.Fatalf("корректное объявление отклонено: %+v", issues)
				}
				return
			}
			if len(issues) == 0 {
				t.Fatalf("объявление принято молча")
			}
			if !strings.Contains(issues[0].Message, c.contains) {
				t.Fatalf("сообщение %q не содержит %q", issues[0].Message, c.contains)
			}
		})
	}
}

// Дубль имени в одном элементе — тоже ошибка: какая из колонок победит,
// зависело бы от порядка обхода.
func TestCheckFormVirtualColumns_Дубль(t *testing.T) {
	issues := CheckFormVirtualColumns(virtualColumnProject(
		metadata.FormVirtualColumn{Name: "КодКлиента", DataPath: "Клиент.Код"},
		metadata.FormVirtualColumn{Name: "кодклиента", DataPath: "Клиент.Наименование"},
	))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "дважды") {
		t.Fatalf("повторное объявление принято: %+v", issues)
	}
}

// Ключ на элементе, который не является табличной частью сущности, — ошибка
// размещения, а не молчание: колонок там не появится вовсе.
func TestCheckFormVirtualColumns_НеТабличнаяЧасть(t *testing.T) {
	proj := virtualColumnProject()
	proj.Entities[1].Forms[0].Elements = []*metadata.FormElement{{
		Kind: metadata.FormElementField, Name: "Комментарий", DataPath: "Объект.Комментарий",
		VirtualColumns: []metadata.FormVirtualColumn{{Name: "КодКлиента", DataPath: "Клиент.Код"}},
	}}
	issues := CheckFormVirtualColumns(proj)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "не на табличной части") {
		t.Fatalf("ожидалась ошибка размещения, получено: %+v", issues)
	}
}
