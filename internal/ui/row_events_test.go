package ui

// Направление 3 (Фаза B): события строк табличной части
// ПриДобавленииСтроки/ПриУдаленииСтроки. Бэкенд диспетчеризует их тем же
// generic-маршрутом, что и ПриИзменении/Нажатие; фронтенд (SlickGrid) дёргает их
// после добавления/удаления строки только при объявленном обработчике
// (data-sg-rowadd/data-sg-rowdel).

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Диспетчер исполняет обработчик ПриДобавленииСтроки, объявленный на элементе ТЧ.
func TestHandleManagedFormEvent_RowAddedFires(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПриДобавленииСтроки()
	Сообщить("строка добавлена");
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "Товары",
				DataPath: "Объект.Товары",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnRowAdded: "ТоварыПриДобавленииСтроки",
				},
			},
		})
	ent.TableParts = []metadata.TablePart{{Name: "Товары"}}

	body := url.Values{}
	body.Set("_element", "Товары")
	body.Set("_event", string(metadata.FormEventOnRowAdded))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ожидался ok=true, error=%q", resp.Error)
	}
	if len(resp.Messages) != 1 || !strings.Contains(resp.Messages[0], "добавлена") {
		t.Errorf("messages=%v, ждали сообщение о добавлении строки", resp.Messages)
	}
}

// ПриИзменении табличной части получает контекст изменённой ячейки: имя ТЧ,
// номер строки, колонку и текущую строку как DSL-объект (#205).
func TestHandleManagedFormEvent_TablePartChangeContext(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ТоварыПриИзменении()
	Сообщить(ИмяТабличнойЧасти);
	Сообщить(ТекущаяКолонка);
	Сообщить(НомерСтроки);
	Сообщить(ТекущаяСтрока.Цена);
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				DataPath: "Объект.Товары",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnChange: "ТоварыПриИзменении",
				},
			},
		})
	ent.TableParts = []metadata.TablePart{{
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Цена", Type: metadata.FieldTypeNumber},
		},
	}}

	body := url.Values{}
	body.Set("_element", "ЭлементТовары")
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_kind", "object")
	body.Set("_tp", "Товары")
	body.Set("_tp_row", "1")
	body.Set("_tp_row_number", "2")
	body.Set("_tp_col", "Цена")
	body.Set("_tp_col_index", "1")
	body.Set("tp_json.Товары", `[{"Количество":1,"Цена":10},{"Количество":2,"Цена":20}]`)

	rec := executeFormEvent(t, srv, ent, body)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ожидался ok=true, error=%q", resp.Error)
	}
	want := []string{"Товары", "Цена", "2", "20"}
	if len(resp.Messages) != len(want) {
		t.Fatalf("messages=%v, ожидалось %v", resp.Messages, want)
	}
	for i := range want {
		if resp.Messages[i] != want[i] {
			t.Errorf("messages[%d]=%q, ожидалось %q (все messages=%v)", i, resp.Messages[i], want[i], resp.Messages)
		}
	}
}

func TestHandleManagedFormEventRejectsForgedTablePartContext(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура КнопкаНажатие()
	Сообщить("button");
КонецПроцедуры

Процедура ТоварыПриИзменении()
	Сообщить(ИмяТабличнойЧасти);
КонецПроцедуры

Процедура УслугиПриИзменении()
	Сообщить(ИмяТабличнойЧасти);
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementButton, Name: "Кнопка", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КнопкаНажатие"}},
		{Kind: metadata.FormElementTablePart, Name: "ЭлементТовары", DataPath: "Объект.Товары", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "ТоварыПриИзменении"}},
		{Kind: metadata.FormElementTablePart, Name: "ЭлементУслуги", DataPath: "Объект.Услуги", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "УслугиПриИзменении"}},
	})
	ent.TableParts = []metadata.TablePart{
		{Name: "Товары", Fields: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}}},
		{Name: "Услуги", Fields: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}}},
	}

	base := func(element string, event metadata.FormEventType) url.Values {
		body := url.Values{}
		body.Set("_element", element)
		body.Set("_event", string(event))
		body.Set("_kind", "object")
		body.Set("tp_json.Товары", `[{"Количество":1}]`)
		body.Set("tp_json.Услуги", `[{"Цена":2}]`)
		return body
	}
	tests := []struct {
		name  string
		query string
		body  url.Values
	}{
		{
			name: "top-level button",
			body: func() url.Values {
				body := base("Кнопка", metadata.FormEventOnClick)
				body.Set("_tp", "Товары")
				return body
			}(),
		},
		{
			name: "handler of another table part",
			body: func() url.Values {
				body := base("ЭлементТовары", metadata.FormEventOnChange)
				body.Set("_tp", "Услуги")
				return body
			}(),
		},
		{
			name:  "query-string context",
			query: "?_tp=Товары&_tp_row=0",
			body:  base("ЭлементТовары", metadata.FormEventOnChange),
		},
		{
			name: "unknown column",
			body: func() url.Values {
				body := base("ЭлементТовары", metadata.FormEventOnChange)
				body.Set("_tp", "Товары")
				body.Set("_tp_row", "0")
				body.Set("_tp_row_number", "1")
				body.Set("_tp_col", "Поддельная")
				body.Set("_tp_col_index", "0")
				return body
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := executeFormEventWithQuery(t, srv, ent, tc.query, tc.body)
			resp := decodeFormEventResponse(t, rec.Body.Bytes())
			if resp.OK || resp.Error == "" || len(resp.Messages) != 0 {
				t.Fatalf("forged TP context accepted: ok=%v error=%q messages=%v", resp.OK, resp.Error, resp.Messages)
			}
		})
	}
}

