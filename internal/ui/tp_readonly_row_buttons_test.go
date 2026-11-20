package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Табличная часть-выдача (результаты поиска): все видимые колонки readonly —
// кнопок «+ Добавить строку» / «− Удалить строку» под ней нет. Хоть одна
// редактируемая колонка — кнопки на месте. Через публичный GET формы.
func TestManagedTablePart_ReadonlyColumnsHideRowButtons(t *testing.T) {
	for _, tc := range []struct {
		name        string
		secondRO    bool
		wantButtons bool
	}{
		{name: "все_колонки_readonly", secondRO: true, wantButtons: false},
		{name: "есть_редактируемая", secondRO: false, wantButtons: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ent := &metadata.Entity{
				Name: "Поиск", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
				TableParts: []metadata.TablePart{{Name: "Найденные", Fields: []metadata.Field{
					{Name: "Номер", Type: metadata.FieldTypeString},
					{Name: "Клиент", Type: metadata.FieldTypeString},
				}}},
			}
			form := &metadata.FormModule{
				Name: "ФормаОбъекта", Kind: "object", EntityName: ent.Name,
				LayoutKind: metadata.FormLayoutManaged,
				Elements: []*metadata.FormElement{{
					Kind: metadata.FormElementTablePart, Name: "Найденные", DataPath: "Объект.Найденные",
					Children: []*metadata.FormElement{
						{Kind: metadata.FormElementColumn, Name: "КолНомер", DataPath: "Объект.Найденные.Номер", ReadOnly: true},
						{Kind: metadata.FormElementColumn, Name: "КолКлиент", DataPath: "Объект.Найденные.Клиент", ReadOnly: tc.secondRO},
					},
				}},
			}
			ent.Forms = []*metadata.FormModule{form}
			s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			req := reqWithChi(http.MethodGet, "/ui/catalog/поиск/new", nil, map[string]string{"entity": "поиск"})
			rec := httptest.NewRecorder()
			s.form(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET: %d", rec.Code)
			}
			body := rec.Body.String()
			for _, attr := range []string{`data-ob-grid-add="Найденные"`, `data-ob-grid-del="Найденные"`} {
				if got := strings.Contains(body, attr); got != tc.wantButtons {
					t.Errorf("%s присутствует = %v, want %v", attr, got, tc.wantButtons)
				}
			}
			if !tc.wantButtons && strings.Contains(body, "Добавить строку") {
				t.Error("под таблицей-выдачей осталась кнопка «Добавить строку»")
			}
		})
	}
}
