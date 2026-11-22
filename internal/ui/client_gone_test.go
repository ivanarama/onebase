package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/incident"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Подбор ссылки, брошенный клиентом (новый запрос окна выбора отменил прежний),
// раньше отвечал 500 и регистрировал инцидент: на рабочей базе — 17–18
// «ошибок» в день при nginx 499 на тех же запросах. Через публичный
// GET /ui/_ref-options/{entity}.
func TestRefOptions_ClientCanceledIs499WithoutIncident(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, "", nil, nil)
	srv.incidents = incident.NewStore(10)

	call := func(ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+ent.Name+"?limit=50", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("entity", ent.Name)
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.refOptionsJSON(rec, req)
		return rec
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if rec := call(canceled); rec.Code != statusClientClosedRequest {
		t.Fatalf("отменённый клиентом запрос: status %d, ждали 499; body=%s", rec.Code, rec.Body.String())
	}
	if got := srv.incidents.Recent("", 10); len(got) != 0 {
		t.Fatalf("отмена клиентом зарегистрирована инцидентом: %+v", got)
	}

	// Настоящий сбой при живом клиенте по-прежнему 500 с инцидентом.
	if _, err := srv.store.Exec(context.Background(), "DROP TABLE "+metadata.TableName(ent.Name)); err != nil {
		t.Fatal(err)
	}
	if rec := call(context.Background()); rec.Code != http.StatusInternalServerError {
		t.Fatalf("сбой хранилища: status %d, ждали 500", rec.Code)
	}
	if got := srv.incidents.Recent("", 10); len(got) != 1 {
		t.Fatalf("сбой хранилища без инцидента: %+v", got)
	}
}