func executeFormEventWithQuery(t *testing.T, s *Server, ent *metadata.Entity, query string, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/ui/catalog/"+ent.Name+"/form-event"+query, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "catalog")
	rctx.URLParams.Add("entity", ent.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
	}
	return rec
}

// При объявленных ПриДобавленииСтроки/ПриУдаленииСтроки рендер грида проставляет
// флаги data-sg-rowadd/data-sg-rowdel — без них фронтенд не дёргает событие.
func TestManagedFormGridRowEventAttrs(t *testing.T) {
	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: "Заказ",
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Заказ"},
		Elements: []*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				TitleMap: map[string]string{"ru": "Товары"},
				DataPath: "Объект.Товары",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnRowAdded:   "ТоварыПриДобавленииСтроки",
					metadata.FormEventOnRowDeleted: "ТоварыПриУдаленииСтроки",
				},
			},
		},
	}
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name:   "Товары",
			Fields: []metadata.Field{{Name: "Количество", Type: "number"}},
		}},
		Forms: []*metadata.FormModule{form},
	}
	data := map[string]any{
		"Entity":        ent,
		"Form":          form,
		"IsNew":         true,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"ChoiceOptions": map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  map[string]map[string]map[string]string{},
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Товары": {}},
		"User":          nil,
		"Lang":          "ru",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `data-sg-rowadd="1"`) {
		t.Error("нет data-sg-rowadd при объявленном ПриДобавленииСтроки")
	}
	if !strings.Contains(html, `data-sg-rowdel="1"`) {
		t.Error("нет data-sg-rowdel при объявленном ПриУдаленииСтроки")
	}
	if strings.Contains(html, "gridCellEventParams") {
		t.Error("runtime грида должен жить в /static/managed.js, а не в HTML")
	}
	js := string(managedJS)
	if !strings.Contains(js, "gridCellEventParams") || !strings.Contains(js, "_tp_col") || !strings.Contains(js, "_tp_row_number") {
		t.Error("/static/managed.js не содержит передачу контекста изменённой ячейки")
	}
}

// AutoSum на элементе ТЧ → грид получает data-sg-autosum; без флага — нет.
// Иначе обычная ТЧ с колонками Цена/Количество/Сумма связывалась бы сама (#215.1).
func TestManagedFormGridAutoSumAttr(t *testing.T) {
	render := func(autoSum bool) string {
		form := &metadata.FormModule{
			Name:       "ФормаОбъекта",
			Kind:       "object",
			EntityName: "Заказ",
			LayoutKind: metadata.FormLayoutManaged,
			Title:      map[string]string{"ru": "Заказ"},
			Elements: []*metadata.FormElement{{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				TitleMap: map[string]string{"ru": "Товары"},
				DataPath: "Объект.Товары",
				AutoSum:  autoSum,
			}},
		}
		ent := &metadata.Entity{
			Name: "Заказ",
			Kind: metadata.KindDocument,
			TableParts: []metadata.TablePart{{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Количество", Type: "number"},
					{Name: "Цена", Type: "number"},
					{Name: "Сумма", Type: "number"},
				},
			}},
			Forms: []*metadata.FormModule{form},
		}
		data := map[string]any{
			"Entity":        ent,
			"Form":          form,
			"IsNew":         true,
			"Values":        map[string]string{},
			"RefOptions":    map[string]any{},
			"EnumOptions":   map[string]any{},
			"ChoiceOptions": map[string]any{},
			"TPRefOptions":  map[string]any{},
			"TPEnumLabels":  map[string]map[string]map[string]string{},
			"TPEnumOrder":   map[string]map[string][]string{},
			"TPRefMeta":     map[string]any{},
			"TablePartRows": map[string][]map[string]any{"Товары": {}},
			"User":          nil,
			"Lang":          "ru",
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}

	if html := render(true); !strings.Contains(html, `data-sg-autosum="1"`) {
		t.Error("нет data-sg-autosum при auto_sum: true")
	}
	if html := render(false); strings.Contains(html, `data-sg-autosum="1"`) {
		t.Error("data-sg-autosum появился без auto_sum")
	}
}
