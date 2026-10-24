package ui

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	sqlite "modernc.org/sqlite"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	orun "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Событие формы читает сохранённую запись шапки в трёх местах: восстановление
// не пришедших с формы полей, перечитка полей после обработчика и признак
// изменённости. Пока запрос ничего не записал, это одна и та же строка — её
// читают один раз; запись из обработчика сбрасывает кэш, и форма получает
// записанное. Путь: YAML + .form.os → зарегистрированный маршрут form-event;
// чтения шапки считает функция-счётчик в представлении на месте таблицы.
func TestFormEventReadsHeaderRowOncePerRequest(t *testing.T) {
	var reads atomic.Int64
	probe := "head_reads_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	// Функцию регистрируем до подключения: соединения пула получают её при
	// открытии.
	sqlite.MustRegisterScalarFunction(probe, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		reads.Add(1)
		return args[0], nil
	})
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "head.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заказ\nelements:\n" +
		"  - kind: Кнопка\n    name: КнНичего\n    events:\n      Нажатие: Ничего\n" +
		"  - kind: Кнопка\n    name: КнМодуль\n    events:\n      Нажатие: ЗаписатьЧерезОбъект\n" +
		"  - kind: ПолеВвода\n    name: Наименование\n    data_path: Объект.Наименование\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	module := "Процедура Ничего()\nКонецПроцедуры\n\n" +
		"Процедура ЗаписатьЧерезОбъект()\n  Д = Объект.Ссылка.ПолучитьОбъект();\n  Д.Комментарий = \"из модуля\";\n  Д.Записать();\nКонецПроцедуры\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заказ")
	if err != nil {
		t.Fatal(err)
	}
	// «Комментарий» на форме не стоит и с неё не приходит: его восстанавливают
	// и перечитывают из базы — ровно те чтения, которые делит кэш.
	ent := &metadata.Entity{Name: "Заказ", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Комментарий", Type: metadata.FieldTypeString}},
		Forms:  []*metadata.FormModule{fm}}
	if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(t.Context(), ent.Name, id, map[string]any{"Наименование": "заказ", "Комментарий": "из базы"}, ent); err != nil {
		t.Fatal(err)
	}
	reg := orun.NewRegistry()
	reg.Load(orun.LoadOptions{Entities: []*metadata.Entity{ent}})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	srv := New(reg, db, interp, auth.NewRepo(db), Config{}, nil)
	defer srv.BeginShutdown()
	router := chi.NewRouter()
	srv.Mount(router)

	fire := func(element string) (map[string]any, int64) {
		t.Helper()
		form := url.Values{"_element": {element}, "_event": {"Нажатие"}, "_id": {id.String()}, "Наименование": {"заказ"}}
		req := httptest.NewRequest(http.MethodPost, "/ui/catalog/заказ/form-event", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		reads.Store(0)
		router.ServeHTTP(rec, req)
		var resp struct {
			OK     bool           `json:"ok"`
			Values map[string]any `json:"values"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK {
			t.Fatalf("form-event %s: %d %s", element, rec.Code, rec.Body.String())
		}
		return resp.Values, reads.Load()
	}

	// Чтения шапки — через функцию-счётчик в представлении на месте таблицы.
	table := metadata.TableName(ent.Name)
	columns, err := db.QueryAll(t.Context(), fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		t.Fatal(err)
	}
	var projection []string
	for _, c := range columns {
		name := fmt.Sprint(c["name"])
		if strings.EqualFold(name, "Комментарий") {
			projection = append(projection, fmt.Sprintf(`%s(%q) AS %q`, probe, name, name))
		} else {
			projection = append(projection, fmt.Sprintf("%q", name))
		}
	}
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	exec(fmt.Sprintf(`ALTER TABLE %q RENAME TO "head_rows"`, table))
	exec(fmt.Sprintf(`CREATE VIEW %q AS SELECT %s FROM "head_rows"`, table, strings.Join(projection, ",")))
	_, idleReads := fire("КнНичего")
	// Обработчик с записью пишет в таблицу через объект — вернём её.
	exec(fmt.Sprintf(`DROP VIEW %q`, table))
	exec(fmt.Sprintf(`ALTER TABLE "head_rows" RENAME TO %q`, table))

	values, _ := fire("КнМодуль")
	if got := fmt.Sprint(values["Комментарий"]); got != "из модуля" {
		t.Fatalf("поле, записанное обработчиком через объект, не перечитано: %q", got)
	}
	// Одно чтение — общая запись шапки для трёх мест; второе — предзагрузка
	// Объект.Ссылка (свой набор колонок, не GetByID). Без кэша их было четыре.
	if idleReads > 2 {
		t.Errorf("обработчик без записи: шапка прочитана %d раз, ожидалось не больше двух (запись шапки один раз + предзагрузка ссылки)", idleReads)
	}
}
