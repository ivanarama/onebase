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

// background и border_color у ПолеВвода красят сам ввод («Приём заявок до» в
// ТЗ форм колл-центра): обёртка поля получает классы ob-field-bg/border и
// CSS-переменные цветов, в том числе у нередактируемого поля и вместе с
// раскладкой; поле без ключей не меняется. Путь: YAML → loader → HTTP.
func TestManagedFieldColors_HTTP(t *testing.T) {
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\nelements:\n" +
		"  - kind: ПолеВвода\n    name: ПолеПриёмДо\n    data_path: Объект.ПриёмДо\n    readonly: true\n    width: 240\n" +
		"    background: \"#fff6e6\"\n    border_color: \"#e0a23a\"\n" +
		"  - kind: ПолеВвода\n    name: ПолеТема\n    data_path: Объект.Тема\n" +
		"  - kind: ПолеВвода\n    name: ПолеЗаметка\n    data_path: Объект.Заметка\n    background: \"url(x)\"\n"
	path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Звонок")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Звонок", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "ПриёмДо", Type: metadata.FieldTypeString},
		{Name: "Тема", Type: metadata.FieldTypeString},
		{Name: "Заметка", Type: metadata.FieldTypeString},
	}, Forms: []*metadata.FormModule{fm}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := httptest.NewRequest("GET", "/ui/catalog/звонок/new", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("entity", "звонок")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
	rec := httptest.NewRecorder()
	s.form(rec, req)
	if rec.Code != 200 {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	wrapper := func(name string) string {
		t.Helper()
		i := strings.Index(page, `data-ob-el="`+name+`"`)
		if i < 0 {
			t.Fatalf("нет поля %s", name)
		}
		start := strings.LastIndex(page[:i], "<")
		end := strings.Index(page[i:], ">")
		return page[start : i+end]
	}

	colored := wrapper("ПолеПриёмДо")
	for _, want := range []string{"ob-field-bg", "ob-field-border", "--ob-field-bg:#fff6e6;", "--ob-field-border:#e0a23a;", "width:"} {
		if !strings.Contains(colored, want) {
			t.Errorf("обёртка окрашенного поля без %q: %s", want, colored)
		}
	}
	if strings.Count(colored, "style=") != 1 {
		t.Errorf("раскладка и цвета должны быть в одном style: %s", colored)
	}
	if plain := wrapper("ПолеТема"); strings.Contains(plain, "ob-field-") || strings.Contains(plain, "--ob-field") {
		t.Errorf("поле без ключей получило окраску: %s", plain)
	}
	if bad := wrapper("ПолеЗаметка"); strings.Contains(bad, "ob-field-bg") || strings.Contains(bad, "url(") {
		t.Errorf("нераспознанный цвет попал в разметку: %s", bad)
	}
	if !strings.Contains(page, ".form-group.ob-field-bg input:not([type=checkbox])") {
		t.Error("нет CSS-правила фона ввода")
	}
}
