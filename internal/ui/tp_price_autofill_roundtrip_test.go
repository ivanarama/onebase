package ui

// Воспроизведение сценария «Заказ покупателя» (PuT): грид ТЧ Товары с
// обработчиком ПриИзменении, который заполняет пустые цены строк — как
// ЗаполнитьЦеныИСтавкиСтрок в форме заказа. Проверяется, что round-trip
// возвращает мутированные строки в tableparts (клиент применяет их к SlickGrid
// через applyTableParts), т.е. серверная половина автоподстановки цен работает.

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestTablePartOnChangeFillsPriceInResponse(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТабТоварыПриИзменении()
	Для Каждого Стр Из Объект.Товары Цикл
		Если Стр.Цена = 0 Тогда
			Стр.Цена = 42;
		КонецЕсли;
	КонецЦикла;
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{{
			Kind:     metadata.FormElementTablePart,
			Name:     "ТабТовары",
			DataPath: "Объект.Товары",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnChange: "ТабТоварыПриИзменении",
			},
		}})
	ent.TableParts = []metadata.TablePart{{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}}

	body := url.Values{}
	body.Set("_element", "ТабТовары")
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "0")
	body.Set("_tp_row_number", "1")
	body.Set("_tp_col", "Номенклатура")
	body.Set("_tp_col_index", "0")
	body.Set("tp_json.Товары", `[{"Номенклатура":"Товар А","Количество":5,"Цена":0}]`)

	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK || resp.Error != "" {
		t.Fatalf("ok=%v error=%q", resp.OK, resp.Error)
	}
	rows := resp.TableParts["Товары"]
	if len(rows) != 1 {
		t.Fatalf("tableparts=%#v, ожидалась 1 строка Товары", resp.TableParts)
	}
	got := fmt.Sprintf("%v", rows[0]["Цена"])
	if got != "42" && got != "42.0" {
		t.Fatalf("Цена в ответе = %q, ожидалось 42: серверная мутация строки потеряна (tableparts=%#v)", got, resp.TableParts)
	}
}
