package ui

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
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

// The initial HTML uses the same successful-controls rules as native FormData.
// Feed that payload through the public row event, including a readonly duplicate.
func TestValueTableInitialReadonlyColumnsRoundTrip(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			table := &metadata.FormElement{
				Kind: metadata.FormElementTablePart, Name: "History", DataPath: "Form.Results",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnRowActivated: "Selected"},
				Children: []*metadata.FormElement{
					{Kind: metadata.FormElementColumn, Name: "NameColumn", DataPath: "Name", ReadOnly: true},
					{Kind: metadata.FormElementColumn, Name: "FlagColumn", DataPath: "Flag", ReadOnly: true},
					{Kind: metadata.FormElementColumn, Name: "AmountColumn", DataPath: "Amount", ReadOnly: true},
				},
			}
			duplicate := &metadata.FormElement{
				Kind: metadata.FormElementTablePart, Name: "ReadonlyHistory", DataPath: "Form.Results", ReadOnly: true,
				Children: table.Children,
			}
			srv, ent := setupManagedEventsServer(t, `
Процедура Selected()
 Если ТекущаяСтрока.Flag И ТекущаяСтрока.HiddenFlag Тогда
  Сообщить("kept-true");
 ИначеЕсли НЕ ТекущаяСтрока.Flag И НЕ ТекущаяСтрока.HiddenFlag Тогда
  Сообщить("kept-false");
 Иначе
  ВызватьИсключение("bool payload lost");
 КонецЕсли;
КонецПроцедуры`, nil, []*metadata.FormElement{table, duplicate})
			form := ent.Forms[0]
			form.Attributes = []*metadata.FormAttribute{{Name: "Results", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{
				{Name: "Name", TypeRef: "string"}, {Name: "Flag", TypeRef: "bool"},
				{Name: "HiddenFlag", TypeRef: "bool"}, {Name: "Amount", TypeRef: "number"},
			}}}
			ctx := map[string]any{
				"Entity": ent, "Form": form, "CanWrite": true,
				"TablePartRows": map[string][]map[string]any{"Results": {{"Name": "selected row", "Flag": flag, "HiddenFlag": flag, "Amount": 7}}},
				"TPRefOptions":  map[string]any{}, "TPEnumLabels": map[string]map[string]map[string]string{},
			}
			var out bytes.Buffer
			for _, element := range form.Elements {
				if err := tmpl.ExecuteTemplate(&out, "managed-element", map[string]any{"El": element, "Ctx": ctx}); err != nil {
					t.Fatal(err)
				}
			}
			doc, err := html.Parse(strings.NewReader(out.String()))
			if err != nil {
				t.Fatal(err)
			}
			body := url.Values{}
			var walk func(*html.Node)
			walk = func(node *html.Node) {
				if node.Type == html.ElementNode && node.Data == "input" {
					name, _ := managedHTMLAttr(node, "name")
					typ, _ := managedHTMLAttr(node, "type")
					_, disabled := managedHTMLAttr(node, "disabled")
					_, checked := managedHTMLAttr(node, "checked")
					if name != "" && !disabled && (typ != "checkbox" || checked) {
						value, _ := managedHTMLAttr(node, "value")
						body.Add(name, value)
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(doc)
			for key, want := range map[string]string{"Name": "selected row", "Flag": fmt.Sprint(flag), "HiddenFlag": fmt.Sprint(flag), "Amount": "7"} {
				got := body["vt.Results.0."+key]
				if len(got) != 1 || got[0] != want {
					t.Fatalf("%s payload=%v, want one %q", key, got, want)
				}
			}
			body.Set("_element", "History")
			body.Set("_event", string(metadata.FormEventOnRowActivated))
			body.Set("_kind", "object")
			body.Set("_tp", "Results")
			body.Set("_tp_row", "0")
			resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
			if !resp.OK || strings.Join(resp.Messages, ",") != "kept-"+fmt.Sprint(flag) {
				t.Fatalf("initial rendered payload failed row event: %#v", resp)
			}
		})
	}
}
