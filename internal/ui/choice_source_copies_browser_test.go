package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Invoked by the existing CI browser entry point. Production managed HTML,
// ui.js, form-event and ref-options run unchanged on both database dialects.
func TestChoiceSourceCopiesBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceHTTPFixtureWithStore(t, db, true)
		// Source values are ordinary catalog items, available in both copies.
		for _, id := range []uuid.UUID{f.rootA, f.rootB} {
			if err := db.Upsert(context.Background(), f.direction.Name, id, map[string]any{"is_folder": false}, f.direction); err != nil {
				t.Fatal(err)
			}
		}
		f.owner.Fields = append(f.owner.Fields,
			metadata.Field{Name: "Режим", Type: metadata.FieldTypeString},
			metadata.Field{Name: "Заблокирован", Type: metadata.FieldTypeBool})
		dir := t.TempDir()
		yaml := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
attributes:
  - name: Объект
    type: DocumentRef.Заявка
    main: true
    save: true
  - name: ЛокальнаяНеисправность
    type: CatalogRef.Неисправность
    save: false
elements:
  - kind: ПолеВвода
    name: Режим
    data_path: Объект.Режим
  - kind: ПолеВвода
    id: source-before
    name: До
    data_path: Объект.Направление
    choice_folders: true
    hidden_when: 'Режим = "second" ИЛИ Режим = "none"'
    readonly_when: 'Заблокирован = Истина'
  - kind: ПолеВвода
    id: source-after
    name: После
    data_path: Объект.Направление
    choice_folders: true
    hidden_when: 'Режим = "first" ИЛИ Режим = "none"'
    readonly_when: 'Заблокирован = Истина'
  - kind: ПолеВвода
    id: fault-picker
    name: Отобранная
    data_path: Объект.Неисправность
    choice_filter:
      - field: Направление
        op: in_hierarchy
        from: Объект.Направление
    choice_context: {Направление: Объект.Направление}
  - kind: ПолеВвода
    name: Подчинённая
    data_path: ЛокальнаяНеисправность
  - kind: Кнопка
    name: Применить
    title: {ru: Применить}
    events: {Нажатие: Применить}
  - kind: Кнопка
    name: Заблокировать
    title: {ru: Заблокировать}
    events: {Нажатие: Заблокировать}
`
		source := `Процедура Применить()
 Объект.Режим = Объект.Режим;
КонецПроцедуры
Процедура Заблокировать()
 Объект.Заблокирован = Истина;
КонецПроцедуры
`
		path := filepath.Join(dir, "объекта.form.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "объекта.form.os"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		form, err := loader.NewManagedFormLoader().LoadFormFile(path, f.owner.Name)
		if err != nil {
			t.Fatal(err)
		}
		f.owner.Forms = []*metadata.FormModule{form}
		if err := db.Migrate(context.Background(), []*metadata.Entity{f.owner}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(context.Background(), f.owner.Name, f.ownerID, map[string]any{"Режим": "first", "Заблокирован": false}, f.owner); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{f.direction, f.target, f.owner}})
		s := New(reg, db, interpreter.New(), nil, Config{}, nil)
		router := chi.NewRouter()
		s.MountStatic(router)
		router.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), f.user)))
				})
			})
			s.Mount(r)
		})
		server := httptest.NewServer(router)
		defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
		fixture, err := json.Marshal(map[string]any{"a": f.rootA, "b": f.rootB, "owner": f.ownerID, "hidden": f.hidden})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "choice-source-copies.spec.js")
		cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
		cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=choice-source-copies", "OB_ACCEPTANCE_URL="+server.URL, "OB_CHOICE_SOURCE_IDS="+string(fixture))
		output, err := cmd.CombinedOutput()
		t.Logf("%s", output)
		if err != nil {
			t.Fatalf("choice source copies browser: %v", err)
		}
	})
}
