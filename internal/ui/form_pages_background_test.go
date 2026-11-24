package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
)

// background у СтраницыФормы — фон всего набора страниц вместе с полосой
// закладок (кнопки закладок прозрачны); набор без ключа — прежний.
// Путь: YAML → loader → HTTP формы.
func TestManagedPagesBackground_HTTP(t *testing.T) {
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\nelements:\n" +
		"  - kind: СтраницыФормы\n    name: Закладки\n    background: \"#fffaf0\"\n    children:\n" +
		"      - kind: Страница\n        name: Заказчик\n        children:\n          - kind: ПолеВвода\n            name: ПолеА\n            data_path: Объект.Наименование\n" +
		"  - kind: СтраницыФормы\n    name: Прочее\n    children:\n" +
		"      - kind: Страница\n        name: Доп\n        children:\n          - kind: ПолеВвода\n            name: ПолеБ\n            data_path: Объект.Наименование\n"
	path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{fm}}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	rec := httptest.NewRecorder()
	srv.form(rec, reqWithChi(http.MethodGet, "/ui/catalog/заявка/new", nil, map[string]string{"entity": "заявка"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	openTag := func(name string) string {
		t.Helper()
		i := strings.Index(page, `data-tabs="`+name+`"`)
		if i < 0 {
			t.Fatalf("нет набора %s", name)
		}
		start := strings.LastIndex(page[:i], "<")
		return page[start : i+strings.Index(page[i:], ">")]
	}
	if tag := openTag("Закладки"); !strings.Contains(tag, "background:#fffaf0;") {
		t.Errorf("набор страниц не получил фон: %s", tag)
	}
	if tag := openTag("Прочее"); strings.Contains(tag, "background:") {
		t.Errorf("набор без ключа получил фон: %s", tag)
	}
}
