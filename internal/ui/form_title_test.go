package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedDisplayTitleInitialAndEvent(t *testing.T) {
	s, e, id := setupDeleteActionServer(t)
	f := e.Forms[0]
	f.TitleProc = "ПредставлениеКарточки"
	f.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Открытие"}
	f.ProgramAST = mustParse(t, `
Функция ПредставлениеКарточки()
  Объект.Наименование = "THIS MUST NOT BECOME A SAVE VALUE";
  Возврат "<MASK & ***>";
КонецФункции
Процедура Открытие()
КонецПроцедуры
`)
	body := renderObjectFormGET(t, s, e, id)
	if !strings.Contains(body, `<meta name="ob-tab-title" content="&lt;MASK &amp; ***&gt;">`) || !strings.Contains(body, `<span data-ob-form-title>&lt;MASK &amp; ***&gt;</span>`) {
		t.Fatal("display title must be escaped in both initial header and tab metadata")
	}
	if strings.Contains(body, "THIS MUST NOT BECOME A SAVE VALUE") {
		t.Fatal("display function mutated the rendered form values")
	}
	values := url.Values{"_event": {"ПриОткрытии"}, "_id": {id.String()}, "Наименование": {"X"}}
	req := httptest.NewRequest("POST", "/ui/catalog/"+e.Name+"/form-event", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	rc.URLParams.Add("kind", "catalog")
	rc.URLParams.Add("entity", e.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK || resp.FormTitle == nil || *resp.FormTitle != "<MASK & ***>" {
		t.Fatalf("missing event display title: %s", rec.Body.String())
	}
	row, err := s.store.GetByID(context.Background(), e.Name, id, e)
	if err != nil || row["Наименование"] != "X" {
		t.Fatalf("display function changed storage: %v %v", row, err)
	}
	if resp.Values["Наименование"] != "X" {
		t.Fatal("display function changed the save values")
	}
}

func TestManagedDisplayTitleRejectsWrites(t *testing.T) {
	s, e, id := setupDeleteActionServer(t)
	e.Forms[0].TitleProc = "ЗапрещённыйЗаголовок"
	e.Forms[0].ProgramAST = mustParse(t, `
Функция ЗапрещённыйЗаголовок()
  Объект.Наименование = "MUTATED";
  Объект.Записать();
  Возврат "bad";
КонецФункции
`)
	req := httptest.NewRequest("GET", "/ui/catalog/"+e.Name+"/"+id.String(), nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("kind", "catalog")
	rc.URLParams.Add("entity", e.Name)
	rc.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rec := httptest.NewRecorder()
	s.formEdit(rec, req)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "MUTATED") {
		t.Fatalf("unsafe title did not fail closed: %d %s", rec.Code, rec.Body.String())
	}
	row, err := s.store.GetByID(context.Background(), e.Name, id, e)
	if err != nil || row["Наименование"] != "X" {
		t.Fatalf("unsafe title mutated storage: %v %v", row, err)
	}
}
