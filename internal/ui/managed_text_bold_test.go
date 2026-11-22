package ui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Имя клиента в заявке — жирным: text_bold у отдельного поля формы и надписи.
func TestManagedTextBold_FieldAndDecoration(t *testing.T) {
	ent := &metadata.Entity{Name: "ЗаявкаЖирный", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Клиент", Type: metadata.FieldTypeString},
		{Name: "Реклама", Type: metadata.FieldTypeString},
	}}
	ent.Forms = []*metadata.FormModule{managedObjectForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеКлиент", DataPath: "Объект.Клиент", ReadOnly: true, TextBold: true},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеРеклама", DataPath: "Объект.Реклама"},
		&metadata.FormElement{Kind: metadata.FormElementLabel, Name: "НадписьИтог", TitleMap: map[string]string{"ru": "Итог"}, TextBold: true},
	)}
	srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	id := uuid.New()
	if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Клиент": "Иванов Иван", "Реклама": "сайт"}, ent); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.formEdit(rec, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
		map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET формы: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	wrapper := func(name string) string {
		m := regexp.MustCompile(`<(div|fieldset) class="([^"]*)"[^>]*data-ob-el="` + name + `"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("элемент %s не найден:\n%s", name, body)
		}
		return m[2]
	}
	if c := wrapper("ПолеКлиент"); !regexp.MustCompile(`\bob-text-bold\b`).MatchString(c) {
		t.Fatalf("text_bold поля не дал класс: %q", c)
	}
	if c := wrapper("ПолеРеклама"); regexp.MustCompile(`\bob-text-bold\b`).MatchString(c) {
		t.Fatalf("поле без text_bold стало жирным: %q", c)
	}
	if c := wrapper("НадписьИтог"); !regexp.MustCompile(`\bob-text-bold\b`).MatchString(c) {
		t.Fatalf("text_bold надписи не дал класс: %q", c)
	}
	if !regexp.MustCompile(`\.ob-text-bold input,[^{]*\{font-weight:700\}`).MatchString(body) {
		t.Fatal("нет CSS-правила жирного значения")
	}
}
