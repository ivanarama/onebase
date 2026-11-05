package ui

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Карточка задачи без исполнителя пуста до ответа на вопрос «Установить
// исполнителем?»: флаг ставит ПриСозданииНаСервере до отрисовки, группа с
// описанием скрыта (секреты закрывает conceal, а не hidden_when); ответ «Да»
// показывает группу с описанием.
const hiddenUntilPhone = "8-900-111-22-33"

func hiddenUntilEntity(t *testing.T) *metadata.Entity {
	t.Helper()
	ent := &metadata.Entity{
		Name: "ЗадачаКЦ", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Описание", Type: metadata.FieldTypeString},
			{Name: "Исполнитель", Type: metadata.FieldTypeString},
		},
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
		LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "СкрытьДоОтвета", TypeRef: "bool", Save: false}},
		Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnCreateAtServer: "ПриСозданииНаСервереФормы"},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "НаимПриИзменении"}},
			{Kind: metadata.FormElementButton, Name: "КнопкаДа",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КнопкаДаНажатие"}},
			{Kind: metadata.FormElementGroupBox, Name: "ГруппаСодержимое", HiddenWhen: "СкрытьДоОтвета",
				Children: []*metadata.FormElement{
					{Kind: metadata.FormElementField, Name: "ПолеОписание", DataPath: "Объект.Описание"},
				}},
		},
		ProgramAST: mustParse(t, `
Процедура ПриСозданииНаСервереФормы()
	Объект.СкрытьДоОтвета = НЕ ЗначениеЗаполнено(Объект.Исполнитель);
КонецПроцедуры

Процедура НаимПриИзменении()
КонецПроцедуры

Процедура КнопкаДаНажатие()
	Объект.Исполнитель = "Оператор";
	Объект.СкрытьДоОтвета = Ложь;
КонецПроцедуры
`),
	}
	ent.Forms = []*metadata.FormModule{form}
	return ent
}

var hiddenGroupTag = regexp.MustCompile(`<fieldset[^>]*data-ob-el="ГруппаСодержимое"[^>]*>`)

func TestHiddenUntilAnswer_FirstRenderWithoutContent(t *testing.T) {
	ent := hiddenUntilEntity(t)
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	for _, tc := range []struct {
		executor   string
		wantHidden bool
	}{{"", true}, {"Иванов", false}} {
		id := uuid.New()
		if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
			"Наименование": "Запрос с сайта", "Описание": "Клиент, тел " + hiddenUntilPhone, "Исполнитель": tc.executor,
		}, ent); err != nil {
			t.Fatal(err)
		}
		rec := executeFormEditGET(t, srv, ent, id)
		if rec.Code != 200 {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		tag := hiddenGroupTag.FindString(body)
		if tag == "" {
			t.Fatalf("группа с hidden_when должна быть в разметке:\n%s", body)
		}
		hiddenNow := strings.Contains(tag, " disabled") && strings.Contains(tag, "display:none")
		if hiddenNow != tc.wantHidden || !strings.Contains(tag, `data-ob-hidden-group="1"`) {
			t.Fatalf("исполнитель %q: группа скрыта=%v, ожидалось %v: %s", tc.executor, hiddenNow, tc.wantHidden, tag)
		}
	}
}

func TestHiddenUntilAnswer_EventValuesAndShowAfterYes(t *testing.T) {
	ent := hiddenUntilEntity(t)
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
		"Наименование": "Запрос с сайта", "Описание": "Клиент, тел " + hiddenUntilPhone,
	}, ent); err != nil {
		t.Fatal(err)
	}
	event := func(element, name string) formEventResponse {
		t.Helper()
		// Поля скрытой группы браузер не отправляет (disabled fieldset).
		resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
			"_id": {id.String()}, "_element": {element}, "_event": {name},
			"Наименование": {"Запрос с сайта"}, "СкрытьДоОтвета": {"true"},
		}).Body.Bytes())
		if !resp.OK {
			t.Fatalf("%s/%s: %s", element, name, resp.Error)
		}
		return resp
	}

	resp := event("ПолеНаим", string(metadata.FormEventOnChange))
	if resp.ElementStates == nil || !resp.ElementStates.Hidden["2"] {
		t.Fatalf("группа должна оставаться скрытой: %+v", resp.ElementStates)
	}

	// «Да»: группа показана, описание приходит из записи.
	resp = event("КнопкаДа", string(metadata.FormEventOnClick))
	if resp.ElementStates == nil || resp.ElementStates.Hidden["2"] {
		t.Fatalf("после «Да» группа должна показаться: %+v", resp.ElementStates)
	}
	if got := toString(resp.Values["Описание"]); !strings.Contains(got, hiddenUntilPhone) {
		t.Fatalf("после «Да» описание должно прийти значением, получено %q (values %v)", got, resp.Values)
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// Служебное поле, скрытое константой (hidden_when: "Истина"), держит состояние
// формы между событиями: его значение ходит, как до скрытия «до ответа» (#2517,
// регресс подбора заказа мастера — терялось направление).
func TestHiddenUntilAnswer_ConstantHiddenServiceFieldKeepsValue(t *testing.T) {
	for _, expr := range []string{"Истина", " true "} {
		ent := &metadata.Entity{Name: "ЗаказМастераКЦ", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		ent.Forms = []*metadata.FormModule{{
			Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged,
			Attributes: []*metadata.FormAttribute{{Name: "ПодборНаправление", TypeRef: "string", Save: false}},
			Elements: []*metadata.FormElement{
				{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
				{Kind: metadata.FormElementField, Name: "ПолеНаправление", DataPath: "ПодборНаправление", HiddenWhen: expr},
				{Kind: metadata.FormElementButton, Name: "КнопкаЗапомнить",
					Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Запомнить"}},
				{Kind: metadata.FormElementButton, Name: "КнопкаПрочитать",
					Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Прочитать"}},
			},
			ProgramAST: mustParse(t, `
Процедура Запомнить()
	Объект.ПодборНаправление = "Холодильники";
КонецПроцедуры

Процедура Прочитать()
	Объект.Наименование = "направление: " + Объект.ПодборНаправление;
КонецПроцедуры
`),
		}}
		srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
		resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
			"_element": {"КнопкаЗапомнить"}, "_event": {string(metadata.FormEventOnClick)},
		}).Body.Bytes())
		if !resp.OK || toString(resp.Values["ПодборНаправление"]) != "Холодильники" {
			t.Fatalf("hidden_when %q: значение служебного поля не пришло в values: %+v", expr, resp.Values)
		}
		resp = decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
			"_element": {"КнопкаПрочитать"}, "_event": {string(metadata.FormEventOnClick)},
			"ПодборНаправление": {toString(resp.Values["ПодборНаправление"])},
		}).Body.Bytes())
		if !resp.OK || toString(resp.Values["Наименование"]) != "направление: Холодильники" {
			t.Fatalf("hidden_when %q: следующее событие не получило значение: %+v", expr, resp.Values)
		}
	}
}

