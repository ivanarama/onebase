package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
)

// frame: false у ГруппаФормы — строка полей внутри раздела без своей рамки,
// отступов и заголовка; раскладка (горизонталь) остаётся. Группа без ключа —
// прежняя рамка с заголовком. Путь: YAML формы → loader → HTTP формы.
func TestManagedGroupFrameFalse_HTTP(t *testing.T) {
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\nelements:\n" +
		"  - kind: ГруппаФормы\n    name: ГруппаАдрес\n    title:\n      ru: Адрес\n    children:\n" +
		"      - kind: ГруппаФормы\n        name: СтрокаДом\n        frame: false\n        orientation: horizontal\n        title:\n          ru: Строка дома\n        children:\n" +
		"          - kind: ПолеВвода\n            name: ПолеДом\n            data_path: Объект.Дом\n" +
		"          - kind: ПолеВвода\n            name: ПолеКвартира\n            data_path: Объект.Квартира\n"
	path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Дом", Type: metadata.FieldTypeString}, {Name: "Квартира", Type: metadata.FieldTypeString},
	}, Forms: []*metadata.FormModule{fm}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := httptest.NewRequest("GET", "/ui/catalog/заявка/new", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("entity", "заявка")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	if rec.Code != 200 {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	openTag := func(name string) string {
		t.Helper()
		i := strings.Index(page, `data-ob-el="`+name+`"`)
		if i < 0 {
			t.Fatalf("нет группы %s", name)
		}
		start := strings.LastIndex(page[:i], "<")
		return page[start : i+strings.Index(page[i:], ">")]
	}

	plain := openTag("СтрокаДом")
	for _, want := range []string{"ob-group-plain", "managed-group-horizontal", "border:0;padding:0;margin:0;"} {
		if !strings.Contains(plain, want) {
			t.Errorf("группа frame: false без %q: %s", want, plain)
		}
	}
	if strings.Contains(plain, "border:1px") {
		t.Errorf("группа frame: false сохранила рамку: %s", plain)
	}
	if strings.Contains(page, "Строка дома") {
		t.Error("группа frame: false нарисовала заголовок")
	}
	if !strings.Contains(page, "ПолеКвартира") {
		t.Error("дети группы frame: false не отрисованы")
	}

	framed := openTag("ГруппаАдрес")
	if !strings.Contains(framed, "border:1px solid #e2e8f0") || strings.Contains(framed, "ob-group-plain") {
		t.Errorf("группа без ключа потеряла рамку: %s", framed)
	}
	if !strings.Contains(page, ">Адрес</legend>") {
		t.Error("группа без ключа потеряла заголовок")
	}
}
