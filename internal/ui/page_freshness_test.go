package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
)

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
