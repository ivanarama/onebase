package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
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
