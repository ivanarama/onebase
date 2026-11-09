package ui

import (
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

// ONEBASE_SERVER_TIMING=1: открытие формы и событие формы отдают Server-Timing
// с фазами (обработчик, списки выбора, состояния, отрисовка) и total; без
// переменной заголовка нет. Путь: YAML + .form.os → зарегистрированный маршрут.
func TestFormServerTimingHeader(t *testing.T) {
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "st.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	yaml := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Заявка\n" +
		"events:\n  ПриОткрытии: ПриОткрытии\nelements:\n  - kind: ПолеВвода\n    name: ПолеНаим\n    data_path: Объект.Наименование\n"
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ФормаОбъекта.form.os"), []byte("Процедура ПриОткрытии()\n  Объект.Наименование = \"открыта\";\nКонецПроцедуры\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fm, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "ФормаОбъекта.form.yaml"), "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	ent := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{fm}}
	if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
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
	call := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		var req *http.Request
		if form != nil {
			req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	event := url.Values{"_element": {""}, "_event": {"ПриОткрытии"}}

	if h := call("GET", "/ui/catalog/заявка/new", nil).Header().Get("Server-Timing"); h != "" {
		t.Fatalf("без ONEBASE_SERVER_TIMING заголовка быть не должно: %q", h)
	}
	t.Setenv("ONEBASE_SERVER_TIMING", "1")
	page := call("GET", "/ui/catalog/заявка/new", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("form: %d", page.Code)
	}
	if h := page.Header().Get("Server-Timing"); !strings.Contains(h, "render;dur=") || !strings.Contains(h, "total;dur=") {
		t.Errorf("Server-Timing открытия формы: %q", h)
	}
	ev := call("POST", "/ui/catalog/заявка/form-event", event)
	if ev.Code != http.StatusOK || !strings.Contains(ev.Body.String(), "открыта") {
		t.Fatalf("form-event: %d %s", ev.Code, ev.Body.String())
	}
	h := ev.Header().Get("Server-Timing")
	for _, want := range []string{"dsl;dur=", "elementStates;dur=", "total;dur="} {
		if !strings.Contains(h, want) {
			t.Errorf("Server-Timing события формы без %q: %q", want, h)
		}
	}
}
