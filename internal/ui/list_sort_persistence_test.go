package ui

// Выбранная пользователем сортировка списка запоминается по пользователю и
// сущности, как вид списка (#1485). Всё через публичный обработчик списка.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

func TestListSortPersistence(t *testing.T) {
	ent := &metadata.Entity{
		Name:   "Клиент",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	for i, name := range []string{"Анна", "Борис", "Вера"} {
		id := uuidMust("1111111" + string(rune('1'+i)) + "-1111-1111-1111-111111111111")
		if err := s.store.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
			t.Fatal(err)
		}
	}
	order := func(body string) string {
		a, b, v := strings.Index(body, "Анна"), strings.Index(body, "Борис"), strings.Index(body, "Вера")
		if a < 0 || b < 0 || v < 0 {
			t.Fatalf("в списке нет строк")
		}
		if v < b && b < a {
			return "desc"
		}
		if a < b && b < v {
			return "asc"
		}
		return "other"
	}
	u1, u2 := user1485("u1"), user1485("u2")

	if got := order(listView1485(t, s, u1, "?sort=Наименование&dir=desc")); got != "desc" {
		t.Fatalf("явная сортировка не применилась: %s", got)
	}
	// Открытие без параметра — тот же порядок, что выбрал пользователь.
	if got := order(listView1485(t, s, u1, "")); got != "desc" {
		t.Fatalf("сохранённая сортировка не восстановилась: %s", got)
	}
	// Другой пользователь — свой порядок (по умолчанию — по наименованию).
	if got := order(listView1485(t, s, u2, "")); got != "asc" {
		t.Fatalf("сортировка одного пользователя протекла другому: %s", got)
	}
	// Новый явный выбор перекрывает сохранённый.
	listView1485(t, s, u1, "?sort=Наименование&dir=asc")
	if got := order(listView1485(t, s, u1, "")); got != "asc" {
		t.Fatalf("новый выбор не перекрыл сохранённый: %s", got)
	}
	// Неизвестное поле не сохраняется и не ломает прежний выбор.
	listView1485(t, s, u1, "?sort=НетТакого&dir=desc")
	if f, d, _ := s.store.GetListSortUserSettings(context.Background(), ent.Name, "u1"); f != "Наименование" || d != "asc" {
		t.Fatalf("сохранено %q|%q, ожидалось Наименование|asc", f, d)
	}
}

// A saved preference may change in another tab after page one has rendered.
// Every continuation of that rendered list must keep its original order.
func TestListSortPersistence_PaginationKeepsRestoredOrder(t *testing.T) {
	for _, mode := range []string{"feed", "pages"} {
		t.Run(mode, func(t *testing.T) {
			ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
			s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			for i, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
				id := uuidMust("1111111" + string(rune('1'+i)) + "-1111-1111-1111-111111111111")
				if err := s.store.Upsert(t.Context(), ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
					t.Fatal(err)
				}
			}
			user := user1485("u1")
			listView1485(t, s, user, "?sort=Наименование&dir=desc")
			body := listView1485(t, s, user, "?lm="+mode+"&limit=2&q=a")
			assertRows := func(body string, first, second, absent1, absent2 string) {
				t.Helper()
				a, b := strings.Index(body, ">"+first+"<"), strings.Index(body, ">"+second+"<")
				if a < 0 || b < a || strings.Contains(body, ">"+absent1+"<") || strings.Contains(body, ">"+absent2+"<") {
					t.Fatalf("expected only %s,%s in that order", first, second)
				}
			}
			assertRows(body, "Omega", "Gamma", "Beta", "Alpha")
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var next string
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					for _, a := range n.Attr {
						if a.Key == "href" {
							u, err := url.Parse(a.Val)
							if err == nil && u.Query().Get("page") == "2" {
								next = a.Val
							}
						}
					}
				}
				// Search and filter forms must preserve the effective sort too.
				if n.Type == html.ElementNode && n.Data == "form" {
					method, _ := keyboardHTMLAttr(n, "method")
					if strings.EqualFold(method, "GET") {
						fields := map[string]string{}
						var inputs func(*html.Node)
						inputs = func(child *html.Node) {
							if child.Type == html.ElementNode && child.Data == "input" {
								name, _ := keyboardHTMLAttr(child, "name")
								value, _ := keyboardHTMLAttr(child, "value")
								fields[name] = value
							}
							for c := child.FirstChild; c != nil; c = c.NextSibling {
								inputs(c)
							}
						}
						inputs(n)
						if _, ok := fields["lm"]; ok && (fields["sort"] != "Наименование" || fields["dir"] != "desc") {
							t.Errorf("list form lost restored order: %v", fields)
						}
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			if next == "" {
				t.Fatal("missing page-two link")
			}
			u, err := url.Parse(next)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if q.Get("sort") != "Наименование" || q.Get("dir") != "desc" || q.Get("q") != "a" || q.Get("limit") != "2" || q.Get("lm") != mode {
				t.Fatalf("continuation lost effective list state: %s", next)
			}
			// The second tab changes the same user's stored preference.
			listView1485(t, s, user, "?sort=Наименование&dir=asc")
			assertRows(listView1485(t, s, user, "?lm="+mode+"&limit=2&q=a"), "Alpha", "Beta", "Gamma", "Omega")
			assertRows(listView1485(t, s, user, next), "Beta", "Alpha", "Gamma", "Omega")
		})
	}
}

func TestListSortFeedBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the feed sort regression test")
	}
	cmd := exec.Command(node, "--test", "static/list_sort_feed_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node feed sort test: %v\n%s", err, output)
	}
}

// Exercise the rendered list attributes and the production live-list listener,
// then send its refresh URL through the same HTTP handler after another tab has
// changed the saved preference.
func TestListSortPersistence_LiveRefreshKeepsRestoredOrder(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the live-list sort regression test")
	}
	for _, mode := range []string{"feed", "pages"} {
		t.Run(mode, func(t *testing.T) {
			ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog,
				Fields:        []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
				ListRefreshOn: []string{"данные.клиент"}}
			s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			for i, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
				id := uuidMust("1111111" + string(rune('1'+i)) + "-1111-1111-1111-111111111111")
				if err := s.store.Upsert(t.Context(), ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
					t.Fatal(err)
				}
			}
			user := user1485("u1")
			listView1485(t, s, user, "?sort=Наименование&dir=desc")
			query := "?lm=" + mode + "&limit=2&q=a&subsystem=sales"
			body := listView1485(t, s, user, query)
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			attrs := map[string]string{}
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if class, _ := keyboardHTMLAttr(n, "class"); strings.Contains(" "+class+" ", " ob-list-content ") {
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			if attrs["data-ob-refresh-on"] != "данные.клиент" {
				t.Fatalf("missing live-list subscription: %v", attrs)
			}
			// A different tab now changes this user's persisted choice to asc.
			listView1485(t, s, user, "?sort=Наименование&dir=asc")
			fixture, err := json.Marshal(map[string]any{
				"attrs": attrs, "href": "https://onebase.test/ui/catalog/клиент" + query,
			})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, "static/list_sort_live_refresh_probe.js") //nolint:gosec // test-only executable resolved by exec.LookPath
			cmd.Stdin = bytes.NewReader(fixture)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("live-list event test: %v\n%s", err, output)
			}
			var requests []string
			if err := json.Unmarshal(output, &requests); err != nil {
				t.Fatalf("decode live-list requests: %v\n%s", err, output)
			}
			if len(requests) != 3 {
				t.Fatalf("expected named, reconnect and imperative refresh requests, got %v", requests)
			}
			for _, request := range requests {
				u, err := url.Parse(request)
				if err != nil {
					t.Fatal(err)
				}
				q := u.Query()
				if q.Get("q") != "a" || q.Get("lm") != mode || q.Get("limit") != "2" || q.Get("subsystem") != "sales" {
					t.Fatalf("live refresh lost list state: %s", request)
				}
				refreshed := listView1485(t, s, user, "?"+u.RawQuery)
				omega, gamma := strings.Index(refreshed, ">Omega<"), strings.Index(refreshed, ">Gamma<")
				if omega < 0 || gamma < omega || strings.Contains(refreshed, ">Alpha<") || strings.Contains(refreshed, ">Beta<") {
					t.Fatalf("live refresh changed restored sort; request=%s", request)
				}
			}
		})
	}
}
