package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Both databases exercise the public list handler and links/forms from its HTML.
// A new preference in another tab must not change a list already being read.
func TestListSortDefaultSnapshot(t *testing.T) {
	for _, scenario := range []string{"catalog", "order_by", "hierarchy", "document", "explicit_dir", "removed_field"} {
		for _, mode := range []string{"feed", "pages"} {
			t.Run(scenario+"/"+mode, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog,
						Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, ListRefreshOn: []string{"данные.клиент"}}
					query := url.Values{"lm": {mode}, "limit": {"2"}, "q": {"a"}, "subsystem": {"sales"}, "f.Наименование": {"a"}}
					want := []string{"Alpha", "Beta", "Gamma", "Omega"}
					switch scenario {
					case "order_by":
						ent.OrderBy = []string{"Ранг"}
						ent.Fields = append(ent.Fields, metadata.Field{Name: "Ранг", Type: metadata.FieldTypeNumber})
						want = []string{"Gamma", "Alpha", "Omega", "Beta"}
					case "hierarchy":
						ent.Hierarchical = true
						want = []string{"Omega", "Alpha", "Beta", "Gamma"}
					case "document":
						ent.Kind = metadata.KindDocument
						ent.Fields = append(ent.Fields, metadata.Field{Name: "Дата", Type: metadata.FieldTypeDate})
						want = []string{"Omega", "Gamma", "Beta", "Alpha"}
					case "explicit_dir":
						query.Set("dir", "desc") // Storage owns the default field/direction rules.
					}
					if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
						t.Fatal(err)
					}
					reg := runtime.NewRegistry()
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
					s := &Server{store: db, reg: reg, messages: NewMessageStore(), lockMgr: runtime.NewLockManager()}
					for i, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
						row := map[string]any{"Наименование": name}
						if scenario == "order_by" {
							row["Ранг"] = []int{2, 4, 1, 3}[i]
						}
						if scenario == "hierarchy" {
							row["is_folder"] = i == 3
						}
						if scenario == "document" {
							row["Дата"] = time.Date(2026, 1, i+1, 12, 0, 0, 0, time.UTC)
						}
						if err := db.Upsert(t.Context(), ent.Name, uuidMust(fmt.Sprintf("1111111%d-1111-1111-1111-111111111111", i+1)), row, ent); err != nil {
							t.Fatal(err)
						}
					}
					user := user1485(scenario + mode)
					if scenario == "removed_field" {
						if err := db.SaveListSortUserSettings(t.Context(), ent.Name, user.Login, "Удалено", "desc"); err != nil {
							t.Fatal(err)
						}
					}
					get := func(user *auth.User, q url.Values) string {
						t.Helper()
						req := reqWithChi("GET", "/ui/"+string(ent.Kind)+"/"+strings.ToLower(ent.Name)+"?"+q.Encode(), nil, map[string]string{"entity": ent.Name})
						if user != nil {
							req = req.WithContext(auth.ContextWithUser(req.Context(), user))
						}
						rec := httptest.NewRecorder()
						s.list(rec, req)
						if rec.Code != 200 {
							t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
						}
						return rec.Body.String()
					}
					first := get(user, query)
					assertDefaultSortRows(t, first, want[:2])
					next, attrs, forms, header := defaultSortState(t, first)
					// This is the first valid saved choice, made in a different tab.
					choice := url.Values{"sort": {"Наименование"}, "dir": {"desc"}, "limit": {"2"}}
					get(user, choice)
					assertDefaultSortRows(t, get(user, next), want[2:])
					if next.Get("sort_default") != "1" || next.Get("sort") != "" || next.Get("dir") != query.Get("dir") {
						t.Fatalf("default state lost/changed: %v", next)
					}
					for _, key := range []string{"q", "limit", "lm", "subsystem", "f.Наименование"} {
						if next.Get(key) != query.Get(key) {
							t.Fatalf("lost %s: %v", key, next)
						}
					}
					if len(forms) < 2 {
						t.Fatalf("search/filter forms missing: %v", forms)
					}
					for _, form := range forms {
						if form.Get("sort_default") != "1" || form.Get("dir") != query.Get("dir") {
							t.Fatalf("form lost default: %v", form)
						}
					}

					assertDefaultSortRows(t, get(user, url.Values{"limit": {"2"}}), []string{"Omega", "Gamma"})
					for _, form := range forms {
						assertDefaultSortRows(t, get(user, form), want[:2])
					}
					refreshes := defaultSortNodeRequests(t, "static/list_sort_live_refresh_probe.js", map[string]any{"attrs": attrs, "href": "https://onebase.test/ui/catalog/клиент?" + query.Encode()})
					if len(refreshes) != 3 {
						t.Fatalf("expected named/SSE/imperative refresh: %v", refreshes)
					}
					for _, request := range refreshes {
						u, err := url.Parse(request)
						if err != nil {
							t.Fatal(err)
						}
						for _, key := range []string{"q", "limit", "lm", "subsystem", "f.Наименование", "dir"} {
							if u.Query().Get(key) != query.Get(key) {
								t.Fatalf("refresh lost %s: %s", key, request)
							}
						}
						assertDefaultSortRows(t, get(user, u.Query()), want[:2])
					}
					if f, d, err := db.GetListSortUserSettings(t.Context(), ent.Name, user.Login); err != nil || f != "Наименование" || d != "desc" {
						t.Fatalf("old list overwrote new preference: %q/%q %v", f, d, err)
					}
					if scenario == "catalog" && mode == "feed" {
						other := *ent
						other.Name = "Другой"
						if err := db.Migrate(t.Context(), []*metadata.Entity{&other}); err != nil {
							t.Fatal(err)
						}
						reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent, &other}})
						for i, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
							if err := db.Upsert(t.Context(), other.Name, uuidMust(fmt.Sprintf("3333333%d-1111-1111-1111-111111111111", i+1)), map[string]any{"Наименование": name}, &other); err != nil {
								t.Fatal(err)
							}
						}
						original := ent
						ent = &other
						assertDefaultSortRows(t, get(user, query), want[:2])
						ent = original
					}
					// A real header click supersedes the pinned default and saves its choice.
					if header.Get("sort") != "Наименование" {
						t.Fatalf("missing sort header: %v", header)
					}
					get(user, header)
					if f, d, _ := db.GetListSortUserSettings(t.Context(), ent.Name, user.Login); f != "Наименование" || d != "asc" {
						t.Fatalf("header lost precedence: %s/%s", f, d)
					}
					// An invalid explicit field must not change the preference either.
					get(user, url.Values{"sort_default": {"1"}, "sort": {"Missing"}, "dir": {"desc"}})
					if f, d, _ := db.GetListSortUserSettings(t.Context(), ent.Name, user.Login); f != "Наименование" || d != "asc" {
						t.Fatalf("invalid field saved: %s/%s", f, d)
					}
					// No user: preserve the old URL behavior without a preference snapshot.
					anonymous := get(nil, query)
					anonNext, _, _, _ := defaultSortState(t, anonymous)
					if anonNext.Has("sort_default") {
						t.Fatalf("anonymous list changed: %v", anonNext)
					}
					// Other users still open with their own default order.
					assertDefaultSortRows(t, get(user1485("other"+scenario+mode), query), want[:2])
				})
			})
		}
	}
}

