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

// form.group_titles: bold — заголовки групп и таблиц этой формы жирные и
// тёмные; без ключа оформление прежнее (группа — полужирный серый, h3
// таблицы без своего цвета). Путь: YAML формы → loader → HTTP формы.
func TestManagedFormGroupTitlesBold_HTTP(t *testing.T) {
	render := func(header string) string {
		t.Helper()
		src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\n" + header +
			"elements:\n" +
			"  - kind: ГруппаФормы\n    name: ГруппаЗвонок\n    title:\n      ru: Информация по звонку\n    children:\n" +
			"      - kind: ПолеВвода\n        name: ПолеТема\n        data_path: Объект.Тема\n" +
			"  - kind: ТабличнаяЧасть\n    name: ТаблицаЗаявки\n    data_path: Объект.Заявки\n    title:\n      ru: Заявки клиента\n"
		path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Звонок")
		if err != nil {
			t.Fatal(err)
		}
		ent := &metadata.Entity{Name: "Звонок", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Тема", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{{Name: "Заявки", Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
			}}},
			Forms: []*metadata.FormModule{fm}}
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
		return rec.Body.String()
	}
	tag := func(page, class, text string) string {
		t.Helper()
		i := strings.Index(page, `class="`+class+`"`)
		if i < 0 {
			t.Fatalf("нет элемента %s", class)
		}
		start := strings.LastIndex(page[:i], "<")
		end := strings.Index(page[i:], text)
		if end < 0 {
			t.Fatalf("у %s нет текста %q", class, text)
		}
		return page[start : i+end]
	}

	plain := render("")
	if g := tag(plain, "ob-group-title", "Информация по звонку"); !strings.Contains(g, "font-weight:600;color:#475569;") {
		t.Errorf("заголовок группы без ключа изменился: %s", g)
	}
	if h := tag(plain, "ob-table-title", "Заявки клиента"); strings.Contains(h, "font-weight") || strings.Contains(h, "color:") {
		t.Errorf("заголовок таблицы без ключа получил оформление: %s", h)
	}

	bold := render("  group_titles: bold\n")
	if g := tag(bold, "ob-group-title", "Информация по звонку"); !strings.Contains(g, "font-weight:700;color:#1f2937;") {
		t.Errorf("group_titles: bold не сделал заголовок группы жирным: %s", g)
	}
	if h := tag(bold, "ob-table-title", "Заявки клиента"); !strings.Contains(h, "font-weight:700;color:#1f2937;") {
		t.Errorf("group_titles: bold не сделал заголовок таблицы жирным: %s", h)
	}
}
