package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Таблица значений как «выдача» (история клиента, результат поиска): rows
// видимых строк с закреплённой шапкой, число записей в заголовке, надпись
// пустой таблицы. Карточка открыта штатным обработчиком формы.
func TestManagedValueTableOutputKeysInFormResponse(t *testing.T) {
	table := &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТаблИстория", DataPath: "Форма.История",
		TitleMap: map[string]string{"ru": "Заявки клиента"},
		Rows:     5, ShowCount: true, EmptyText: "Заявок нет",
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementColumn, Name: "КолДата", DataPath: "Дата", ReadOnly: true},
			{Kind: metadata.FormElementColumn, Name: "КолНомер", DataPath: "Номер", ReadOnly: true},
		},
	}
	entity := layoutTestEntity(table)
	entity.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "История", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{
		{Name: "Дата", TypeRef: "string"}, {Name: "Номер", TypeRef: "string"}, {Name: "Ид", TypeRef: "string"},
	}}}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{entity})
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+entity.Name+"/new", nil, map[string]string{"entity": entity.Name})
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`Заявки клиента <span data-ob-vt-count="История">(0)</span>`,
		`<div class="managed-vt-rows" data-ob-vt-rows="5" style="height:204px">`,
		`data-vt-empty="Заявок нет"`,
		`<tr class="ob-vt-empty"><td colspan="2">Заявок нет</td></tr>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("нет %q в карточке:\n%s", want, body)
		}
	}
}

// Заполненная таблица: строки надписи нет, длинное значение нередактируемой
// ячейки целиком доступно подсказкой.
func TestValueTableOutputFilledRowsHaveTooltipAndNoEmptyRow(t *testing.T) {
	out := renderValueTableElement(t, []*metadata.FormElement{колонка("Представление", "Адрес", true)}, nil)
	if strings.Contains(out, "ob-vt-empty") {
		t.Fatalf("у заполненной таблицы не должно быть строки «нет данных»:\n%s", out)
	}
	if !strings.Contains(out, `value="г Москва, ул Ленина" readonly title="г Москва, ул Ленина"`) {
		t.Fatalf("нередактируемая ячейка без подсказки с полным текстом:\n%s", out)
	}
}

// rows у многострочного ПолеВвода — число видимых строк textarea (по умолчанию 5).
func TestManagedFormMultilineFieldRows(t *testing.T) {
	el := fieldEl("ПолеКомм", "Объект.Комментарий")
	el.Multiline, el.Rows = true, 2
	ent := layoutTestEntity(el)
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	if !strings.Contains(rec.Body.String(), `<textarea name="Комментарий" autocomplete="off" rows="2"`) {
		t.Fatalf("rows многострочного поля не применён:\n%s", rec.Body.String())
	}
}