func TestListSortDefaultFeedRequests(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
		s := &Server{store: db, reg: reg, messages: NewMessageStore(), lockMgr: runtime.NewLockManager()}
		for i, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
			if err := db.Upsert(t.Context(), ent.Name, uuidMust(fmt.Sprintf("2222222%d-1111-1111-1111-111111111111", i+1)), map[string]any{"Наименование": name}, ent); err != nil {
				t.Fatal(err)
			}
		}
		user := user1485("feed-js")
		body := listView1485(t, s, user, "?lm=feed&limit=1&q=a&subsystem=sales&f.Наименование=a")
		next, _, _, _ := defaultSortState(t, body)
		listView1485(t, s, user, "?sort=Наименование&dir=desc")
		requests := defaultSortNodeRequests(t, "static/list_sort_default_feed_probe.js", map[string]any{"next": "https://onebase.test/ui/catalog/клиент?" + next.Encode()})
		if len(requests) != 2 {
			t.Fatalf("expected two feed requests: %v", requests)
		}
		for i, request := range requests {
			u, err := url.Parse(request)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if q.Get("page") != fmt.Sprint(i+2) || q.Get("sort_default") != "1" || q.Get("subsystem") != "sales" || q.Get("f.Наименование") != "a" {
				t.Fatalf("feed lost state: %s", request)
			}
			assertDefaultSortRows(t, listView1485(t, s, user, "?"+u.RawQuery), []string{[]string{"Beta", "Gamma"}[i]})
		}
		if f, d, _ := db.GetListSortUserSettings(t.Context(), ent.Name, user.Login); f != "Наименование" || d != "desc" {
			t.Fatalf("feed overwrote preference: %s/%s", f, d)
		}
	})
}

func assertDefaultSortRows(t *testing.T, body string, want []string) {
	t.Helper()
	var got []string
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			for _, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
				if strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(n.Data), "📁 "), "📄 ")) == name {
					got = append(got, name)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list rows %v, want %v", got, want)
	}
}

func defaultSortState(t *testing.T, body string) (url.Values, map[string]string, []url.Values, url.Values) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var next, header url.Values
	attrs := map[string]string{}
	var forms []url.Values
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "a" {
				href, _ := keyboardHTMLAttr(n, "href")
				u, err := url.Parse(href)
				if err == nil {
					q := u.Query()
					if q.Get("page") == "2" {
						next = q
					}
					if q.Get("sort") == "Наименование" {
						header = q
					}
				}
			}
			if class, _ := keyboardHTMLAttr(n, "class"); strings.Contains(" "+class+" ", " ob-list-content ") {
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
			}
			if n.Data == "form" {
				method, _ := keyboardHTMLAttr(n, "method")
				if strings.EqualFold(method, "GET") {
					q := url.Values{}
					var inputs func(*html.Node)
					inputs = func(c *html.Node) {
						if c.Type == html.ElementNode && c.Data == "input" {
							name, _ := keyboardHTMLAttr(c, "name")
							value, _ := keyboardHTMLAttr(c, "value")
							if name != "" {
								q.Add(name, value)
							}
						}
						for child := c.FirstChild; child != nil; child = child.NextSibling {
							inputs(child)
						}
					}
					inputs(n)
					if q.Has("lm") {
						forms = append(forms, q)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if next == nil {
		t.Fatal("missing rendered page 2 link")
	}
	return next, attrs, forms, header
}

func defaultSortNodeRequests(t *testing.T, script string, fixture any) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for this regression")
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, script) //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Node: %v\n%s", err, out)
	}
	var requests []string
	if err := json.Unmarshal(out, &requests); err != nil {
		t.Fatalf("Node JSON: %v: %s", err, out)
	}
	return requests
}