// Значение, которое код поставил в поле, скрытое условием (и в поле группы,
// скрытой при открытии), приходит в values и записывается — как до #2517
// (регресс: «Доп. информация» дисконтной карты терялась).
func TestHiddenUntilAnswer_ConditionHiddenValuesTravelAndSave(t *testing.T) {
	ent := &metadata.Entity{Name: "ЗвонокКЦ", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "ДопИнфо", Type: metadata.FieldTypeString},
		{Name: "Серия", Type: metadata.FieldTypeString},
	}}
	ent.Forms = []*metadata.FormModule{{
		Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged,
		Attributes: []*metadata.FormAttribute{{Name: "СкрытьДоп", TypeRef: "bool", Save: false}},
		Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnCreateAtServer: "ПриСозданииНаСервереФормы"},
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
			{Kind: metadata.FormElementField, Name: "ПолеДопИнфо", DataPath: "Объект.ДопИнфо", HiddenWhen: "СкрытьДоп"},
			{Kind: metadata.FormElementGroupBox, Name: "ГруппаСерия", HiddenWhen: "СкрытьДоп",
				Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ПолеСерия", DataPath: "Объект.Серия"}}},
			{Kind: metadata.FormElementButton, Name: "КнопкаКарта",
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КартаНажатие"}},
		},
		ProgramAST: mustParse(t, `
Процедура ПриСозданииНаСервереФормы()
	Объект.СкрытьДоп = Истина;
КонецПроцедуры

Процедура КартаНажатие()
	Объект.ДопИнфо = "Дисконтная карта, серия ДК-5";
	Объект.Серия = "ДК-5";
КонецПроцедуры
`),
	}}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": "Звонок"}, ent); err != nil {
		t.Fatal(err)
	}
	if rec := executeFormEditGET(t, srv, ent, id); rec.Code != 200 || !hiddenGroupDisabled(rec.Body.String(), "ГруппаСерия") {
		t.Fatalf("группа должна быть скрыта при открытии: %d", rec.Code)
	}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, url.Values{
		"_id": {id.String()}, "_element": {"КнопкаКарта"}, "_event": {string(metadata.FormEventOnClick)},
		"Наименование": {"Звонок"}, "СкрытьДоп": {"true"},
	}).Body.Bytes())
	if !resp.OK || toString(resp.Values["ДопИнфо"]) != "Дисконтная карта, серия ДК-5" || toString(resp.Values["Серия"]) != "ДК-5" {
		t.Fatalf("значения скрытых полей должны прийти в values: %+v", resp.Values)
	}
	// Запись формы с этими значениями сохраняет их.
	записатьЗаявку(t, srv, ent, id, url.Values{
		"Наименование": {"Звонок"}, "ДопИнфо": {toString(resp.Values["ДопИнфо"])}, "Серия": {toString(resp.Values["Серия"])},
		"СкрытьДоп": {"true"}, "_version": {"1"},
	})
	row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if toString(row["ДопИнфо"]) != "Дисконтная карта, серия ДК-5" || toString(row["Серия"]) != "ДК-5" {
		t.Fatalf("после записи: ДопИнфо=%v Серия=%v", row["ДопИнфо"], row["Серия"])
	}
}

func hiddenGroupDisabled(body, name string) bool {
	m := regexp.MustCompile(`<fieldset[^>]*data-ob-el="` + name + `"[^>]*>`).FindString(body)
	return strings.Contains(m, " disabled") && strings.Contains(m, "display:none")
}
