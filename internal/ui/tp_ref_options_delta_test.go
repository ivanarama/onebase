package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ответ события формы несёт варианты выбора ссылочных колонок только тех
// табличных частей, которые стоят на форме и изменились обработчиком (или
// чьи варианты зависят от владельца). Неизменившиеся ТЧ браузер держит со
// страницы — пересылать их на каждое событие значило десятки запросов и
// десятки КБ (заявка колл-центра: 17 ТЧ, 66 КБ одинаковых вариантов).
// Путь: YAML + .form.os → зарегистрированный маршрут form-event.
func TestFormEventTPRefOptionsOnlyChangedTableParts(t *testing.T) {
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "tpref.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ref := func(name, entity string) metadata.Field {
		return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
	}
	goods := &metadata.Entity{Name: "Номенклатура", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	store := &metadata.Entity{Name: "Склад", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	cell := &metadata.Entity{Name: "Ячейка", Kind: metadata.KindCatalog, Owner: "Склад", Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\nelements:\n" +
		"  - kind: Кнопка\n    name: КнНичего\n    events:\n      Нажатие: Ничего\n" +
		"  - kind: Кнопка\n    name: КнДобавить\n    events:\n      Нажатие: ДобавитьТовар\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧТовары\n    data_path: Объект.Товары\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧЯчейки\n    data_path: Объект.Ячейки\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte("Процедура Ничего()\nКонецПроцедуры\n\nПроцедура ДобавитьТовар()\n  Строка = Объект.Товары.Добавить();\nКонецПроцедуры\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	order := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, ref("Склад", "Склад")},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{ref("Товар", "Номенклатура")}},
			{Name: "Прочее", Fields: []metadata.Field{ref("Товар", "Номенклатура")}},
			{Name: "Ячейки", Fields: []metadata.Field{ref("Ячейка", "Ячейка")}},
		},
		Forms: []*metadata.FormModule{fm}}
	entities := []*metadata.Entity{goods, store, cell, order}
	if err := db.Migrate(t.Context(), entities); err != nil {
		t.Fatal(err)
	}
	reg := orun.NewRegistry()
	reg.Load(orun.LoadOptions{Entities: entities})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
	defer srv.BeginShutdown()
	router := chi.NewRouter()
	srv.Mount(router)

	fire := func(element string) map[string]json.RawMessage {
		t.Helper()
		form := url.Values{"_element": {element}, "_event": {"Нажатие"},
			"tp_json.Товары": {"[]"}, "tp_json.Прочее": {"[]"}, "tp_json.Ячейки": {"[]"}}
		req := httptest.NewRequest(http.MethodPost, "/ui/catalog/заявка/form-event", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var resp struct {
			OK           bool                       `json:"ok"`
			Error        string                     `json:"error"`
			TPRefOptions map[string]json.RawMessage `json:"tpRefOptions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK {
			t.Fatalf("form-event %s: %d %s", element, rec.Code, rec.Body.String())
		}
		return resp.TPRefOptions
	}

	idle := fire("КнНичего")
	if _, ok := idle["Товары"]; ok {
		t.Error("ТЧ на форме без изменений не должна пересылать варианты")
	}
	if _, ok := idle["Прочее"]; ok {
		t.Error("ТЧ не на форме не должна пересылать варианты")
	}
	if _, ok := idle["Ячейки"]; !ok {
		t.Error("варианты, зависящие от владельца, пересылаются всегда: владельца мог сменить пользователь")
	}

	added := fire("КнДобавить")
	if _, ok := added["Товары"]; !ok {
		t.Error("обработчик изменил строки ТЧ — варианты этой ТЧ обязаны приехать")
	}
	if _, ok := added["Прочее"]; ok {
		t.Error("ТЧ не на форме не должна пересылать варианты и после изменения соседней")
	}
}

// Открытие управляемой формы: варианты ссылочных колонок приходят только для
// ТЧ, стоящих на форме; ТЧ сущности, которой на форме нет, браузер не рисует.
func TestFormOpenTPRefOptionsOnlyPlacedTableParts(t *testing.T) {
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "open.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ref := metadata.Field{Name: "Товар", Type: metadata.FieldType("reference:Номенклатура"), RefEntity: "Номенклатура"}
	goods := &metadata.Entity{Name: "Номенклатура", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\nelements:\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧТовары\n    data_path: Объект.Товары\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	order := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{ref}},
			{Name: "Прочее", Fields: []metadata.Field{ref}},
		},
		Forms: []*metadata.FormModule{fm}}
	entities := []*metadata.Entity{goods, order}
	if err := db.Migrate(t.Context(), entities); err != nil {
		t.Fatal(err)
	}
	reg := orun.NewRegistry()
	reg.Load(orun.LoadOptions{Entities: entities})
	srv := New(reg, db, interpreter.New(), auth.NewRepo(db), Config{}, nil)
	defer srv.BeginShutdown()
	router := chi.NewRouter()
	srv.Mount(router)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/catalog/заявка/new", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("form: %d", rec.Code)
	}
	page := rec.Body.String()
	marker := `id="ob-managed-tp-ref-opts">`
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatal("нет вариантов ТЧ в странице")
	}
	end := strings.Index(page[i:], "</script>")
	var opts map[string]json.RawMessage
	if err := json.Unmarshal([]byte(page[i+len(marker):i+end]), &opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := opts["Товары"]; !ok {
		t.Error("ТЧ на форме обязана получить варианты при открытии")
	}
	if _, ok := opts["Прочее"]; ok {
		t.Error("ТЧ не на форме не должна получать варианты при открытии")
	}
}
