package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// density: compact ставит класс плотной раскладки на <html> документа формы —
// только этой формы; без ключа страница прежняя. Карточка открыта штатным
// обработчиком формы.
func TestManagedFormDensityClass(t *testing.T) {
	for _, tc := range []struct {
		density string
		want    bool
	}{{"compact", true}, {"", false}} {
		ent := layoutTestEntity(fieldEl("ПолеНаим", "Объект.Наименование"))
		ent.Forms[0].Density = tc.density
		srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
		req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil, map[string]string{"entity": ent.Name})
		rec := httptest.NewRecorder()
		srv.form(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("form: %d %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		has := strings.Contains(body, `<html lang="ru" class="ob-density-compact">`)
		if has != tc.want {
			t.Errorf("density=%q: класс плотной раскладки на <html> = %v, ожидалось %v", tc.density, has, tc.want)
		}
		if !strings.Contains(body, `.ob-density-compact input[type=text]`) {
			t.Error("правила плотной раскладки не попали в страницу формы")
		}
	}
}
