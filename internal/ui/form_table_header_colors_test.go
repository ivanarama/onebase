package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// header_background / header_border_color у ТабличнаяЧасть красят шапку
// именно этой таблицы — SlickGrid, простой таблицы (no_grid) и таблицы
// значений; таблица без ключей не меняется. Путь: YAML формы → loader →
// зарегистрированный маршрут формы.
func TestTableHeaderColors_FormHTTP(t *testing.T) {
	ctx := t.Context()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entity := &metadata.Entity{Name: "Звонок", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Заявки", Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}},
			{Name: "Задачи", Fields: []metadata.Field{{Name: "Тема", Type: metadata.FieldTypeString}}},
			{Name: "Телефоны", Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}},
		}}
	const colors = "    header_background: \"#f1ead6\"\n    header_border_color: \"#efe3c2\"\n"
	source := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\n" +
		"attributes:\n  - name: Объект\n    type: CatalogRef.Звонок\n    main: true\n    save: true\n" +
		"  - name: История\n    type: ValueTable\n    columns:\n      - name: Номер\n        type: string\n" +
		"elements:\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧЗаявки\n    data_path: Объект.Заявки\n" + colors +
		"  - kind: ТабличнаяЧасть\n    name: ТЧЗадачи\n    data_path: Объект.Задачи\n    no_grid: true\n" + colors +
		"  - kind: ТабличнаяЧасть\n    name: ТЧИстория\n    data_path: Форма.История\n" + colors +
		"  - kind: ТабличнаяЧасть\n    name: ТЧТелефоны\n    data_path: Объект.Телефоны\n"
	file := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	form, err := loader.NewManagedFormLoader().LoadFormFile(file, entity.Name)
	if err != nil {
		t.Fatal(err)
	}
	entity.Forms = []*metadata.FormModule{form}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	server := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
	t.Cleanup(server.BeginShutdown)
	router := chi.NewRouter()
	server.Mount(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/catalog/звонок/new", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("form HTTP status: %d", rec.Code)
	}
	page := rec.Body.String()

	// Открывающий тег контейнера таблицы по его характерному атрибуту.
	openTag := func(marker string) string {
		t.Helper()
		i := strings.Index(page, marker)
		if i < 0 {
			t.Fatalf("нет таблицы %s", marker)
		}
		start := strings.LastIndex(page[:i], "<")
		return page[start : i+strings.Index(page[i:], ">")]
	}
	colored := regexp.MustCompile(`class="[^"]*ob-th-bg ob-th-border[^"]*"[^>]*style="[^"]*--ob-th-bg:#f1ead6;--ob-th-border:#efe3c2;`)
	for name, marker := range map[string]string{
		"SlickGrid":        `data-sg-tp="Заявки"`,
		"простая таблица":  `data-tp="Задачи"`,
		"таблица значений": `data-vt="История"`,
	} {
		if tag := openTag(marker); !colored.MatchString(tag) {
			t.Errorf("%s: шапка не получила цвета: %s", name, tag)
		}
	}
	if tag := openTag(`data-sg-tp="Телефоны"`); strings.Contains(tag, "ob-th-") || strings.Contains(tag, "--ob-th") {
		t.Errorf("таблица без ключей получила цвет шапки: %s", tag)
	}
	if !strings.Contains(page, ".ob-grid.ob-th-bg .slick-header-columns") {
		t.Error("нет CSS-правила цвета шапки")
	}
}
