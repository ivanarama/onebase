package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Новая карточка сообщает вкладке оболочки заголовок, как в 1С:
// «<синоним> (создание)». Раньше <meta name="ob-tab-title"> была только у
// существующей записи, и вкладка новой карточки оставалась с заголовком
// ссылки, открывшей её, — у кнопки «Создать» это подсказка «Insert».
func TestManagedForm_NewCardTabTitle(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, ``, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Наименование", DataPath: "Объект.Наименование"},
	})
	get := func() string {
		t.Helper()
		req := httptest.NewRequest("GET", "/ui/catalog/"+ent.Name+"/new", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("kind", "catalog")
		rctx.URLParams.Add("entity", ent.Name)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		srv.form(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ожидался 200, получен %d", rec.Code)
		}
		return rec.Body.String()
	}

	if body := get(); !strings.Contains(body, `<meta name="ob-tab-title" content="`+ent.Name+` (создание)">`) {
		t.Errorf("без синонима: нет <meta ob-tab-title> «%s (создание)» на новой карточке", ent.Name)
	}
	ent.Title = "Звонок"
	if body := get(); !strings.Contains(body, `<meta name="ob-tab-title" content="Звонок (создание)">`) {
		t.Errorf("нет <meta ob-tab-title> «Звонок (создание)» — заголовок вкладки новой карточки берётся из синонима")
	}
}
