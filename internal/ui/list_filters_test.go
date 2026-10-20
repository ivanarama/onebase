package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// Панель «Отбор» с основным набором (list_filters): сущность из YAML, список
// и сохранение выбора — через HTTP-обработчики, как у пользователя.

func loadListFiltersDoc(t *testing.T, withKey bool) *metadata.Entity {
	t.Helper()
	body := `name: Заявка
fields:
  - {name: Номер, type: string}
  - {name: Дата, type: date}
  - {name: Автор, type: string}
  - {name: Бренд, type: string}
  - {name: Модель, type: string}
  - {name: Примечание, type: string}
`
	if withKey {
		body += "list_filters: [Номер, Дата, Автор]\n"
	}
	p := filepath.Join(t.TempDir(), "Заявка.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ent, err := metadata.LoadFile(p, metadata.KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	return ent
}

func listFiltersUser(login string) *auth.User {
	return &auth.User{Login: login, Roles: []*auth.Role{{Permissions: auth.Permission{
		Documents: map[string][]string{"Заявка": {"read"}},
	}}}}
}

// listFilterPanel — имена полей панели «Отбор» (по name инпутов f.*) и
// отмеченные/неотмеченные пункты «+ Отбор».
func listFilterPanel(t *testing.T, s *Server, u *auth.User, query string) (panel []string, choices map[string]bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/document/заявка"+query, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", "заявка")
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, rctx), u))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("список: code=%d body=%s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	choices = map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "select") {
			name, _ := keyboardHTMLAttr(n, "name")
			typ, _ := keyboardHTMLAttr(n, "type")
			if strings.HasPrefix(name, "f.") && typ != "hidden" {
				field := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, "f."), ".from"), ".to")
				if !seen[field] {
					seen[field] = true
					panel = append(panel, field)
				}
			}
			if name == "field" {
				v, _ := keyboardHTMLAttr(n, "value")
				_, on := keyboardHTMLAttr(n, "checked")
				choices[v] = on
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return panel, choices
}

func listFiltersPost(t *testing.T, s *Server, u *auth.User, entity, back string, fields ...string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"entity": {entity}, "back": {back}, "field": fields}
	req := httptest.NewRequest(http.MethodPost, "/ui/_list-filters", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	s.listFilterFieldsSave(rec, req)
	return rec
}

// location — адрес перенаправления без %-кодирования кириллицы.
func location(rec *httptest.ResponseRecorder) string {
	loc, err := url.PathUnescape(rec.Header().Get("Location"))
	if err != nil {
		return rec.Header().Get("Location")
	}
	return loc
}

func TestListFilters_BasicSetAndUserAdditions(t *testing.T) {
	doc := loadListFiltersDoc(t, true)
	s, _ := newSubmitTestServer(t, []*metadata.Entity{doc})
	anna, oleg := listFiltersUser("anna"), listFiltersUser("oleg")

	panel, choices := listFilterPanel(t, s, anna, "")
	if strings.Join(panel, ",") != "Номер,Дата,Автор" {
		t.Fatalf("основной набор: панель %v", panel)
	}
	if len(choices) != 3 || choices["Бренд"] || choices["Модель"] || choices["Примечание"] {
		t.Fatalf("«+ Отбор» должен предлагать остальные поля без отметок: %v", choices)
	}

	rec := listFiltersPost(t, s, anna, "Заявка", "/ui/document/заявка?q=x", "Модель")
	if rec.Code != http.StatusSeeOther || location(rec) != "/ui/document/заявка?q=x" {
		t.Fatalf("сохранение: code=%d location=%q", rec.Code, location(rec))
	}
	panel, choices = listFilterPanel(t, s, anna, "")
	if strings.Join(panel, ",") != "Номер,Дата,Автор,Модель" || !choices["Модель"] {
		t.Fatalf("после добавления: панель %v, отметки %v", panel, choices)
	}
	if panel, _ = listFilterPanel(t, s, oleg, ""); strings.Join(panel, ",") != "Номер,Дата,Автор" {
		t.Fatalf("выбор одного пользователя достался другому: %v", panel)
	}

	// Действующий отбор по полю вне панели виден — иначе фильтр «спрятан».
	if panel, _ = listFilterPanel(t, s, oleg, "?f.Примечание=срочно"); strings.Join(panel, ",") != "Номер,Дата,Автор,Примечание" {
		t.Fatalf("активный отбор не показан: %v", panel)
	}

	if rec = listFiltersPost(t, s, anna, "Заявка", "/ui/document/заявка"); rec.Code != http.StatusSeeOther {
		t.Fatalf("сброс: code=%d", rec.Code)
	}
	if panel, _ = listFilterPanel(t, s, anna, ""); strings.Join(panel, ",") != "Номер,Дата,Автор" {
		t.Fatalf("после снятия отметок: %v", panel)
	}
}

func TestListFilters_WithoutKeyShowsAllFields(t *testing.T) {
	doc := loadListFiltersDoc(t, false)
	s, _ := newSubmitTestServer(t, []*metadata.Entity{doc})
	panel, choices := listFilterPanel(t, s, listFiltersUser("anna"), "")
	if strings.Join(panel, ",") != "Номер,Дата,Автор,Бренд,Модель,Примечание" || len(choices) != 0 {
		t.Fatalf("без list_filters — все поля и без «+ Отбор»: %v %v", panel, choices)
	}
	if rec := listFiltersPost(t, s, listFiltersUser("anna"), "Заявка", "/ui/document/заявка", "Модель"); rec.Code != http.StatusNotFound {
		t.Fatalf("сохранение без list_filters: code=%d, ожидался 404", rec.Code)
	}
}

func TestListFilters_RejectsBadInput(t *testing.T) {
	doc := loadListFiltersDoc(t, true)
	s, _ := newSubmitTestServer(t, []*metadata.Entity{doc})
	anna := listFiltersUser("anna")
	if rec := listFiltersPost(t, s, anna, "Заявка", "/ui/document/заявка", "НетТакого"); rec.Code != http.StatusBadRequest {
		t.Errorf("неизвестное поле: code=%d", rec.Code)
	}
	if rec := listFiltersPost(t, s, anna, "Заявка", "/ui/document/заявка", "Номер"); rec.Code != http.StatusBadRequest {
		t.Errorf("поле основного набора как дополнительное: code=%d", rec.Code)
	}
	if rec := listFiltersPost(t, s, anna, "НетСущности", "/ui/x"); rec.Code != http.StatusNotFound {
		t.Errorf("неизвестная сущность: code=%d", rec.Code)
	}
	stranger := &auth.User{Login: "stranger", Roles: []*auth.Role{{Permissions: auth.Permission{Documents: map[string][]string{}}}}}
	if rec := listFiltersPost(t, s, stranger, "Заявка", "/ui/document/заявка", "Модель"); rec.Code != http.StatusNotFound {
		t.Errorf("без права чтения: code=%d", rec.Code)
	}
	for _, back := range []string{"https://evil.example/", "//evil.example/ui/", "/ui\\..\\x", "javascript:alert(1)"} {
		rec := listFiltersPost(t, s, anna, "Заявка", back, "Модель")
		if loc := location(rec); loc != "/ui/document/заявка" {
			t.Errorf("адрес возврата %q не заменён: %q", back, loc)
		}
	}
}
