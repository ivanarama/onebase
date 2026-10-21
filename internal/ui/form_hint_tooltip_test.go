package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// hint по умолчанию — всплывающая подсказка (значок ⓘ у подписи), а не текст
// под полем: у списка «Да/Нет», флажка и остальных полей одинаково. Текст под
// полем — только с hint_inline. Карточка открыта штатным обработчиком формы.
func TestManagedFormHintIsTooltipByDefault(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ОГД", Type: metadata.FieldTypeBool},
			{Name: "Звёздочка", Type: metadata.FieldTypeBool},
			{Name: "Примечание", Type: metadata.FieldTypeString},
		},
	}
	ent.Forms = []*metadata.FormModule{managedObjectForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеОГД", DataPath: "Объект.ОГД", Hint: "Особо гарантийный документ"},
		&metadata.FormElement{Kind: "Флажок", Name: "ПолеЗвёздочка", DataPath: "Объект.Звёздочка", Hint: "Клиент с особыми условиями"},
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "ПолеПримечание", DataPath: "Объект.Примечание", Hint: "видно под полем", HintInline: true},
	)}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.form(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<span class="ob-hint" title="Особо гарантийный документ" aria-label="Особо гарантийный документ">ⓘ</span></label>`,
		`<span class="ob-hint" title="Клиент с особыми условиями" aria-label="Клиент с особыми условиями">ⓘ</span></label>`,
		`<small style="color:#94a3b8;font-size:11px">видно под полем</small>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("нет %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `<small style="color:#94a3b8;font-size:11px">Особо гарантийный документ</small>`) {
		t.Error("hint списка «Да/Нет» напечатан текстом под полем, а должен всплывать")
	}
	if strings.Contains(body, `title="видно под полем" aria-label`) {
		t.Error("с hint_inline значок ⓘ не нужен — подсказка уже видна текстом")
	}
}
