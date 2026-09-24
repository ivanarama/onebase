package ui

// Фон ГруппаФормы (#1547): рендер идёт через публичный HTTP-обработчик формы,
// как его открывает пользователь. Инвалидное значение и фон на негрупповом
// элементе не применяются — их называет check, а не рантайм.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

func renderManagedFormBody(t *testing.T, root *metadata.FormElement) string {
	t.Helper()
	ent := layoutTestEntity(root)
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := httptest.NewRequest("GET", "/ui/catalog/клиент/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "клиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	if rec.Code != 200 {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}
	return rec.Body.String()
}

func TestManagedGroup_BackgroundRendered(t *testing.T) {
	body := renderManagedFormBody(t, &metadata.FormElement{
		Kind:       metadata.FormElementGroupBox,
		Name:       "ГруппаДействия",
		Background: "#e8f5e9",
	})
	if !strings.Contains(body, "background:#e8f5e9") {
		t.Errorf("фон группы не попал в разметку:\n%s", body)
	}
}

func TestManagedGroup_BackgroundInvalidIgnored(t *testing.T) {
	body := renderManagedFormBody(t, &metadata.FormElement{
		Kind:       metadata.FormElementGroupBox,
		Name:       "ГруппаДействия",
		Background: "expression(url(javascript:alert(1)))",
	})
	if strings.Contains(body, "expression(") {
		t.Errorf("невалидное значение фона утекло в разметку:\n%s", body)
	}
}

func TestManagedField_BackgroundIgnored(t *testing.T) {
	body := renderManagedFormBody(t, &metadata.FormElement{
		Kind:       metadata.FormElementField,
		Name:       "ПолеНаименование",
		DataPath:   "Объект.Наименование",
		Background: "#e8f5e9",
	})
	if strings.Contains(body, "background:#e8f5e9") {
		t.Errorf("фон негруппового элемента не должен применяться:\n%s", body)
	}
}
