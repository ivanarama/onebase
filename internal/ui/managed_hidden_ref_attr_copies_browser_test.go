package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Real YAML and public GET, picker, event and save routes exercise a form-local
// reference, including the sessionStorage restore after the save redirect.
// The existing CI entry point runs this fixture on both database dialects.
func testHiddenRefAttrCopiesBrowserAcceptance(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		warehouse := &metadata.Entity{Name: "Склад", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{warehouse}); err != nil {
			t.Fatal(err)
		}
		refIDs := map[string]string{}
		for _, label := range []string{"Исходный склад", "Выбранный склад"} {
			id := uuid.New()
			if err := db.Upsert(ctx, warehouse.Name, id, map[string]any{"Наименование": label}, warehouse); err != nil {
				t.Fatal(err)
			}
			refIDs[label] = id.String()
		}
		entities := []*metadata.Entity{warehouse}
		ids := map[string]string{}
		for _, reverse := range []bool{false, true} {
			name := fmt.Sprintf("Ссылки%t", reverse)
			first := `  - kind: ПолеВвода
    name: После
    data_path: Склад
    hidden_when: 'Режим <> "Принята"'
    readonly_when: 'Заблокирован = Истина'
`
			second := `  - kind: ПолеВвода
    name: До
    data_path: Склад
    hidden_when: 'Режим = "Принята"'
    readonly_when: 'Заблокирован = Истина'
`
			if reverse {
				first, second = second, first
			}
			yaml := fmt.Sprintf(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: %s
attributes:
  - name: Объект
    type: CatalogRef.%s
    save: true
    main: true
  - name: Склад
    type: CatalogRef.Склад
    save: false
elements:
  - kind: ПолеВвода
    name: Режим
    data_path: Объект.Режим
  - kind: ПолеВвода
    name: Получено
    data_path: Объект.Получено
%s%s  - kind: Кнопка
    name: Проверить
    title: {ru: Проверить}
    events: {Нажатие: Проверить}
  - kind: Кнопка
    name: Переключить
    title: {ru: Переключить}
    events: {Нажатие: Переключить}
  - kind: Кнопка
    name: Заблокировать
    title: {ru: Заблокировать}
    events: {Нажатие: Заблокировать}
`, name, name, first, second)
			source := `Процедура Проверить()
 Если ЗначениеЗаполнено(Объект.Склад) Тогда
  Объект.Получено = Объект.Склад.Наименование;
 Иначе
  Объект.Получено = "Пусто";
 КонецЕсли;
КонецПроцедуры
Процедура Переключить()
 Проверить();
 Если Объект.Режим = "Принята" Тогда
  Объект.Режим = "Черновик";
 Иначе
  Объект.Режим = "Принята";
 КонецЕсли;
КонецПроцедуры
Процедура Заблокировать()
 Объект.Заблокирован = Истина;
КонецПроцедуры
`
			dir := t.TempDir()
			path := filepath.Join(dir, "объекта.form.yaml")
			if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "объекта.form.os"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			form, err := loader.NewManagedFormLoader().LoadFormFile(path, name)
			if err != nil {
				t.Fatal(err)
			}
			entity := &metadata.Entity{Name: name, Kind: metadata.KindCatalog, Forms: []*metadata.FormModule{form}, Fields: []metadata.Field{
				{Name: "Режим", Type: metadata.FieldTypeString}, {Name: "Получено", Type: metadata.FieldTypeString}, {Name: "Заблокирован", Type: metadata.FieldTypeBool},
			}}
			if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			if err := db.Upsert(ctx, name, id, map[string]any{"Режим": "Черновик", "Заблокирован": false}, entity); err != nil {
				t.Fatal(err)
			}
			ids[name] = id.String()
			entities = append(entities, entity)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities})
		s := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		s.MountStatic(router)
		s.Mount(router)
		server := httptest.NewServer(router)
		defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
		fixture, err := json.Marshal(map[string]any{"ids": ids, "refs": refIDs})
		if err != nil {
			t.Fatal(err)
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "hidden-ref-attrs.spec.js")
		cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
		cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=hidden-ref-attrs", "OB_ACCEPTANCE_URL="+server.URL, "OB_HIDDEN_REF_ATTR_FIXTURE="+string(fixture))
		output, err := cmd.CombinedOutput()
		t.Logf("%s", output)
		if err != nil {
			t.Fatalf("hidden reference attributes browser: %v", err)
		}
		// save:false stays out of persistent metadata and the storage row.
		for name, id := range ids {
			row, err := db.GetByID(ctx, name, uuid.MustParse(id), reg.GetEntity(name))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := row["Склад"]; ok {
				t.Fatal("form-local reference became persistent")
			}
		}
	})
}
