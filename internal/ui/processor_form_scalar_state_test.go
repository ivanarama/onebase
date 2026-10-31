package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

func TestProcessorFormScalarState(t *testing.T) {
	for _, prefix := range []string{"Форма.", "Объект.", ""} {
		t.Run(prefix, func(t *testing.T) {
			program := mustParse(t, `
Процедура Запустить()
 Объект.Задача = "J-42";
 Объект.Сумма = 12.5;
 Объект.Флаг = Истина;
 Объект.Период = Дата(2026, 10, 25, 12, 34, 56);
КонецПроцедуры
Процедура Проверить()
 Сообщить("Задача=[" + Строка(Объект.Задача) + "] голое=[" + Строка(Задача) + "]");
 Если Объект.Сумма + 1 <> 13.5 Тогда ВызватьИсключение("число потеряно"); КонецЕсли;
 Если НЕ Объект.Флаг Тогда ВызватьИсключение("bool потерян"); КонецЕсли;
 Если Объект.Период <> Дата(2026, 10, 25, 12, 34, 56) Тогда ВызватьИсключение("дата потеряна"); КонецЕсли;
КонецПроцедуры
`)
			form := processorExecutionForm(
				&metadata.FormElement{Kind: metadata.FormElementField, Name: "ЗадачаПоле", DataPath: prefix + "Задача"},
				&metadata.FormElement{Kind: metadata.FormElementField, Name: "СуммаПоле", DataPath: prefix + "Сумма"},
				&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "ФлагПоле", DataPath: prefix + "Флаг"},
				&metadata.FormElement{Kind: metadata.FormElementDatePicker, Name: "ПериодПоле", DataPath: prefix + "Период"},
				&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Запустить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Запустить"}},
				&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Проверить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
			)
			form.Attributes = []*metadata.FormAttribute{
				{Name: "Задача", TypeRef: "string"}, {Name: "Сумма", TypeRef: "number(15,2)"},
				{Name: "Флаг", TypeRef: "bool"}, {Name: "Период", TypeRef: "date"},
			}
			form.ProgramAST = program
			proc := &processor.Processor{Name: "СостояниеОбработки", Forms: []*metadata.FormModule{form}}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
			router := chi.NewRouter()
			srv.Mount(router)
			fire := func(button string, values url.Values) formEventResponse {
				t.Helper()
				values.Set("_element", button)
				values.Set("_event", string(metadata.FormEventOnClick))
				req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-event", strings.NewReader(values.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if !resp.OK {
					t.Fatalf("event failed: %+v", resp)
				}
				return resp
			}
			first := fire("Запустить", url.Values{})
			if first.Values["Задача"] != "J-42" {
				t.Fatalf("first response: %+v", first)
			}
			// These are the controls carried by the browser after applying the first response.
			second := fire("Проверить", url.Values{"Задача": {first.Values["Задача"].(string)}, "Сумма": {"12,5"}, "Флаг": {"true"}, "Период": {"2026-10-25T12:34:56"}})
			if len(second.Messages) != 1 || second.Messages[0] != "Задача=[J-42] голое=[J-42]" {
				t.Fatalf("state lost: %+v", second)
			}
		})
	}
}

func TestProcessorFormScalarStateInputBoundary(t *testing.T) {
	program := mustParse(t, `
Процедура Проверить()
 Сообщить("[" + Строка(Объект.Текст) + "]");
 Сообщить(Объект.Параметр);
 Если Объект.Параметр + 1 <> 8 Тогда ВызватьИсключение("тип параметра изменился"); КонецЕсли;
 Если Объект.Строки.Количество() <> 0 Тогда ВызватьИсключение("таблица принята как скаляр"); КонецЕсли;
 Если ЗначениеЗаполнено(Объект.ТолькоЧтение) Или ЗначениеЗаполнено(Объект.ГруппаТолькоЧтение)
  Или ЗначениеЗаполнено(Объект.Неразмещённый) Или ЗначениеЗаполнено(Объект.Надпись)
  Или ЗначениеЗаполнено(Объект.Вложенный) Или ЗначениеЗаполнено(Объект.ВнутриТаблицы)
  Или ЗначениеЗаполнено(Объект.Основной) Или ЗначениеЗаполнено(Объект.id)
  Или ЗначениеЗаполнено(Объект._event) Или ЗначениеЗаполнено(Объект.Необъявленный) Тогда
  ВызватьИсключение("принято запрещённое поле");
 КонецЕсли;
 Если Объект.Флаг Тогда ВызватьИсключение("ложь потеряна"); КонецЕсли;
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ТекстПоле", DataPath: "Форма.Текст"},
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "ФлагПоле", DataPath: "Форма.Флаг"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПараметрПоле", DataPath: "Объект.Параметр"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ЧтениеПоле", DataPath: "Форма.ТолькоЧтение", ReadOnly: true},
		&metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "ЧтениеГруппа", ReadOnly: true, Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ВГруппе", DataPath: "ГруппаТолькоЧтение"}}},
		&metadata.FormElement{Kind: metadata.FormElementLabel, Name: "НадписьПоле", DataPath: "Надпись"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ВложенныйПоле", DataPath: "Объект.Вложенный.Свойство"},
		&metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "Таблица", DataPath: "Форма.Строки", Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ВТаблице", DataPath: "ВнутриТаблицы"}}},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ОсновнойПоле", DataPath: "Основной"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "IDПоле", DataPath: "id"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "СлужебноеПоле", DataPath: "_event"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Проверить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Проверить"}},
	)
	for _, name := range []string{"Текст", "Параметр", "ТолькоЧтение", "ГруппаТолькоЧтение", "Неразмещённый", "Надпись", "Вложенный", "ВнутриТаблицы", "id", "_event"} {
		form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: name, TypeRef: "string"})
	}
	form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: "Флаг", TypeRef: "bool"}, &metadata.FormAttribute{Name: "Основной", TypeRef: "string", MainAttribute: true}, &metadata.FormAttribute{Name: "Строки", TypeRef: "ValueTable", Columns: []*metadata.FormAttributeColumn{{Name: "Текст", TypeRef: "string"}}})
	form.ProgramAST = program
	proc := &processor.Processor{Name: "ГраницаСостояния", Params: []processor.Param{{Name: "Параметр", Type: "number"}}, Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	router := chi.NewRouter()
	srv.Mount(router)
	for _, tc := range []struct {
		name   string
		posted bool
		text   string
	}{{"body", true, "  значимый текст  "}, {"empty", true, ""}, {"query only", false, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			body := processorClickBody("Проверить")
			body.Set("Параметр", "7")
			body.Set("Флаг", "false")
			body.Set("Строки", "forged scalar table")
			for _, name := range []string{"ТолькоЧтение", "ГруппаТолькоЧтение", "Неразмещённый", "Надпись", "Вложенный", "ВнутриТаблицы", "Основной", "id", "Необъявленный"} {
				body.Set(name, "injected")
			}
			if tc.posted {
				body.Set("Текст", tc.text)
			}
			req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-event?"+url.Values{"Текст": {"query injection"}, "Параметр": {"999"}}.Encode(), strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if !resp.OK || len(resp.Messages) != 2 || resp.Messages[0] != "["+tc.text+"]" || resp.Messages[1] != "7" {
				t.Fatalf("input boundary: %+v", resp)
			}
		})
	}
}
