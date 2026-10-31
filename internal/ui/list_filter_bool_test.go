package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Панель «Отбор» списка: булев реквизит — выбор «— все — / Да / Нет», а не
// текстовое поле, и отбор по нему находит записи. Раньше поле было текстовым,
// а введённое «Да» искалось подстрокой и на SQLite не находило ничего.
func TestUI_EntityList_BoolFilter(t *testing.T) {
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Выполнена", Type: metadata.FieldTypeBool},
		},
	}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{задача})
	for _, row := range []map[string]any{
		{"Наименование": "Позвонить клиенту", "Выполнена": true},
		{"Наименование": "Проверить отказ", "Выполнена": false},
	} {
		if err := s.store.Upsert(ctx, задача.Name, uuid.New(), row, задача); err != nil {
			t.Fatal(err)
		}
	}
	список := func(query string) string {
		t.Helper()
		target := "/ui/catalog/" + задача.Name
		if query != "" {
			target += "?" + query
		}
		req := reqWithChi(http.MethodGet, target, nil, map[string]string{"kind": "catalog", "entity": задача.Name})
		req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "root", Login: "admin", IsAdmin: true}))
		rec := httptest.NewRecorder()
		s.list(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	html := список("")
	if !strings.Contains(html, `<select name="f.Выполнена">`) {
		t.Fatalf("булев реквизит в панели «Отбор» — не выбор Да/Нет:\n%s", filterPanelFragment(html, "f.Выполнена"))
	}
	if strings.Contains(html, `<input type="text" name="f.Выполнена"`) {
		t.Fatal("булев реквизит остался текстовым полем")
	}

	да := список("f.Выполнена=true")
	if !strings.Contains(да, "Позвонить клиенту") || strings.Contains(да, "Проверить отказ") {
		t.Fatal("отбор «Да»: только выполненная задача")
	}
	if !strings.Contains(да, `<option value="true" selected>`) {
		t.Fatalf("выбранное «Да» не отмечено в панели:\n%s", filterPanelFragment(да, "f.Выполнена"))
	}

	нет := список("f.Выполнена=false")
	if strings.Contains(нет, "Позвонить клиенту") || !strings.Contains(нет, "Проверить отказ") {
		t.Fatal("отбор «Нет»: только невыполненная задача")
	}

	// Ревью #2184: старый адрес с «нет»/«да»/1/0 хранилище понимает — панель
	// обязана показать то же значение выбранным, иначе «Применить» отправит
	// пустое и молча вернёт весь список. Повторное применение — это отправка
	// формы панели: значение выбранного option.
	for raw, want := range map[string]string{"нет": "false", "Нет": "false", "0": "false", "да": "true", "1": "true", "истина": "true"} {
		html := список("f.Выполнена=" + url.QueryEscape(raw))
		if got := selectedFilterOption(t, html, "f.Выполнена"); got != want {
			t.Fatalf("f.Выполнена=%s: выбран %q, want %q:\n%s", raw, got, want, filterPanelFragment(html, "f.Выполнена"))
		}
		again := список("f.Выполнена=" + want)
		if (want == "true") != strings.Contains(again, "Позвонить клиенту") || (want == "false") != strings.Contains(again, "Проверить отказ") {
			t.Fatalf("повторное применение %s изменило отбор", raw)
		}
	}
	// Непонятное значение: список пуст, и панель держит его выбранным — повторное
	// «Применить» не расширяет отбор до всего списка.
	bad := список("f.Выполнена=может")
	if strings.Contains(bad, "Позвонить клиенту") || strings.Contains(bad, "Проверить отказ") {
		t.Fatal("непонятное значение открыло список")
	}
	if got := selectedFilterOption(t, bad, "f.Выполнена"); got != "может" {
		t.Fatalf("непонятное значение не сохранено в панели: %q", got)
	}
}

// selectedFilterOption — значение, которое отправит select панели «Отбор»:
// value выбранного option, иначе первого.
func selectedFilterOption(t *testing.T, page, name string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var sel *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if sel != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "select" {
			if v, _ := managedHTMLAttr(n, "name"); v == name {
				sel = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if sel == nil {
		t.Fatalf("нет select %s", name)
	}
	first, chosen := "", ""
	firstSet := false
	for c := sel.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "option" {
			continue
		}
		v, _ := managedHTMLAttr(c, "value")
		if !firstSet {
			first, firstSet = v, true
		}
		if _, ok := managedHTMLAttr(c, "selected"); ok {
			chosen = v
		}
	}
	if chosen != "" {
		return chosen
	}
	return first
}

func filterPanelFragment(html, name string) string {
	i := strings.Index(html, name)
	if i < 0 {
		return "(поля нет)"
	}
	start := i - 200
	if start < 0 {
		start = 0
	}
	end := i + 400
	if end > len(html) {
		end = len(html)
	}
	return html[start:end]
}
