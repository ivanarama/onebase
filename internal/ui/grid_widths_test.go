package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"golang.org/x/net/html"
)

// Ширина колонок таблицы формы за пользователем (замечание 26.10: «Адрес» в
// результатах поиска заявок): POST /ui/_grid-widths сохраняет ширины, следующая
// отрисовка формы отдаёт их в data-sg-widths и показывает «Стандартные
// колонки», пустое значение возвращает ширины формы. Путь — тот же, что у
// браузера: обработчик сохранения и обработчик формы обработки.

func gridWidthsPost(t *testing.T, s *Server, u *auth.User, grid, widths string) int {
	t.Helper()
	body := url.Values{"grid": {grid}, "widths": {widths}}
	req := httptest.NewRequest(http.MethodPost, "/ui/_grid-widths", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.gridWidthsSave(rec, req)
	return rec.Code
}

func gridWidthsRendered(t *testing.T, s *Server, u *auth.User) (widths string, resetHidden bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/processor/ПоискЗаявок", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "ПоискЗаявок")
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), u))
	rec := httptest.NewRecorder()
	s.processorForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("форма обработки: status=%d", rec.Code)
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	host := keyboardFindHTML(doc, func(n *html.Node) bool {
		v, ok := keyboardHTMLAttr(n, "data-sg-tp")
		return n.Type == html.ElementNode && ok && v == "Найденные"
	})
	if host == nil {
		t.Fatal("таблица формы не отрисована")
	}
	widths, _ = keyboardHTMLAttr(host, "data-sg-widths")
	reset := keyboardFindHTML(doc, func(n *html.Node) bool {
		_, ok := keyboardHTMLAttr(n, "data-ob-grid-order-reset")
		return n.Type == html.ElementNode && ok
	})
	if reset == nil {
		t.Fatal("нет кнопки «Стандартные колонки»")
	}
	style, _ := keyboardHTMLAttr(reset, "style")
	return widths, strings.Contains(style, "display:none")
}

func TestGridWidths_SavedPerUserAndRendered(t *testing.T) {
	s, _ := gridOrderTestServer(t)
	anna, oleg := gridOrderUser("anna"), gridOrderUser("oleg")
	const grid = "processor/ПоискЗаявок/ФормаОбъекта/ТЧНайденные"

	if widths, hidden := gridWidthsRendered(t, s, anna); widths != "" || !hidden {
		t.Fatalf("до изменения: widths=%q resetHidden=%v", widths, hidden)
	}
	if code := gridWidthsPost(t, s, anna, grid, `{"Номер":90,"Адрес":400,"Мастер":120}`); code != http.StatusNoContent {
		t.Fatalf("сохранение ширин: code=%d", code)
	}
	if widths, hidden := gridWidthsRendered(t, s, anna); widths != `{"Адрес":400,"Мастер":120,"Номер":90}` || hidden {
		t.Fatalf("после изменения: widths=%q resetHidden=%v", widths, hidden)
	}
	if widths, _ := gridWidthsRendered(t, s, oleg); widths != "" {
		t.Fatalf("ширины одного пользователя достались другому: %q", widths)
	}
	if code := gridWidthsPost(t, s, anna, grid, ""); code != http.StatusNoContent {
		t.Fatalf("сброс ширин: code=%d", code)
	}
	if widths, hidden := gridWidthsRendered(t, s, anna); widths != "" || !hidden {
		t.Fatalf("после сброса: widths=%q resetHidden=%v", widths, hidden)
	}
}

// Ключ проверяется по метаданным и правам, как у порядка колонок, а ширина —
// по разумным границам: иначе в _settings можно было бы писать что угодно.
func TestGridWidths_RejectsUnknownGridAndBadWidths(t *testing.T) {
	s, _ := gridOrderTestServer(t)
	anna := gridOrderUser("anna")
	const grid = "processor/ПоискЗаявок/ФормаОбъекта/ТЧНайденные"
	if code := gridWidthsPost(t, s, anna, "processor/ПоискЗаявок/ФормаОбъекта/НетТакой", `{"Номер":90}`); code != http.StatusNotFound {
		t.Errorf("чужая таблица: code=%d, ожидался 404", code)
	}
	stranger := &auth.User{Login: "stranger", Roles: []*auth.Role{{Permissions: auth.Permission{
		Processors: map[string][]string{},
	}}}}
	if code := gridWidthsPost(t, s, stranger, grid, `{"Номер":90}`); code != http.StatusNotFound {
		t.Errorf("без права запуска обработки: code=%d, ожидался 404", code)
	}
	for _, bad := range []string{`["Номер"]`, `{"Номер":5}`, `{"Номер":5000}`, `{"":100}`, `{"Номер":"100"}`} {
		if code := gridWidthsPost(t, s, anna, grid, bad); code != http.StatusBadRequest {
			t.Errorf("widths=%s: code=%d, ожидался 400", bad, code)
		}
	}
}
