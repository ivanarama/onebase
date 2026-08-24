package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// Колонка табличной части (план 154) ничего не ломает, когда объявлена неверно:
// она молча не участвует в составе, а обработчик на ней молча не вызывается.
// Поэтому ошибку обязан назвать check — иначе форма выглядит настроенной, а
// причину пришлось бы искать в браузере (тот же вывод, что и в #845).
func tablePartColumnProject(table *metadata.FormElement) *project.Project {
	order := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name: "Строки",
			Fields: []metadata.Field{
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Цена", Type: metadata.FieldTypeNumber},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			},
		}},
	}
	order.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: order.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Elements:   []*metadata.FormElement{table},
	}}
	return &project.Project{Entities: []*metadata.Entity{order}}
}

func columnElement(name, dataPath string) *metadata.FormElement {
	return &metadata.FormElement{Kind: metadata.FormElementColumn, Name: name, DataPath: dataPath}
}

func tableWith(children ...*metadata.FormElement) *metadata.FormElement {
	return &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабСтроки", DataPath: "Объект.Строки",
		Children: children,
	}
}

func TestCheckFormTablePartColumns(t *testing.T) {
	withChange := func(el *metadata.FormElement) *metadata.FormElement {
		el.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ПриИзмененииКолонки"}
		return el
	}
	cases := []struct {
		name     string
		table    *metadata.FormElement
		contains string // пусто — объявление корректно
	}{
		{
			"состав по data_path",
			tableWith(columnElement("КолЦена", "Объект.Строки.Цена")),
			"",
		},
		{
			"состав по имени элемента",
			tableWith(columnElement("Цена", "")),
			"",
		},
		{
			"без колонок вовсе",
			tableWith(),
			"",
		},
		{
			"колонка не сопоставлена реквизиту",
			tableWith(columnElement("КолСкидка", "Объект.Строки.Скидка")),
			"не сопоставлена реквизиту",
		},
		{
			"два объявления одного реквизита",
			tableWith(columnElement("КолЦена", "Объект.Строки.Цена"), columnElement("ЦенаЕщёРаз", "Объект.Строки.Цена")),
			"ссылаются на один реквизит",
		},
		{
			"обработчик ПриИзменении на колонке",
			tableWith(withChange(columnElement("КолЦена", "Объект.Строки.Цена"))),
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := CheckFormTablePartColumns(tablePartColumnProject(c.table))
			if c.contains == "" {
				if len(issues) != 0 {
					t.Fatalf("корректное объявление отклонено: %+v", issues)
				}
				return
			}
			if len(issues) == 0 {
				t.Fatal("объявление принято молча")
			}
			if !strings.Contains(issues[0].Message, c.contains) {
				t.Fatalf("сообщение %q не содержит %q", issues[0].Message, c.contains)
			}
		})
	}
}

// Событие строки принадлежит таблице целиком: строка добавляется и удаляется
// не «в колонке». Рантайм такой запрос отклонит, поэтому обработчик был бы
// написан впустую.
func TestCheckFormTablePartColumns_СобытиеСтрокиНаКолонке(t *testing.T) {
	column := columnElement("КолЦена", "Объект.Строки.Цена")
	column.Handlers = map[metadata.FormEventType]string{
		metadata.FormEventOnRowAdded: "ПриДобавленииСтроки",
	}
	issues := CheckFormTablePartColumns(tablePartColumnProject(tableWith(column)))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "не отправляет") {
		t.Fatalf("событие строки на колонке принято: %+v", issues)
	}
}

// В простой таблице событий ячейки нет вовсе — ни у колонки, ни у самой ТЧ.
// Без этой проверки обработчик выглядел бы настроенным и никогда не срабатывал.
func TestCheckFormTablePartColumns_СобытиеКолонкиПриNoGrid(t *testing.T) {
	column := columnElement("КолЦена", "Объект.Строки.Цена")
	column.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ЦенаПриИзменении"}
	table := tableWith(column)
	table.NoGrid = true

	issues := CheckFormTablePartColumns(tablePartColumnProject(table))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "no_grid") {
		t.Fatalf("обработчик колонки при no_grid принят: %+v", issues)
	}
}

// Колонка вне табличной части невидима вдвойне: её нет ни на форме, ни в
// диагностике — состав колонок собирается только из детей ТЧ.
func TestCheckFormTablePartColumns_КолонкаВнеТабличнойЧасти(t *testing.T) {
	group := &metadata.FormElement{
		Kind: metadata.FormElementGroupBox, Name: "Группа",
		Children: []*metadata.FormElement{columnElement("КолЦена", "Объект.Строки.Цена")},
	}
	issues := CheckFormTablePartColumns(tablePartColumnProject(group))
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "вне табличной части") {
		t.Fatalf("колонка вне ТЧ принята: %+v", issues)
	}
}
