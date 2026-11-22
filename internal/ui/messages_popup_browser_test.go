package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Real HTTP, DSL, store, managed form and polling scripts; the browser delays
// only delivery of genuine responses to exercise both arrival orders.
func TestMessagesPopupBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "object.form.yaml")
	yaml := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: PopupProbe
attributes:
  - name: Объект
    type: CatalogRef.PopupProbe
    save: true
    main: true
elements:
  - kind: Кнопка
    name: Notify
    title: {ru: Notify}
    events: {Нажатие: Notify}
`
	source := "Процедура Notify()\n Сообщить(\"same-message\");\nКонецПроцедуры\n"
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "object.form.os"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	form, err := loader.NewManagedFormLoader().LoadFormFile(path, "PopupProbe")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "PopupProbe", Kind: metadata.KindCatalog, Forms: []*metadata.FormModule{form}}
	db, err := storage.ConnectSQLite(ctx, filepath.Join(dir, "popup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFormOpenMode(ctx, storage.FormModeTabs); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.LoadPages([]*page.Page{{Name: "PopupPage"}})
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}, PagePrograms: map[string]*ast.Program{"PopupPage": mustParse(t, `Процедура ПриФормировании(Страница, Параметры) Экспорт
 Страница.КнопкаДействие("Notify", "Notify");
 Страница.КнопкаДействие("Error", "Error");
КонецПроцедуры
Процедура Notify(Страница, Параметры) Экспорт
 Сообщить("page-message");
КонецПроцедуры
Процедура Error(Страница, Параметры) Экспорт
 ВызватьИсключение "page-error";
КонецПроцедуры`)}})
	reg.LoadHomePage(&metadata.HomePage{Messages: metadata.MessagesPopup, MessageSeconds: 10})
	s := New(reg, db, interpreter.New(), nil, Config{}, nil)
	router := chi.NewRouter()
	s.MountStatic(router)
	s.Mount(router)
	server := httptest.NewServer(router)
	defer func() { s.CloseEventStreams(); server.Close(); s.Close() }()
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "messages-popup.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=messages-popup", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("popup browser acceptance: %v", err)
	}
}
