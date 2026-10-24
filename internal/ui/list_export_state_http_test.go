package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

// Follow the export URL from a real GET: both template functions must coexist
// with masked inputs, and query escaping must survive the HTML boundary.
func TestUI_ListExportStateThroughHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{Name: "Заявка", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Автор", Type: metadata.FieldTypeString}, {Name: "Метка", Type: metadata.FieldTypeString},
				{Name: "Активный", Type: metadata.FieldTypeBool}},
			Activity: &metadata.ActivityConfig{Field: "Активный", DefaultScope: metadata.ActivityScopeActive},
			Forms: []*metadata.FormModule{{Name: "ФормаСписка", Kind: "list", EntityName: "Заявка",
				ListFilter: []metadata.FormListCondition{{Query: listQueryMine, Removable: true}}}},
		}
		s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		s.store = db
		s.entitySvc.Store = db
		const value = "Найди & + ?/#%"
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
			"Наименование": value, "Метка": value, "Автор": "u-other", "Активный": false}, entity); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		s.Mount(router)
		get := func(target string) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r = r.WithContext(auth.ContextWithUser(context.Background(), listQueryUser("u-op")))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s: %d: %s", target, w.Code, w.Body.String())
			}
			return w
		}
		for _, query := range []url.Values{
			{"activity": {metadata.ActivityScopeInactive}, "q": {value}, "f.Метка": {value}, "lf_off": {"0"}},
			{},
		} {
			page := get("/ui/catalog/заявка?" + query.Encode()).Body.String()
			doc, err := html.Parse(strings.NewReader(page))
			if err != nil {
				t.Fatal(err)
			}
			var links []string
			htmlFound := false
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.TextNode && strings.TrimSpace(n.Data) == value && n.Parent != nil && n.Parent.Data == "td" {
					htmlFound = true
				}
				if n.Type == html.ElementNode && n.Data == "a" {
					href, _ := keyboardHTMLAttr(n, "href")
					if parsed, err := url.Parse(href); err == nil && parsed.Path == "/ui/catalog/заявка/excel" {
						links = append(links, href)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			if len(links) != 1 {
				t.Fatalf("export links: %v", links)
			}
			link, err := url.Parse(links[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(link.Path, "/excel") || link.Fragment != "" {
				t.Fatalf("query escaped into path/fragment: %s", links[0])
			}
			for _, key := range []string{"q", "f.Метка", "lf_off"} {
				if got := link.Query().Get(key); got != query.Get(key) {
					t.Errorf("%s=%q, want %q", key, got, query.Get(key))
				}
			}
			if len(query) > 0 && link.Query().Get("activity") != query.Get("activity") {
				t.Fatal("lost activity")
			}
			exported := get(links[0])
			book, err := excelize.OpenReader(bytes.NewReader(exported.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := book.GetRows(book.GetSheetName(0))
			if closeErr := book.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				for _, cell := range row {
					found = found || cell == value
				}
			}
			if found != (len(query) > 0) {
				t.Fatalf("export differs from list filter: rows=%v query=%v", rows, query)
			}
			if htmlFound != (len(query) > 0) {
				t.Fatal("HTML list lost the selected row state")
			}
		}
	})
}

// The journal uses the other registered suffix function; an empty filter must
// produce a bare export path, and a populated filter must survive URL decoding.
func TestUI_JournalExportStateThroughHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		entity := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Метка", Type: metadata.FieldTypeString}}}
		journal := &metadata.Journal{Name: "Заказы", Documents: []string{entity.Name},
			Columns: []metadata.JournalColumn{{Label: "Метка", Field: "Метка"}},
			Filters: []metadata.JournalFilter{{Field: "Метка", Type: "string"}}}
		s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		s.store = db
		s.entitySvc.Store = db
		s.reg.LoadJournals([]*metadata.Journal{journal})
		router := chi.NewRouter()
		s.Mount(router)
		get := func(target string) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r = r.WithContext(auth.ContextWithUser(r.Context(), &auth.User{ID: "root", IsAdmin: true}))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("GET %s: %d: %s", target, w.Code, w.Body.String())
			}
			return w
		}
		for _, value := range []string{"", "А & + ?/#%"} {
			qs := url.Values{}
			if value != "" {
				qs.Set("f.Метка", value)
			}
			doc, err := html.Parse(strings.NewReader(get("/ui/journal/заказы?" + qs.Encode()).Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			var links []*url.URL
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "a" {
					href, _ := keyboardHTMLAttr(n, "href")
					if u, err := url.Parse(href); err == nil && u.Path == "/ui/journal/заказы/excel" {
						links = append(links, u)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			if len(links) != 1 {
				t.Fatalf("export links: %v", links)
			}
			link := links[0]
			if link.Fragment != "" || link.Query().Get("f.Метка") != value || (value == "" && (link.RawQuery != "" || link.ForceQuery)) {
				t.Fatalf("invalid export query: %s", link)
			}
			book, err := excelize.OpenReader(bytes.NewReader(get(link.String()).Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if err := book.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
