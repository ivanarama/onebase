package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ПриСозданииНаСервере у открытой записи видит её ссылку: Объект.Ссылка
// заполнена и разыменовывается. Конфигурация колл-центра обходила пустую
// ссылку поиском по номеру. У нового объекта ссылка пуста, как в 1С.
func TestFormCreateHook_ObjectRefOfOpenedRecord(t *testing.T) {
	ent := &metadata.Entity{
		Name: "ЗвонокСсылка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	ent.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "Метка", TypeRef: "string"}},
		Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnCreateAtServer: "ПриСоздании"},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementField, Name: "ПолеМетка", DataPath: "Метка"},
			{Kind: metadata.FormElementButton, Name: "КнопкаСсылка",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КнопкаСсылкаНажатие"}},
		},
		ProgramAST: mustParse(t, `
Процедура ПриСоздании()
	Если ЗначениеЗаполнено(Объект.Ссылка) Тогда
		Объект.Метка = "ссылка:" + Объект.Ссылка.Наименование;
	Иначе
		Объект.Метка = "ссылка пуста";
	КонецЕсли;
КонецПроцедуры

Процедура КнопкаСсылкаНажатие()
	Объект.Метка = "событие:" + Объект.Ссылка.Наименование;
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Звонок 42"}, ent); err != nil {
		t.Fatal(err)
	}
	rec := executeFormEditGET(t, srv, ent, id)
	if rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `value="ссылка:Звонок 42"`) {
		t.Fatalf("ПриСозданииНаСервере открытой записи не видит Объект.Ссылка: %s", formFieldSnippet(body, "ПолеМетка"))
	}
	// Событие формы той же записи видит ту же ссылку с подписью.
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
		"_id": {id.String()}, "_element": {"КнопкаСсылка"}, "_event": {string(metadata.FormEventOnClick)},
		"Наименование": {"Звонок 42"},
	}).Body.Bytes())
	if !resp.OK || toString(resp.Values["Метка"]) != "событие:Звонок 42" {
		t.Fatalf("событие формы: Объект.Ссылка.Наименование = %q (%s)", toString(resp.Values["Метка"]), resp.Error)
	}
	newRec := httptest.NewRecorder()
	srv.form(newRec, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name}))
	if newRec.Code != 200 {
		t.Fatalf("GET new: %d %s", newRec.Code, newRec.Body.String())
	}
	if body := newRec.Body.String(); !strings.Contains(body, `value="ссылка пуста"`) {
		t.Fatalf("у нового объекта ссылка должна быть пуста: %s", formFieldSnippet(body, "ПолеМетка"))
	}
}

func formFieldSnippet(body, el string) string {
	i := strings.Index(body, `data-ob-el="`+el+`"`)
	if i < 0 {
		return "(нет элемента " + el + ")"
	}
	end := i + 600
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
