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

// title_hidden: true у ТабличнаяЧасть — над таблицей нет заголовка (ни
// title, ни подставленного имени таблицы): у сетки и у таблицы значений.
// Таблица без ключа заголовок сохраняет. Путь: YAML → loader → HTTP формы.
func TestManagedTableTitleHidden_HTTP(t *testing.T) {
	src := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\n" +
		"attributes:\n  - name: Объект\n    type: CatalogRef.Заявка\n    main: true\n    save: true\n" +
		"  - name: РезультатыПоискаАдреса\n    type: ValueTable\n    columns:\n      - name: Адрес\n        type: string\n" +
		"elements:\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧРезультаты\n    data_path: Форма.РезультатыПоискаАдреса\n    title_hidden: true\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧТелефоныБезПодписи\n    data_path: Объект.Телефоны\n    title_hidden: true\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧКонтакты\n    data_path: Объект.Контакты\n    title:\n      ru: Контактные лица\n"
	path := filepath.Join(t.TempDir(), "ФормаОбъекта.form.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(path, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Телефоны", Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}}},
			{Name: "Контакты", Fields: []metadata.Field{{Name: "Имя", Type: metadata.FieldTypeString}}},
		}, Forms: []*metadata.FormModule{fm}}
	srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	rec := httptest.NewRecorder()
	srv.form(rec, reqWithChi(http.MethodGet, "/ui/catalog/заявка/new", nil, map[string]string{"entity": "заявка"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	for _, hidden := range []string{">РезультатыПоискаАдреса</h3>", ">Телефоны</h3>"} {
		if strings.Contains(page, hidden) {
			t.Errorf("title_hidden не убрал заголовок %s", hidden)
		}
	}
	if !strings.Contains(page, `data-vt="РезультатыПоискаАдреса"`) || !strings.Contains(page, `data-sg-tp="Телефоны"`) {
		t.Error("таблицы с title_hidden должны остаться на форме")
	}
	if !strings.Contains(page, ">Контактные лица</h3>") {
		t.Error("таблица без ключа потеряла заголовок")
	}
}
