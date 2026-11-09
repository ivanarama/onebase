package ui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Пустое поле с маской ввода показывает её шаблон: карточка открыта штатным
// обработчиком формы — тем же путём, что у пользователя.
func TestManagedFormInputMaskShowsPlaceholder(t *testing.T) {
	ent := layoutTestEntity(&metadata.FormElement{
		Kind:      metadata.FormElementField,
		Name:      "ПолеКомментарий",
		DataPath:  "Объект.Комментарий",
		InputMask: "(000)000-00-00",
	})
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
	body := rec.Body.String()
	if !strings.Contains(body, `data-ob-input-mask="(000)000-00-00" placeholder="(___)___-__-__"`) {
		t.Errorf("шаблон маски не выведен подсказкой пустого поля:\n%s", body)
	}
}
