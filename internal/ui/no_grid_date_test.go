package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Дата в простой таблице (no_grid) рисуется полем даты, а не текстом: в
// текстовом поле стояла сырая метка «2026-10-19T09:15:30» (история заявки
// cc2). Путь пользователя: записать строку формой, открыть запись снова —
// дата на месте, с секундами, и пересохранение формы её не меняет.
func TestNoGridDateColumn_RendersDateInputAndRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readOnly bool
	}{
		{name: "editable"},
		{name: "readonly", readOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := metadata.TablePart{Name: "Lines", Fields: []metadata.Field{
				{Name: "Дата", Type: metadata.FieldTypeDate},
				{Name: "Действие", Type: metadata.FieldTypeString},
			}}
			form := managedObjectForm(
				fieldEl("NameField", "Object.Name"),
				&metadata.FormElement{
					Kind: metadata.FormElementTablePart, Name: "LinesTable",
					DataPath: "Object.Lines", NoGrid: true, ReadOnly: tc.readOnly,
				},
			)
			ent := &metadata.Entity{
				Name: "NoGridDateRecord", Kind: metadata.KindCatalog,
				Fields:     []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
				TableParts: []metadata.TablePart{tp},
				Forms:      []*metadata.FormModule{form},
			}
			srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})

			// Строку пишет форма: у readonly-таблицы браузер строки не шлёт,
			// поэтому её строку кладём записью с редактируемой формой.
			form.Elements[1].ReadOnly = false
			body := url.Values{
				"Name":                {"rec"},
				"tp_present.Lines":    {"1"},
				"tp.Lines.0.Дата":     {"2026-10-19T09:15:30"},
				"tp.Lines.0.Действие": {"Подтверждён отказ"},
			}
			rec := httptest.NewRecorder()
			srv.submit(rec, reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/new", body,
				map[string]string{"entity": ent.Name}))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("запись: code=%d body=%s", rec.Code, rec.Body.String())
			}
			form.Elements[1].ReadOnly = tc.readOnly
			id := onlyStoredID(t, srv, ent)

			open := func() string {
				rec := httptest.NewRecorder()
				srv.formEdit(rec, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
					map[string]string{"entity": ent.Name, "id": id.String()}))
				if rec.Code != http.StatusOK {
					t.Fatalf("открытие: code=%d body=%s", rec.Code, rec.Body.String())
				}
				return rec.Body.String()
			}
			page := open()
			want := `<input type="datetime-local" step="1" name="tp.Lines.0.Дата" value="2026-10-19T09:15:30"`
			if tc.readOnly {
				want = `<input type="text" name="tp.Lines.0.Дата" value="19.10.2026 09:15:30" disabled`
			}
			if !strings.Contains(page, want) {
				i := strings.Index(page, `name="tp.Lines.0.Дата"`)
				ctx := ""
				if i >= 0 {
					ctx = page[max(0, i-120):min(len(page), i+120)]
				}
				t.Fatalf("дата строки не полем даты с исходным временем; вокруг ячейки: %q", ctx)
			}
			if tc.readOnly {
				return
			}

			// Пересохранение формы тем значением, что она показала, дату не трогает.
			rec = httptest.NewRecorder()
			srv.submitEdit(rec, reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/"+id.String(), body,
				map[string]string{"entity": ent.Name, "id": id.String()}))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("пересохранение: code=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(open(), want) {
				t.Fatal("после пересохранения дата строки изменилась")
			}
		})
	}
}

func TestTPDateInputValue_KeepsWallClockAndConvertsZoned(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-19T09:15:30": "2026-10-19T09:15:30", // уже стенные часы — не сдвигать
		"2026-10-19":          "2026-10-19T00:00:00",
		"не дата":             "",
		"":                    "",
	} {
		if got := tpDateInputValue(in); got != want {
			t.Errorf("tpDateInputValue(%q) = %q, want %q", in, got, want)
		}
	}
}
