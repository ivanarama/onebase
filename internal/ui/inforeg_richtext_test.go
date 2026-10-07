package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// richtext-ресурс регистра сведений редактируется тем же редактором, что и
// richtext-реквизит объекта. До этого форма записи рисовала его однострочным
// вводом: оформление в регистре можно было задать только правкой разметки
// руками — то есть на практике никак.
func TestInfoRegFormRendersRichTextEditor(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name: "ПримечанияПоНаправлениям",
		Dimensions: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:Направление"), RefEntity: "Направление"},
		},
		Resources: []metadata.Field{
			{Name: "Примечание", Type: metadata.FieldTypeRichText},
			{Name: "ИдЛегаси", Type: metadata.FieldTypeString},
		},
	}
	html := renderInfoRegForm(t, ir, map[string]string{"Примечание": "<p><b>Выезд платный</b></p>", "ИдЛегаси": ""})

	if !strings.Contains(html, `<textarea name="Примечание" autocomplete="off" class="richtext-field"`) {
		t.Error("richtext-ресурс отрисован не как поле редактора")
	}
	if !strings.Contains(html, `<div class="richtext-editor"></div>`) {
		t.Error("нет места под редактор — Quill монтируется на соседний .richtext-editor")
	}
	// Разметка уезжает в textarea экранированной — как и в карточке объекта:
	// браузер вернёт её редактору обратно как HTML при разборе содержимого.
	if !strings.Contains(html, "&lt;b&gt;Выезд платный&lt;/b&gt;") {
		t.Error("сохранённая разметка не попала в поле")
	}
	if !strings.Contains(html, "/vendor/quill/quill.js") {
		t.Error("ассеты редактора не подключены на форме записи регистра")
	}
	// Обычный ресурс остаётся однострочным вводом.
	if !strings.Contains(html, `<input type="text" name="ИдЛегаси"`) {
		t.Error("строковый ресурс перестал быть однострочным вводом")
	}
}

// У регистра без richtext-ресурса вендор-ассеты не грузятся: они нужны не всем,
// а страница за них платит.
func TestInfoRegFormWithoutRichTextSkipsEditorAssets(t *testing.T) {
	ir := &metadata.InfoRegister{
		Name:       "СтатусОператора",
		Dimensions: []metadata.Field{{Name: "Оператор", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Статус", Type: metadata.FieldTypeString}},
	}
	html := renderInfoRegForm(t, ir, map[string]string{"Оператор": "", "Статус": ""})
	if strings.Contains(html, "/vendor/quill/quill.js") {
		t.Error("ассеты редактора подключены там, где richtext нет")
	}
}

func renderInfoRegForm(t *testing.T, ir *metadata.InfoRegister, values map[string]string) string {
	t.Helper()
	data := map[string]any{
		"InfoReg": ir,
		"Values":  values,
		"RefOpts": map[string][]map[string]any{},
		"User":    nil, "Lang": "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-inforeg-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func newInfoRegRichTextServer(t *testing.T) (*httptest.Server, *Server, *metadata.InfoRegister) {
	t.Helper()
	ir := &metadata.InfoRegister{
		Name:       "ПримечанияПоНаправлениям",
		Dimensions: []metadata.Field{{Name: "Направление", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Примечание", Type: metadata.FieldTypeRichText}},
	}
	s, ctx := newSubmitTestServer(t, nil)
	if err := s.store.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	s.reg.Load(runtime.LoadOptions{InfoRegs: []*metadata.InfoRegister{ir}})
	router := chi.NewRouter()
	s.Mount(router)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return ts, s, ir
}

func infoRegSubmitURL(ts *httptest.Server, infoReg string) string {
	return ts.URL + "/ui/inforeg/" + url.PathEscape(infoReg) + "/new"
}

func TestInfoRegRichTextSubmitSanitizesBeforeSave(t *testing.T) {
	ts, s, ir := newInfoRegRichTextServer(t)
	unsafeHTML := `<p><b>Выезд платный</b><script>alert(1)</script><img src="x" onerror="alert(2)"></p>`
	form := url.Values{
		"Направление": {"Монтаж"},
		"Примечание":  {unsafeHTML},
	}

	code, body := postForm(t, infoRegSubmitURL(ts, ir.Name), form)
	if code != http.StatusFound {
		t.Fatalf("статус = %d, ожидался 302; тело: %s", code, body)
	}
	rows, err := s.store.InfoRegList(t.Context(), ir, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("записей = %d, ожидалась одна", len(rows))
	}
	got := rows[0]["Примечание"].(string)
	if !strings.Contains(got, "<b>Выезд платный</b>") {
		t.Fatalf("безопасная разметка потеряна: %q", got)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "onerror") {
		t.Fatalf("опасная разметка сохранена: %q", got)
	}
}

func TestInfoRegRichTextSubmitAcceptsTwoMiB(t *testing.T) {
	ts, s, ir := newInfoRegRichTextServer(t)
	html := "<p>" + strings.Repeat("a", 2<<20) + "</p>"
	form := url.Values{"Направление": {"Сервис"}, "Примечание": {html}}

	code, body := postForm(t, infoRegSubmitURL(ts, ir.Name), form)
	if code != http.StatusFound {
		t.Fatalf("статус = %d, ожидался 302; тело: %s", code, body)
	}
	rows, err := s.store.InfoRegList(t.Context(), ir, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0]["Примечание"].(string)) < 2<<20 {
		t.Fatalf("richtext не сохранён целиком: %#v", rows)
	}
}

func TestInfoRegRichTextSubmitRejectsValueOverMaxBytes(t *testing.T) {
	ts, s, ir := newInfoRegRichTextServer(t)
	form := url.Values{
		"Направление": {"СлишкомМного"},
		"Примечание":  {strings.Repeat("a", richtext.MaxBytes+1)},
	}

	code, body := postForm(t, infoRegSubmitURL(ts, ir.Name), form)
	if code != http.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400; тело: %s", code, body)
	}
	if strings.Contains(body, "request body too large") || !strings.Contains(body, "richtext") {
		t.Fatalf("непонятная ошибка лимита: %s", body)
	}
	rows, err := s.store.InfoRegList(t.Context(), ir, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("превышенное значение сохранено: %#v", rows)
	}
}
