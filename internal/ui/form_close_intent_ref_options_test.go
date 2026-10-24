package ui

import (
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	sqlite "modernc.org/sqlite"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Ответ закрытия формы — дельта к снимку до ПередЗакрытием
// (compactFormCloseDelta): варианты выбора в нём остаются только у полей и ТЧ,
// которые обработчик изменил. Раньше их считали для всей формы дважды — для
// снимка и для ответа — и отбрасывали; у заявки колл-центра это 200–350 мс на
// каждое закрытие. Путь: YAML + .form.os → зарегистрированный маршрут
// form-close-intent; чтения справочников неизменившихся поля и ТЧ считает
// функция-счётчик в представлении на месте их таблиц.
func TestFormCloseIntentRefOptionsOnlyForChanges(t *testing.T) {
	var untouchedReads atomic.Int64
	probe := "close_untouched_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sqlite.MustRegisterScalarFunction(probe, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		untouchedReads.Add(1)
		return args[0], nil
	})
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ref := func(name, entity string) metadata.Field {
		return metadata.Field{Name: name, Type: metadata.FieldType("reference:" + entity), RefEntity: entity}
	}
	named := func(name string) *metadata.Entity {
		return &metadata.Entity{Name: name, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	}
	store, goods, service, staff := named("Склад"), named("Номенклатура"), named("Услуга"), named("Сотрудник")
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\n" +
		"events:\n  ПередЗакрытием: ПроверитьЗакрытие\nelements:\n" +
		"  - kind: ПолеВвода\n    name: Наименование\n    data_path: Объект.Наименование\n" +
		"  - kind: ПолеВвода\n    name: Склад\n    data_path: Объект.Склад\n" +
		"  - kind: ПолеВвода\n    name: Исполнитель\n    data_path: Объект.Исполнитель\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧТовары\n    data_path: Объект.Товары\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧУслуги\n    data_path: Объект.Услуги\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	// Обработчик меняет склад и добавляет строку товаров, исполнителя и услуги
	// не трогает; закрытие отменяет, чтобы форма осталась с ответом.
	module := "Процедура ПроверитьЗакрытие(Отказ)\n" +
		"  Объект.Склад = Справочники.Склад.НайтиПоНаименованию(\"второй\");\n" +
		"  С = Объект.Товары.Добавить();\n  С.Товар = Справочники.Номенклатура.НайтиПоНаименованию(\"товар\");\n" +
		"  Отказ = Истина;\nКонецПроцедуры\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	order := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, ref("Склад", "Склад"), ref("Исполнитель", "Сотрудник")},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{ref("Товар", "Номенклатура")}},
			{Name: "Услуги", Fields: []metadata.Field{ref("Услуга", "Услуга")}},
		},
		Forms: []*metadata.FormModule{fm}}
	entities := []*metadata.Entity{store, goods, service, staff, order}
	if err := db.Migrate(t.Context(), entities); err != nil {
		t.Fatal(err)
	}
	seed := func(ent *metadata.Entity, name string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if err := db.Upsert(t.Context(), ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := seed(store, "первый")
	seed(store, "второй")
	item := seed(goods, "товар")
	svc := seed(service, "услуга")
	worker := seed(staff, "исполнитель")

	reg := orun.NewRegistry()
	reg.Load(orun.LoadOptions{Entities: entities})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
	defer srv.BeginShutdown()

	// Справочники, к которым относятся неизменившиеся поле и ТЧ, читаются
	// через функцию-счётчик в представлении на месте таблицы.
	for _, ent := range []*metadata.Entity{service, staff} {
		table := metadata.TableName(ent.Name)
		columns, err := db.QueryAll(t.Context(), fmt.Sprintf(`PRAGMA table_info(%q)`, table))
		if err != nil {
			t.Fatal(err)
		}
		var projection []string
		for _, c := range columns {
			name := fmt.Sprint(c["name"])
			if strings.EqualFold(name, "Наименование") {
				projection = append(projection, fmt.Sprintf(`%s(%q) AS %q`, probe, name, name))
			} else {
				projection = append(projection, fmt.Sprintf("%q", name))
			}
		}
		for _, q := range []string{
			fmt.Sprintf(`ALTER TABLE %q RENAME TO %q`, table, table+"_rows"),
			fmt.Sprintf(`CREATE VIEW %q AS SELECT %s FROM %q`, table, strings.Join(projection, ","), table+"_rows"),
		} {
			if _, err := db.Exec(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
	}

	body := closeIntentBody(uuid.NewString(), "cross", "заявка")
	body.Set("Склад", first.String())
	body.Set("Исполнитель", worker.String())
	body.Set("tp_json.Товары", `[]`)
	body.Set("tp_json.Услуги", fmt.Sprintf(`[{"Услуга":%q}]`, svc.String()))
	rec := executeFormCloseIntent(t, srv, order, body)
	resp := decodeCloseIntentResponse(t, rec)
	if !resp.OK || resp.Close == nil || resp.Close.Allowed {
		t.Fatalf("ожидался отказ в закрытии с ответом формы: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := mapValueCI(resp.Values, "Склад"); !ok {
		t.Fatalf("изменённый обработчиком склад не приехал: %v", resp.Values)
	}
	if _, ok := resp.RefOptions["Склад"]; !ok {
		t.Errorf("изменённой ссылке нужен вариант выбора, иначе <select> её потеряет: %v", resp.RefOptions)
	}
	if opts, ok := resp.TPRefOptions["Товары"]; !ok || !strings.Contains(fmt.Sprint(opts["Товар"]), item.String()) {
		t.Errorf("изменённой ТЧ нужны варианты с добавленным товаром: %v", resp.TPRefOptions)
	}
	if _, ok := resp.TPRefOptions["Услуги"]; ok {
		t.Error("неизменившейся ТЧ варианты в ответе закрытия не нужны")
	}
	// Четыре чтения — подготовка объекта для обработчика: подписи ссылок шапки
	// и строк ТЧ и предзагрузка реквизитов ссылок (Объект.Исполнитель.…).
	// Варианты выбора для снимка и для ответа добавляли ещё четыре.
	if n := untouchedReads.Load(); n > 4 {
		t.Errorf("справочники неизменившихся поля и ТЧ прочитаны %d раз, ожидалось не больше 4 (подготовка объекта для обработчика): варианты для них ответ закрытия отбрасывает", n)
	}
}
