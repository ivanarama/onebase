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

// Строки ТЧ после обработчика события перечитываются из базы, только если за
// время обработчика в неё писали: обработчик, переписавший ТЧ этой же записи
// через объект из базы (как общий модуль, #579), отдаёт форме свежие строки;
// обработчик, ничего не писавший, ТЧ не перечитывает. Путь: YAML + .form.os →
// зарегистрированный маршрут form-event; чтения ТЧ считает функция-счётчик в
// представлении на месте таблицы ТЧ.
func TestFormEventTablePartRefreshOnlyAfterWrites(t *testing.T) {
	var reads atomic.Int64
	probe := "tp_refresh_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sqlite.MustRegisterScalarFunction(probe, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		reads.Add(1)
		return args[0], nil
	})
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заказ\nelements:\n" +
		"  - kind: Кнопка\n    name: КнНичего\n    events:\n      Нажатие: Ничего\n" +
		"  - kind: Кнопка\n    name: КнМодуль\n    events:\n      Нажатие: ЗаписатьЧерезОбъект\n" +
		"  - kind: ТабличнаяЧасть\n    name: ТЧТовары\n    data_path: Объект.Товары\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	module := "Процедура Ничего()\nКонецПроцедуры\n\n" +
		"Процедура ЗаписатьЧерезОбъект()\n  Д = Объект.Ссылка.ПолучитьОбъект();\n  С = Д.Товары.Добавить();\n  С.Товар = \"из модуля\";\n  Д.Записать();\nКонецПроцедуры\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заказ")
	if err != nil {
		t.Fatal(err)
	}
	tp := metadata.TablePart{Name: "Товары", Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}}}
	ent := &metadata.Entity{Name: "Заказ", Kind: metadata.KindCatalog,
		Fields:     []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{tp}, Forms: []*metadata.FormModule{fm}}
	if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(t.Context(), ent.Name, id, map[string]any{"Наименование": "заказ"}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTablePartRows(t.Context(), ent.Name, tp.Name, id, []map[string]any{{"Товар": "из формы"}}, tp); err != nil {
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

	// Чтения строк ТЧ — через функцию-счётчик в представлении на месте таблицы.
	table := metadata.TablePartTableName(ent.Name, tp.Name)
	columns, err := db.QueryAll(t.Context(), fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		t.Fatal(err)
	}
	var projection []string
	for _, c := range columns {
		name := fmt.Sprint(c["name"])
		if strings.EqualFold(name, "Товар") {
			projection = append(projection, fmt.Sprintf(`%s(%q) AS %q`, probe, name, name))
		} else {
			projection = append(projection, fmt.Sprintf("%q", name))
		}
	}

	fire := func(element string) (string, int64) {
		t.Helper()
		form := url.Values{"_element": {element}, "_event": {"Нажатие"}, "_id": {id.String()},
			"Наименование": {"заказ"}, "tp_json.Товары": {`[{"Товар":"из формы"}]`}}
		req := httptest.NewRequest(http.MethodPost, "/ui/catalog/заказ/form-event", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		reads.Store(0)
		router.ServeHTTP(rec, req)
		var resp struct {
			OK         bool                        `json:"ok"`
			TableParts map[string][]map[string]any `json:"tableparts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK {
			t.Fatalf("form-event %s: %d %s", element, rec.Code, rec.Body.String())
		}
		var names []string
		for _, row := range resp.TableParts["Товары"] {
			names = append(names, fmt.Sprint(row["Товар"]))
		}
		return strings.Join(names, ","), reads.Load()
	}

	// До обработчика строки ТЧ читаются один раз (снимок базы для #579).
	if _, err := db.Exec(t.Context(), fmt.Sprintf(`ALTER TABLE %q RENAME TO "gate_rows"`, table)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), fmt.Sprintf(`CREATE VIEW %q AS SELECT %s FROM "gate_rows"`, table, strings.Join(projection, ","))); err != nil {
		t.Fatal(err)
	}
	rows, idleReads := fire("КнНичего")
	if rows != "из формы" {
		t.Fatalf("обработчик без записи изменил строки ТЧ: %q", rows)
	}
	// Вернём таблицу: обработчик с записью пишет в неё через объект.
	if _, err := db.Exec(t.Context(), fmt.Sprintf(`DROP VIEW %q`, table)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), fmt.Sprintf(`ALTER TABLE "gate_rows" RENAME TO %q`, table)); err != nil {
		t.Fatal(err)
	}
	rows, _ = fire("КнМодуль")
	if !strings.Contains(rows, "из модуля") {
		t.Fatalf("строки, записанные обработчиком через объект, не перечитаны: %q", rows)
	}
	if idleReads > 1 {
		t.Errorf("обработчик без записи: строки ТЧ прочитаны %d раз, ожидалось не больше одного (снимок до обработчика)", idleReads)
	}
}
