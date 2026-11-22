package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestManagedFieldLabelColor(t *testing.T) {
	entity := &metadata.Entity{
		Kind: metadata.KindCatalog,
		Name: "Товары",
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Примечание", Type: metadata.FieldTypeString},
			{Name: "Описание", Type: metadata.FieldTypeString},
		},
	}
	entity.Forms = []*metadata.FormModule{{
		Name: "Форма", EntityName: entity.Name, Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "Цветное", DataPath: "Объект.Наименование", LabelColor: "#134bc2", ReadOnly: true},
			{Kind: metadata.FormElementField, Name: "Обычное", DataPath: "Объект.Примечание"},
			{Kind: metadata.FormElementField, Name: "НедопустимыйЦвет", DataPath: "Объект.Описание", LabelColor: "red;background:url(x)"},
		},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	req := httptest.NewRequest(http.MethodGet, "/ui/catalog/товары/new", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("entity", "товары")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET формы: %d", rec.Code)
	}
	page := rec.Body.String()
	if !strings.Contains(page, `<label style="color:#134bc2;">Наименование</label>`) {
		t.Fatal("цвет не применён к подписи поля")
	}
	if strings.Count(page, "color:#134bc2;") != 1 {
		t.Fatal("цвет подписи применён и к другим элементам")
	}
	for _, label := range []string{"Примечание", "Описание"} {
		if !strings.Contains(page, "<label>"+label+"</label>") {
			t.Errorf("обычная подпись %q поменялась", label)
		}
	}
	if strings.Contains(page, "background:url(x)") {
		t.Fatal("невалидный цвет попал в CSS")
	}
}
