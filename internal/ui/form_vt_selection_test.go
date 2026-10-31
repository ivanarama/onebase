package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ValueTable как таблица ВЫБОРА, а не ввода.
//
// Формовая таблица-атрибут рисуется простой разметкой, и до этой правки она
// умела ровно одно — редактирование: показывала все объявленные колонки (то
// есть и служебный идентификатор строки), предлагала «+ Добавить строку» и
// крестики удаления, печатала свой заголовок вторым (первый печатается выше
// для любой табличной части) и не сообщала серверу о щелчке по строке —
// подписка на ПриАктивизацииСтроки есть только у SlickGrid.
//
// Между тем именно такой таблицей показывают результат поиска: строки кладёт
// обработчик, пользователь выбирает щелчком, править ячейки незачем.
func renderValueTableElement(t *testing.T, columns []*metadata.FormElement, handlers map[metadata.FormEventType]string) string {
	t.Helper()
	table := &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТаблицаРезультатов",
		DataPath: "Форма.Результаты", TitleMap: map[string]string{"ru": "Результат поиска"},
		Children: columns, Handlers: handlers,
	}
	form := &metadata.FormModule{
		Elements: []*metadata.FormElement{table},
		Attributes: []*metadata.FormAttribute{{
			Name: "Результаты", TypeRef: "ValueTable",
			Columns: []*metadata.FormAttributeColumn{
				{Name: "Улица", TypeRef: "string"},
				{Name: "Представление", TypeRef: "string"},
				{Name: "Ид", TypeRef: "string"},
			},
		}},
	}
	ctx := map[string]any{
		"Entity": &metadata.Entity{}, "Form": form, "CanWrite": true,
		"TablePartRows": map[string][]map[string]any{
			"Результаты": {{"Улица": "ул Ленина", "Представление": "г Москва, ул Ленина", "Ид": "uuid-1"}},
		},
		"TPRefOptions": map[string]any{},
		"TPEnumLabels": map[string]map[string]map[string]string{},
	}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "managed-element", map[string]any{"El": table, "Ctx": ctx}); err != nil {
		t.Fatalf("отрисовка ValueTable: %v", err)
	}
	return out.String()
}

func колонка(имя, подпись string, readOnly bool) *metadata.FormElement {
	return &metadata.FormElement{
		Kind: metadata.FormElementColumn, Name: "Кол" + имя, DataPath: имя,
		TitleMap: map[string]string{"ru": подпись}, ReadOnly: readOnly,
	}
}

func TestValueTableShowsOnlyDeclaredColumns(t *testing.T) {
	html := renderValueTableElement(t, []*metadata.FormElement{
		колонка("Улица", "Улица", true),
		колонка("Представление", "Представление", true),
	}, nil)

	if strings.Contains(html, "<th>Ид</th>") {
		t.Errorf("необъявленная колонка показана в шапке: %s", html)
	}
	// Значение всё равно должно доехать до сервера — иначе обработчик не узнает,
	// какую запись выбрали.
	if !strings.Contains(html, `name="vt.Результаты.0.Ид"`) {
		t.Errorf("скрытая колонка выпала из разметки, значение не вернётся: %s", html)
	}
	for _, подпись := range []string{"<th>Улица</th>", "<th>Представление</th>"} {
		if !strings.Contains(html, подпись) {
			t.Errorf("объявленная колонка потеряна (%s): %s", подпись, html)
		}
	}
}

func TestValueTableReadOnlyColumnsHideRowEditing(t *testing.T) {
	html := renderValueTableElement(t, []*metadata.FormElement{
		колонка("Улица", "Улица", true),
		колонка("Представление", "Представление", true),
	}, nil)

	if strings.Contains(html, "+ Добавить строку") {
		t.Errorf("таблица только для чтения предлагает добавление строк: %s", html)
	}
	if strings.Contains(html, "data-ob-remove-row") {
		t.Errorf("таблица только для чтения предлагает удаление строк: %s", html)
	}
	if strings.Count(html, "Результат поиска") != 1 {
		t.Errorf("заголовок напечатан %d раз, ожидался один: %s", strings.Count(html, "Результат поиска"), html)
	}
}

// План колонок обязан доехать до клиента: строки после события перерисовывает
// он, и без плана рисовал их «как умеет» — всеми колонками, включая служебные,
// и без признака строки, то есть щелчок переставал работать после первого же
// поиска.
func TestValueTableSendsColumnPlanToClient(t *testing.T) {
	html := renderValueTableElement(t,
		[]*metadata.FormElement{колонка("Улица", "Улица", true), колонка("Представление", "Представление", true)},
		map[metadata.FormEventType]string{metadata.FormEventOnRowActivated: "СтрокаВыбрана"})

	for _, маркер := range []string{
		`data-vt-editable="0"`,
		`data-vt-flags="Улица:r,Представление:r,Ид:rh"`,
	} {
		if !strings.Contains(html, маркер) {
			t.Errorf("клиент не получит план колонок, нет %s: %s", маркер, html)
		}
	}
}

func TestValueTableEditableKeepsRowEditing(t *testing.T) {
	// Редактируемая колонка — прежнее поведение: таблицу можно пополнять руками.
	html := renderValueTableElement(t, []*metadata.FormElement{
		колонка("Улица", "Улица", false),
	}, nil)
	if !strings.Contains(html, "+ Добавить строку") {
		t.Errorf("редактируемая таблица лишилась добавления строк: %s", html)
	}
}

func TestValueTableRowActivationWiredWhenHandlerDeclared(t *testing.T) {
	без := renderValueTableElement(t, []*metadata.FormElement{колонка("Улица", "Улица", true)}, nil)
	if strings.Contains(без, "data-ob-vt-activate") {
		t.Errorf("подписка на щелчок навязана таблице без обработчика: %s", без)
	}

	с := renderValueTableElement(t,
		[]*metadata.FormElement{колонка("Улица", "Улица", true)},
		map[metadata.FormEventType]string{metadata.FormEventOnRowActivated: "СтрокаВыбрана"})
	for _, маркер := range []string{`data-ob-vt-activate="ТаблицаРезультатов"`, `data-ob-vt-name="Результаты"`, `data-ob-vt-row="0"`} {
		if !strings.Contains(с, маркер) {
			t.Errorf("щелчок по строке не подключён, нет %s: %s", маркер, с)
		}
	}
}
