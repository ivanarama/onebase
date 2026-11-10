package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// density: compact ставит класс плотной раскладки на <html> документа формы —
// только этой формы; без ключа страница прежняя. density: dense добавляет к
// нему свой класс: правила compact остаются в силе, dense их уточняет, а форма
// с compact ничего от dense не получает. Карточка открыта штатным
// обработчиком формы.
func TestManagedFormDensityClass(t *testing.T) {
	for _, tc := range []struct {
		density string
		html    string // ожидаемое начало документа
	}{
		{"compact", `<html lang="ru" class="ob-density-compact">`},
		{"dense", `<html lang="ru" class="ob-density-compact ob-density-dense">`},
		{"", `<html lang="ru"><head>`},
	} {
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
		if !strings.Contains(body, tc.html) {
			t.Errorf("density=%q: на <html> ожидалось %s", tc.density, tc.html)
		}
		if !strings.Contains(body, `.ob-density-compact input[type=text]`) || !strings.Contains(body, `.ob-density-dense input[type=text]`) {
			t.Error("правила плотной раскладки не попали в страницу формы")
		}
		// dense перекрывает compact только порядком при равной специфичности:
		// его правила обязаны идти позже.
		if strings.Index(body, `.ob-density-dense .form-group{`) < strings.Index(body, `.ob-density-compact .form-group{`) {
			t.Error("правила dense стоят раньше compact и не перекроют его")
		}
	}
}
