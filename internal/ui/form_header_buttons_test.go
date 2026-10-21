package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Кнопка с placement: header рисуется в строке команд документа — сразу после
// «История» у записанного объекта, в конце строки у нового — и не рисуется в
// теле формы. Карточка открыта штатными обработчиками формы.
func TestManagedFormHeaderPlacementButton(t *testing.T) {
	btn := func() *metadata.FormElement {
		return &metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнУведомить", Placement: metadata.FormPlacementHeader,
			TitleMap: map[string]string{"ru": "Уведомить тех. отдел"},
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Уведомить"},
		}
	}
	check := func(t *testing.T, body string, after string) {
		t.Helper()
		if n := strings.Count(body, `data-ob-fire-click="КнУведомить"`); n != 1 {
			t.Fatalf("кнопка должна быть ровно одна, найдено %d:\n%s", n, body)
		}
		at := strings.Index(body, `data-ob-fire-click="КнУведомить"`)
		if form := strings.Index(body, `<form id="main-form"`); form < 0 || at > form {
			t.Fatalf("кнопка не в строке команд (после начала тела формы):\n%s", body)
		}
		if after != "" {
			if h := strings.Index(body, after); h < 0 || h > at {
				t.Fatalf("кнопка не стоит после %q:\n%s", after, body)
			}
		}
	}

	t.Run("новая запись", func(t *testing.T) {
		ent := layoutTestEntity(btn())
		srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
		req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
		rec := httptest.NewRecorder()
		srv.form(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("form: %d", rec.Code)
		}
		check(t, rec.Body.String(), "")
	})
	t.Run("записанная — после «История»", func(t *testing.T) {
		srv, ent := setupManagedEventsServer(t, "Процедура Уведомить()\nКонецПроцедуры\n", nil, []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"}, btn(),
		})
		id := insertContragent(t, srv, ent, "Ромашка")
		rec := executeFormEditGET(t, srv, ent, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("formEdit: %d", rec.Code)
		}
		check(t, rec.Body.String(), "/history\" class=\"btn btn-sm btn-secondary\">")
	})
}
