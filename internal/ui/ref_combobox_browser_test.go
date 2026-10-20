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

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
)

// This test drives the actual HTTP form, scripts, browser validation and save.
// npm ci in e2e/ and Playwright Chromium are required; no DOM stub is used.
func TestManagedRefComboboxBrowser(t *testing.T) {
	t.Run("required", func(t *testing.T) { testManagedRefComboboxBrowser(t, true) })
	t.Run("optional-close", func(t *testing.T) { testManagedRefComboboxBrowser(t, false) })
}

func testManagedRefComboboxBrowser(t *testing.T, required bool) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("ONEBASE_REQUIRE_REF_BROWSER") == "1" {
			t.Fatal(err)
		}
		t.Skip("node required for browser acceptance")
	}
	if _, err := os.Stat(filepath.Join("..", "..", "e2e", "node_modules", "playwright")); err != nil {
		if os.Getenv("ONEBASE_REQUIRE_REF_BROWSER") == "1" {
			t.Fatal(err)
		}
		t.Skip("run npm ci in e2e/ for browser acceptance")
	}
	f := newChoiceHTTPFixtureWithOwner(t, true)
	if err := f.server.store.Upsert(context.Background(), f.target.Name, f.pageTwo, map[string]any{metadata.StandardOwnerField: f.rootA.String()}, f.target); err != nil {
		t.Fatal(err)
	}
	if err := f.server.store.EnsureServiceSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	form := f.owner.Forms[0]
	f.owner.Fields = append(f.owner.Fields, metadata.Field{Name: "Запрет", Type: metadata.FieldTypeBool}, metadata.Field{Name: "Скрыть", Type: metadata.FieldTypeBool})
	f.owner.Fields[1].Required = required
	form.Elements[1].ReadOnlyWhen = "Запрет"
	form.Elements[1].HiddenWhen = "Скрыть"
	form.Elements[1].Required = required
	form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: "Просмотр", TypeRef: "CatalogRef." + f.target.Name})
	form.Elements = append(form.Elements,
		&metadata.FormElement{ID: "lock", Name: "ПолеЗапрет", Kind: metadata.FormElementCheckbox, DataPath: "Объект.Запрет", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Обновить"}},
		&metadata.FormElement{ID: "hide", Name: "ПолеСкрыть", Kind: metadata.FormElementCheckbox, DataPath: "Объект.Скрыть", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "Обновить"}},
		&metadata.FormElement{ID: "echo", Name: "Повторить", Kind: metadata.FormElementButton, TitleMap: map[string]string{"ru": "Повторить"}, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Обновить"}},
		&metadata.FormElement{ID: "readonly", Name: "Просмотр", Kind: metadata.FormElementField, DataPath: "Форма.Просмотр", ReadOnly: true},
		&metadata.FormElement{ID: "fill", Name: "Заполнить", Kind: metadata.FormElementButton, TitleMap: map[string]string{"ru": "Заполнить"}, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Заполнить"}},
	)
	for _, branch := range []string{"A", "B"} {
		form.Elements = append(form.Elements, &metadata.FormElement{ID: "source" + branch, Name: "Источник" + branch, Kind: metadata.FormElementButton, TitleMap: map[string]string{"ru": "Источник" + branch}, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Источник" + branch}})
	}
	form.ProgramAST = mustParse(t, `
Процедура Заполнить()
 Объект.Неисправность = Справочники.Неисправность.НайтиПоНаименованию("element 051");
КонецПроцедуры
Процедура ИсточникA()
 Объект.Направление = Справочники.Направление.НайтиПоНаименованию("A");
КонецПроцедуры
Процедура ИсточникB()
 Объект.Направление = Справочники.Направление.НайтиПоНаименованию("B");
КонецПроцедуры
Процедура Обновить()
КонецПроцедуры
`)
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.direction, f.target, f.owner}); err != nil {
		t.Fatal(err)
	}
	f.server.interp = interpreter.New()
	f.server.interp.LookupProc = f.server.reg.GetModuleProc
	f.server.lockMgr = runtime.NewLockManager()
	f.server.hub = realtime.NewHub()
	f.server.messages = NewMessageStore()
	f.server.entitySvc = f.server.newEntityService(nil)
	router := chi.NewRouter()
	f.server.MountStatic(router)
	f.server.Mount(router)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), f.user)))
	}))
	defer server.Close()
	// Exercise inline create through its real public iframe on a simple catalog.
	inline, order, _ := setupRefOptionEventServer(t)
	enabled := true
	order.Fields[1].AllowInlineCreate = &enabled
	order.Forms[0].Elements = append(order.Forms[0].Elements, &metadata.FormElement{ID: "warehouse", Name: "ПолеСклад", Kind: metadata.FormElementField, DataPath: "Объект.Склад"})
	inline.hub = realtime.NewHub()
	if err := inline.store.EnsureServiceSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	inlineRouter := chi.NewRouter()
	inline.MountStatic(inlineRouter)
	inline.Mount(inlineRouter)
	inlineServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inlineRouter.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{Login: "root", IsAdmin: true})))
	}))
	defer inlineServer.Close()
	payload, _ := json.Marshal(map[string]any{"optional": !required, "base": server.URL, "inlineBase": inlineServer.URL, "id": f.ownerID.String(), "rootA": f.rootA.String(), "rootB": f.rootB.String(), "pageTwo": f.pageTwo.String(), "legacy": f.legacySelected.String()})
	cmd := exec.CommandContext(t.Context(), node, "static/ref_combobox_browser_test.js") //nolint:gosec // local test executable
	cmd.Env = append(os.Environ(), "ONEBASE_REF_BROWSER="+string(payload))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser acceptance: %v\n%s", err, out)
	}
	t.Log(string(out))
}
