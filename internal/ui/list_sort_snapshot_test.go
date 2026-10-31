package ui

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Use rendered continuations and production JS: keeping the old tab's rows is
// insufficient if the same request overwrites the newer tab's preference.
func TestListSortSnapshotPreservesNewerChoice(t *testing.T) {
	for _, opening := range []string{"restored", "explicit"} {
		for _, mode := range []string{"feed", "pages"} {
			t.Run(opening+"/"+mode, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Hierarchical: true,
						Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, ListRefreshOn: []string{"данные.клиент"}}
					if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
						t.Fatal(err)
					}
					reg := runtime.NewRegistry()
					reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}})
					s := &Server{store: db, reg: reg, messages: NewMessageStore(), lockMgr: runtime.NewLockManager()}
					parent := uuidMust("55555555-1111-1111-1111-111111111111")
					if err := db.Upsert(t.Context(), ent.Name, parent, map[string]any{"Наименование": "Folder", "is_folder": true}, ent); err != nil {
						t.Fatal(err)
					}
					for i, name := range []string{"Alpha", "Beta", "Gamma", "Omega"} {
						if err := db.Upsert(t.Context(), ent.Name, uuidMust(fmt.Sprintf("4444444%d-1111-1111-1111-111111111111", i+1)), map[string]any{"Наименование": name, "parent_id": parent.String()}, ent); err != nil {
							t.Fatal(err)
						}
					}
					user := user1485("snapshot")
					get := func(q url.Values) string { t.Helper(); return listView1485(t, s, user, "?"+q.Encode()) }
					copyQuery := func(q url.Values) url.Values {
						copy := url.Values{}
						for k, v := range q {
							copy[k] = append([]string(nil), v...)
						}
						return copy
					}
					query := url.Values{"lm": {mode}, "limit": {"2"}, "q": {"a"}, "subsystem": {"sales"}, "f.Наименование": {"a"}, "parent": {parent.String()}, "view": {"list"}}
					choose := func(dir string) {
						t.Helper()
						q := copyQuery(query)
						q.Set("sort", "Наименование")
						q.Set("dir", dir)
						get(q)
					}
					fresh := func() string { t.Helper(); return get(query) }
					choose("desc")
					oldQuery := copyQuery(query)
					if opening == "explicit" {
						oldQuery.Set("sort", "Наименование")
						oldQuery.Set("dir", "desc")
					}
					old := get(oldQuery)
					assertDefaultSortRows(t, old, []string{"Omega", "Gamma"})
					next, attrs, forms, _ := defaultSortState(t, old)
					checkLatest := func() {
						t.Helper()
						f, d, err := db.GetListSortUserSettings(t.Context(), ent.Name, user.Login)
						if err != nil || f != "Наименование" || d != "asc" {
							t.Fatalf("old list overwrote newer preference: %q/%q %v", f, d, err)
						}
						assertDefaultSortRows(t, fresh(), []string{"Alpha", "Beta"})
					}
					choose("asc")
					checkLatest()
					assertDefaultSortRows(t, get(next), []string{"Beta", "Alpha"})
					checkLatest()
					assertState := func(q url.Values) {
						t.Helper()
						if q.Get("sort_snapshot") != "1" || q.Get("sort") != "Наименование" || q.Get("dir") != "desc" {
							t.Fatalf("lost snapshot: %v", q)
						}
						for _, key := range []string{"q", "limit", "lm", "subsystem", "f.Наименование", "parent", "view"} {
							if q.Get(key) != query.Get(key) {
								t.Fatalf("lost %s: %v", key, q)
							}
						}
					}
					assertState(next)
					if len(forms) < 2 {
						t.Fatal("search/filter forms missing")
					}
					for _, q := range forms {
						assertState(q)
						assertDefaultSortRows(t, get(q), []string{"Omega", "Gamma"})
						checkLatest()
					}
					requests := defaultSortNodeRequests(t, "static/list_sort_live_refresh_probe.js", map[string]any{"attrs": attrs, "href": "https://onebase.test/ui/catalog/клиент?" + oldQuery.Encode()})
					if len(requests) != 3 {
						t.Fatalf("expected named/SSE/imperative refresh, got %v", requests)
					}
					for _, request := range requests {
						u, err := url.Parse(request)
						if err != nil {
							t.Fatal(err)
						}
						assertState(u.Query())
						assertDefaultSortRows(t, get(u.Query()), []string{"Omega", "Gamma"})
						checkLatest()
					}
					// Two sequential requests from the actual production feed code.
					single := copyQuery(next)
					single.Del("page")
					single.Set("limit", "1")
					one := get(single)
					checkLatest()
					feedNext, _, _, _ := defaultSortState(t, one)
					feedRequests := defaultSortNodeRequests(t, "static/list_sort_default_feed_probe.js", map[string]any{"next": "https://onebase.test/ui/catalog/клиент?" + feedNext.Encode()})
					if len(feedRequests) != 2 {
						t.Fatalf("expected two feed requests: %v", feedRequests)
					}
					for i, request := range feedRequests {
						u, err := url.Parse(request)
						if err != nil {
							t.Fatal(err)
						}
						if u.Query().Get("page") != fmt.Sprint(i+2) || u.Query().Get("sort_snapshot") != "1" {
							t.Fatalf("feed lost state: %s", request)
						}
						assertDefaultSortRows(t, get(u.Query()), []string{[]string{"Gamma", "Beta"}[i]})
						checkLatest()
					}
					// Links that really select a column must release both captured states.
					doc, err := html.Parse(strings.NewReader(old))
					if err != nil {
						t.Fatal(err)
					}
					var header url.Values
					var walk func(*html.Node)
					walk = func(n *html.Node) {
						if n.Type == html.ElementNode && n.Data == "a" && n.Parent != nil && n.Parent.Data == "th" {
							href, _ := keyboardHTMLAttr(n, "href")
							u, e := url.Parse(href)
							if e == nil && u.Query().Get("sort") == "Наименование" {
								header = u.Query()
							}
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							walk(c)
						}
					}
					walk(doc)
					if header == nil || header.Has("sort_snapshot") || header.Has("sort_default") {
						t.Fatalf("header is not a new choice: %v", header)
					}
					// The old descending header toggles to ascending; first replace the
					// stored choice so that the click must actually write, rather than no-op.
					choose("desc")
					get(header)
					checkLatest()
					// Malformed/incomplete snapshot state must never write a preference.
					for _, q := range []url.Values{
						{"sort_snapshot": {"1"}, "sort": {"Missing"}, "dir": {"desc"}},
						{"sort_snapshot": {"invalid"}, "sort": {"Наименование"}, "dir": {"desc"}},
						{"sort_snapshot": {"1"}, "dir": {"desc"}},
						{"sort_snapshot": {"1"}, "sort": {"Наименование"}, "dir": {"invalid"}},
					} {
						get(q)
						checkLatest()
					}
				})
			})
		}
	}
}
