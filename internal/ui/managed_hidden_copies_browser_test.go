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

// The same browser scenario uses real YAML, GET, event, native FormData, save
// and read routes on both dialects. No private JS function or mocked response
// stands in for the event pipeline (the delayed response is fetched unchanged).
func TestHiddenCopiesBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	t.Run("files", testHiddenFileCopiesBrowserAcceptance)
	t.Run("references", testHiddenRefAttrCopiesBrowserAcceptance)
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		var entities []*metadata.Entity
		ids := map[string]string{}
		for _, reverse := range []bool{false, true} {
			name := fmt.Sprintf("Копии%t", reverse)
			dir := t.TempDir()
			first := `  - kind: ПолеВвода
    name: До
    data_path: Объект.Текст
    hidden_when: 'Режим = "Принята"'
`
			second := `  - kind: ПолеВвода
    name: После
    data_path: Объект.Текст
    hidden_when: 'Режим <> "Принята"'
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
elements:
  - kind: ПолеВвода
    name: Режим
    data_path: Объект.Режим
%s%s  - kind: Кнопка
    name: Переключить
    title: {ru: Переключить}
    events: {Нажатие: Переключить}
  - kind: Кнопка
    name: Серверный
    title: {ru: Серверный}
    events: {Нажатие: Серверный}
`, name, name, first, second)
			source := `Процедура Переключить()
 Если Объект.Режим = "Принята" Тогда
  Объект.Режим = "Черновик";
 Иначе
  Объект.Режим = "Принята";
 КонецЕсли;
КонецПроцедуры
Процедура Серверный()
 Объект.Текст = "Серверное";
 Переключить();
КонецПроцедуры
`
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
			entity := &metadata.Entity{Name: name, Kind: metadata.KindCatalog, Fields: []metadata.Field{
				{Name: "Текст", Type: metadata.FieldTypeString}, {Name: "Режим", Type: metadata.FieldTypeString},
			}, Forms: []*metadata.FormModule{form}}
			entities = append(entities, entity)
			if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			if err := db.Upsert(ctx, name, id, map[string]any{"Текст": "", "Режим": "Черновик"}, entity); err != nil {
				t.Fatal(err)
			}
			ids[name] = id.String()
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: entities})
		s := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		s.MountStatic(router)
		s.Mount(router)
		server := httptest.NewServer(router)
		defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
		idsJSON, err := json.Marshal(ids)
		if err != nil {
			t.Fatal(err)
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "hidden-copies.spec.js")
		cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
		cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=hidden-copies", "OB_ACCEPTANCE_URL="+server.URL, "OB_HIDDEN_COPY_IDS="+string(idsJSON))
		output, err := cmd.CombinedOutput()
		t.Logf("%s", output)
		if err != nil {
			t.Fatalf("hidden copies browser: %v", err)
		}
	})
}
