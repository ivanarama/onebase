package ui

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
)

func richtextEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Задача",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Результат", Type: metadata.FieldTypeRichText},
		},
	}
}

// TestFormToFields_SanitizesRichText: на записи richtext-поле прогоняется через
// санитайзер — script/onerror вырезаны, форматирование сохранено.
func TestFormToFields_SanitizesRichText(t *testing.T) {
	body := url.Values{}
	body.Set("Наименование", "Тест")
	body.Set("Результат", `<p>ok</p><script>alert(1)</script><img src="x" onerror="alert(2)">`)

	req := httptest.NewRequest("POST", "/", nil)
	req.PostForm = body

	fields, err := formToFields(req, richtextEntity())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := fields["Результат"].(string)
	if got == "" {
		t.Fatalf("Результат пуст, ожидался санитизированный HTML")
	}
	low := strings.ToLower(got)
	if strings.Contains(low, "script") {
		t.Errorf("script не вырезан: %q", got)
	}
	if strings.Contains(low, "onerror") {
		t.Errorf("onerror не вырезан: %q", got)
	}
	if !strings.Contains(got, "<p>ok</p>") {
		t.Errorf("форматирование потеряно: %q", got)
	}
}

// TestCheckRichTextLimits_Oversize: значение больше MaxBytes → ошибка формы.
func TestCheckRichTextLimits_Oversize(t *testing.T) {
	body := url.Values{}
	body.Set("Наименование", "Тест")
	body.Set("Результат", strings.Repeat("a", richtext.MaxBytes+1))

	req := httptest.NewRequest("POST", "/", nil)
	req.PostForm = body

	if err := checkRichTextLimits(req, richtextEntity()); err == nil {
		t.Fatalf("ожидалась ошибка превышения размера, получили nil")
	}
}

// TestCheckRichTextLimits_WithinLimit: значение в пределах лимита → без ошибки.
func TestCheckRichTextLimits_WithinLimit(t *testing.T) {
	body := url.Values{}
	body.Set("Результат", "<p>небольшой текст</p>")

	req := httptest.NewRequest("POST", "/", nil)
	req.PostForm = body

	if err := checkRichTextLimits(req, richtextEntity()); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}

// TestPageForm_RichTextLoadsQuill: форма сущности с richtext-полем рендерит
// контейнер Quill (.richtext-editor) и подключает офлайн-ассеты из
// /vendor/quill/ (этап 2). textarea остаётся (прогрессивное улучшение).
func TestPageForm_RichTextLoadsQuill(t *testing.T) {
	data := map[string]any{
		"Entity":        richtextEntity(),
		"IsNew":         true,
		"Values":        map[string]string{"Наименование": "", "Результат": "<p>привет</p>"},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string]any{},
		"IsPopup":       true, // без nav — проще данные шаблона
		"Lang":          "ru",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		`<textarea name="Результат" autocomplete="off" class="richtext-field"`, // textarea сохранён (#595: autocomplete off)
		`<div class="richtext-editor"></div>`,                                  // контейнер Quill
		`/vendor/quill/quill.snow.css`,                                         // CSS офлайн
		`/vendor/quill/quill.js`,                                               // bundle офлайн
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page-form не содержит %q", want)
		}
	}
	for _, want := range []string{`new Quill(`, `q.clipboard.convert(`, `q.setContents(`} {
		if !strings.Contains(string(uiJS), want) {
			t.Errorf("/static/ui.js не содержит %q", want)
		}
	}
}

// TestPageForm_NoRichTextNoQuill: форма без richtext-поля НЕ тянет Quill —
// ассеты грузятся только когда они нужны.
func TestPageForm_NoRichTextNoQuill(t *testing.T) {
	ent := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	data := map[string]any{
		"Entity":        ent,
		"IsNew":         true,
		"Values":        map[string]string{"Наименование": ""},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string]any{},
		"IsPopup":       true,
		"Lang":          "ru",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	if strings.Contains(buf.String(), "/vendor/quill/") {
		t.Error("форма без richtext-поля не должна подключать Quill")
	}
}
