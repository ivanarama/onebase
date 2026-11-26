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

// choice_free_input у ПолеСписка: строка ввода со своим списком вариантов
// (1С: СписокВыбора + РедактированиеТекста). Значение не из списка
// показывается как есть, вариант и вписанный текст одинаково приходят в
// ПриИзменении и записываются. Под readonly строка не правится, кнопка списка
// погашена.
func TestChoiceFreeInput_RenderEventAndSave(t *testing.T) {
	visitField := func(readOnly bool) *metadata.FormElement {
		return &metadata.FormElement{
			Kind: metadata.FormElementInputList, Name: "ПолеВремя", DataPath: "Объект.ВремяВизита",
			ChoiceFreeInput: true, ReadOnly: readOnly,
			Choices:  []metadata.FormChoice{{Value: "8-14"}, {Value: "14-20"}, {Value: "ЗМ в течение часа"}},
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ВремяПриИзменении"},
		}
	}
	newEntity := func(readOnly bool) *metadata.Entity {
		ent := &metadata.Entity{
			Name: "ЗаявкаВизит", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "ВремяВизита", Type: metadata.FieldTypeString},
			},
		}
		ent.Forms = []*metadata.FormModule{{
			Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged,
			Elements: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
				visitField(readOnly),
			},
			ProgramAST: mustParse(t, `
Процедура ВремяПриИзменении()
	Сообщить("время: " + Объект.ВремяВизита);
КонецПроцедуры
`),
		}}
		return ent
	}

	ent := newEntity(false)
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Заявка 1", "ВремяВизита": "к 11:30"}, ent); err != nil {
		t.Fatal(err)
	}

	// Открытие: значение не из списка — в строке как есть, варианты — в списке.
	rec := executeFormEditGET(t, srv, ent, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<input type="text" name="ВремяВизита" value="к 11:30" autocomplete="off" data-ob-free-choice-input`,
		`data-ob-fire-change="ПолеВремя"`,
		`<div class="ob-free-choice-opt" role="option" data-value="ЗМ в течение часа">ЗМ в течение часа</div>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("в форме нет %s", want)
		}
	}
	if strings.Contains(body, `<select name="ВремяВизита"`) || strings.Contains(body, "<datalist") {
		t.Fatal("поле со вводом не должно быть <select> или <datalist>")
	}

	// ПриИзменении: вписанный текст приходит в обработчик так же, как вариант.
	for _, value := range []string{"14-20", "после 18:00"} {
		resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
			"_id": {id.String()}, "_element": {"ПолеВремя"}, "_event": {string(metadata.FormEventOnChange)},
			"Наименование": {"Заявка 1"}, "ВремяВизита": {value},
		}).Body.Bytes())
		if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != "время: "+value {
			t.Fatalf("ПриИзменении с %q: ok=%v messages=%v error=%q", value, resp.OK, resp.Messages, resp.Error)
		}
	}

	// Запись: значение не из списка сохраняется и снова показывается как есть.
	save := httptest.NewRecorder()
	srv.submitEdit(save, reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), url.Values{
		"Наименование": {"Заявка 1"}, "ВремяВизита": {"после 18:00"},
	}, map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()}))
	if save.Code != http.StatusSeeOther {
		t.Fatalf("запись: %d %s", save.Code, save.Body.String())
	}
	row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if row["ВремяВизита"] != "после 18:00" {
		t.Fatalf("сохранено %v, ожидалось «после 18:00»", row["ВремяВизита"])
	}
	if again := executeFormEditGET(t, srv, ent, id).Body.String(); !strings.Contains(again, `name="ВремяВизита" value="после 18:00"`) {
		t.Fatal("после записи значение не показано как есть")
	}

	// readonly: строка только для чтения, кнопка списка погашена.
	roEnt := newEntity(true)
	roSrv, roCtx := newSubmitTestServer(t, []*metadata.Entity{roEnt})
	roID := uuid.New()
	if err := roSrv.store.Upsert(roCtx, roEnt.Name, roID, map[string]any{"Наименование": "Заявка 2", "ВремяВизита": "8-14"}, roEnt); err != nil {
		t.Fatal(err)
	}
	roBody := executeFormEditGET(t, roSrv, roEnt, roID).Body.String()
	if !strings.Contains(roBody, `value="8-14" autocomplete="off" data-ob-free-choice-input readonly`) ||
		!strings.Contains(roBody, `class="ob-free-choice-btn" tabindex="-1" aria-label="Выбрать из списка" title="Выбрать из списка" disabled`) {
		t.Fatal("под readonly строка должна быть readonly, а кнопка списка — disabled")
	}
}
