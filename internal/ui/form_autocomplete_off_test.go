package ui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Форма объекта — и управляемая, и автоформа — несёт autocomplete="off" на
// самом <form>: браузер предлагает «Сохранить адрес?» по отправке формы, а не
// только по подсказкам отдельных полей. Через публичный GET новой формы.
func TestMainFormAutocompleteOff(t *testing.T) {
	formTag := regexp.MustCompile(`<form id="main-form"[^>]*>`)
	for _, managed := range []bool{true, false} {
		name := "автоформа"
		if managed {
			name = "управляемая"
		}
		t.Run(name, func(t *testing.T) {
			ent := &metadata.Entity{Name: "Адреса", Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Адрес", Type: metadata.FieldTypeString},
			}}
			if managed {
				ent.Forms = []*metadata.FormModule{{
					Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name, LayoutKind: metadata.FormLayoutManaged,
					Elements: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "Адрес", DataPath: "Объект.Адрес"}},
				}}
			}
			s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			req := reqWithChi(http.MethodGet, "/ui/catalog/адреса/new", nil, map[string]string{"entity": "адреса"})
			rec := httptest.NewRecorder()
			s.form(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET: %d", rec.Code)
			}
			tag := formTag.FindString(rec.Body.String())
			if tag == "" || !strings.Contains(tag, `autocomplete="off"`) {
				t.Fatalf("у <form id=\"main-form\"> нет autocomplete=\"off\": %q", tag)
			}
		})
	}
}
