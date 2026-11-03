package ui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Заголовки закладок формы своим цветом и жирным шрифтом: оператор находит
// «Заказчика», «Изделие» и «Дополнение» по цвету, а черта активной закладки
// окрашена тем же цветом.
func tabTitleStyleButtons(t *testing.T, pages *metadata.FormElement) map[string]string {
	t.Helper()
	ent := &metadata.Entity{Name: "ЗаявкаЦветЗакладок", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}}}
	ent.Forms = []*metadata.FormModule{managedObjectForm(pages)}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{}, ent); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.formEdit(rec, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET формы: %d %s", rec.Code, rec.Body.String())
	}
	// Кнопка закладки: data-tab-page="<страница>" … [style="…"] … data-ob-readonly-navigation.
	re := regexp.MustCompile(`class="managed-tab-btn[^"]*" data-tab-idx="\d+" data-tab-page="([^"]*)"[^>]*?(?: style="([^"]*)")? data-ob-readonly-navigation`)
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(rec.Body.String(), -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("кнопки закладок не найдены:\n%s", rec.Body.String())
	}
	return out
}

func TestManagedTabTitleStyle_PageColorAndBold(t *testing.T) {
	got := tabTitleStyleButtons(t, &metadata.FormElement{
		Kind: metadata.FormElementPages, Name: "Закладки",
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "Заказчик", TitleColor: "#1565c0", TitleBold: true},
			{Kind: metadata.FormElementPage, Name: "Изделие", TitleColor: "#2e7d32"},
			{Kind: metadata.FormElementPage, Name: "Мусор", TitleColor: "red;background:url(x)"},
			{Kind: metadata.FormElementPage, Name: "Обычная"},
		},
	})
	if got["Заказчик"] != "--ob-tab-color:#1565c0;color:#1565c0;font-weight:700;" {
		t.Fatalf("цвет и жирность страницы: %q", got["Заказчик"])
	}
	if got["Изделие"] != "--ob-tab-color:#2e7d32;color:#2e7d32;" {
		t.Fatalf("цвет страницы: %q", got["Изделие"])
	}
	// Недопустимый цвет отброшен целиком, без атрибутов — как раньше.
	if got["Мусор"] != "" || got["Обычная"] != "" {
		t.Fatalf("без допустимых атрибутов стиля быть не должно: %q / %q", got["Мусор"], got["Обычная"])
	}
}

func TestManagedTabTitleStyle_SetBoldForAllPages(t *testing.T) {
	got := tabTitleStyleButtons(t, &metadata.FormElement{
		Kind: metadata.FormElementPages, Name: "Закладки", TabTitlesBold: true,
		Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "Заказчик", TitleColor: "#e65100"},
			{Kind: metadata.FormElementPage, Name: "Обычная"},
		},
	})
	if got["Заказчик"] != "--ob-tab-color:#e65100;color:#e65100;font-weight:700;font-size:15px;" {
		t.Fatalf("набор жирный + цвет страницы: %q", got["Заказчик"])
	}
	if got["Обычная"] != "font-weight:700;font-size:15px;" {
		t.Fatalf("набор жирный: %q", got["Обычная"])
	}
}
