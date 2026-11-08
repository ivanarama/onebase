package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// obFire consumes an actual saving DSL handler response over HTTP. A page
// from the previous process must adopt the saved identity before fencing every
// further write; the same-boot control continues with that identity normally.
func TestManagedFormRestartPreservesCommittedWrite(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the form restart HTTP regression")
	}
	srv, ent := setupManagedEventsServer(t, `
Процедура Записать(Элемент)
	Если НЕ ЗначениеЗаполнено(Объект.Ссылка) Тогда
		Объект.Записать();
	КонецЕсли;
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementButton, Name: "Записать",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Записать"},
	}})
	router := chi.NewRouter()
	var requests atomic.Int32
	router.Post("/ui/{kind}/{entity}/form-event", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		srv.handleManagedFormEvent(w, r)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", "--test-name-pattern", "^restart preserves committed identity", "static/managed_close_intent_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(), "OB_RESTART_TEST_URL="+server.URL+"/ui/catalog/"+ent.Name+"/form-event", "OB_RESTART_TEST_BOOT="+uiBootID)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart HTTP regression: %v\n%s", err, output)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("form-event requests = %d, want 2 same-boot and 1 changed-boot", got)
	}
	rows, err := srv.store.List(ctx, ent.Name, ent, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("saved rows = %d, want one per page", len(rows))
	}
}

// Страница формы не кэшируется браузером и несёт метку запуска сервера, а
// ответ события формы — тот же заголовок: «Назад» не поднимает старый DOM
// из bfcache, а форма, открытая до перезапуска сервера, узнаёт об этом.
func TestManagedFormPage_NoStoreAndBootID(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "X"}, ent); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/ui/catalog/" + ent.Name + "/" + id.String(), "/ui/catalog/" + ent.Name + "/new"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("kind", "catalog")
		rctx.URLParams.Add("entity", ent.Name)
		if !strings.HasSuffix(path, "/new") {
			rctx.URLParams.Add("id", id.String())
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		if strings.HasSuffix(path, "/new") {
			srv.form(rec, req)
		} else {
			srv.formEdit(rec, req)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("%s: Cache-Control = %q, want private, no-store", path, got)
		}
		if got := rec.Header().Get("X-OB-Boot"); got != uiBootID || got == "" {
			t.Errorf("%s: X-OB-Boot = %q, want %q", path, got, uiBootID)
		}
		if !strings.Contains(rec.Body.String(), `<meta name="ob-boot" content="`+uiBootID+`">`) {
			t.Errorf("%s: нет <meta name=\"ob-boot\"> с меткой запуска", path)
		}
	}

	body := url.Values{"_id": {id.String()}, "_kind": {"object"}, "_element": {"Наименование"}, "_event": {string(metadata.FormEventOnChange)}}
	req := reqWithChi(http.MethodPost, "/ui/catalog/"+ent.Name+"/form-event", body, map[string]string{"entity": ent.Name})
	rec := httptest.NewRecorder()
	srv.handleManagedFormEvent(rec, req)
	if got := rec.Header().Get("X-OB-Boot"); got != uiBootID {
		t.Errorf("form-event: X-OB-Boot = %q, want %q", got, uiBootID)
	}
}

// Обработчик, сам выставивший Cache-Control, его сохраняет.
func TestMarkFreshPage_KeepsExplicitCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Cache-Control", "public, max-age=60")
	markFreshPage(rec)
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("Cache-Control перетёрт: %q", got)
	}
}
