package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func imageEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Картинка", Type: metadata.FieldTypeImage},
		},
	}
}

// TestPageForm_ImageFieldWidget: форма с полем image рендерит скрытый input
// (хранит ссылку-UUID), превью существующей картинки и кнопку загрузки с
// entity-scoped URL /ui/<kind>/<entity>/_image.
func TestPageForm_ImageFieldWidget(t *testing.T) {
	data := map[string]any{
		"Entity":        imageEntity(),
		"IsNew":         false,
		"CanWrite":      true,
		"Values":        map[string]string{"Наименование": "Болт", "Картинка": "abc-123"},
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
	html := buf.String()
	for _, want := range []string{
		`<input type="hidden" name="Картинка" value="abc-123">`, // form-backing ссылка
		`/ui/_image/abc-123`,              // превью существующей картинки
		`/ui/catalog/номенклатура/_image`, // entity-scoped URL загрузки (lower)
		`id="ob-tp-ref-opts"`,             // общий form-shared bootstrap подключён
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page-form (image) не содержит %q", want)
		}
	}
	if !strings.Contains(string(uiJS), `function obImageUpload(`) {
		t.Errorf("/static/ui.js не содержит obImageUpload")
	}
}

// TestPageList_ImageThumbnail: в табличном списке поле image выводится как
// миниатюра <img src="/ui/_image/...">, а не как сырой UUID.
func TestPageList_ImageThumbnail(t *testing.T) {
	rows := []map[string]any{
		{"id": "1", "Наименование": "Болт", "Картинка": "ref-9"},
	}
	data := map[string]any{
		"Entity":           imageEntity(),
		"Rows":             rows,
		"Params":           storage.ListParams{},
		"RefFilterOptions": map[string]any{},
		"IsAdmin":          true,
		"Lang":             "ru",
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-list", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()
	if !strings.Contains(html, `src="/ui/_image/ref-9"`) {
		t.Error("в списке нет миниатюры картинки")
	}
	if strings.Contains(html, ">ref-9<") {
		t.Error("в списке выводится сырой UUID картинки вместо миниатюры")
	}
}
