package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

func liveListNode(root *html.Node, attr, value string) *html.Node {
	for _, a := range root.Attr {
		if a.Key == attr && a.Val == value {
			return root
		}
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := liveListNode(child, attr, value); found != nil {
			return found
		}
	}
	return nil
}

func liveListText(root *html.Node) string {
	if root.Type == html.TextNode {
		return root.Data
	}
	var text strings.Builder
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(liveListText(child))
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func TestLiveListHTTPIncludesCurrentSummaryAndClampsRemovedPage(t *testing.T) {
	for _, mode := range []string{"view=list&lm=pages", "view=tiles&lm=pages", "view=list&lm=feed", "view=tiles&lm=feed"} {
		t.Run(mode, func(t *testing.T) {
			ent := &metadata.Entity{
				Name: "LiveItems", Kind: metadata.KindCatalog,
				ListRefreshOn: []string{"данные.liveitems"}, NotifyChanges: true,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			router := chi.NewRouter()
			s.Mount(router)
			ids := make([]uuid.UUID, 5)
			for i := range ids {
				ids[i] = uuid.New()
				if err := s.store.Upsert(ctx, ent.Name, ids[i], map[string]any{"Наименование": fmt.Sprintf("Item %d", i)}, ent); err != nil {
					t.Fatal(err)
				}
			}
			page := "3"
			if strings.Contains(mode, "feed") {
				page = "1"
			}
			get := func() *html.Node {
				t.Helper()
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/catalog/liveitems?limit=2&page="+page+"&lang=ru&"+mode, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("GET list: %d %s", rec.Code, rec.Body.String())
				}
				doc, err := html.Parse(strings.NewReader(rec.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				live := liveListNode(doc, "data-ob-live", "catalog/liveitems")
				if live == nil {
					t.Fatal("list refresh target missing")
				}
				if liveListNode(live, "id", "ob-detail") != nil || liveListNode(doc, "id", "ob-detail") == nil {
					t.Fatal("detail panel must survive replacement of the live list")
				}
				return live
			}
			live := get()
			want := "Стр. 3 из 3 (5 записей)"
			if strings.Contains(mode, "feed") {
				want = "Загружено: 2 из 5"
			}
			if !strings.Contains(liveListText(live), want) {
				t.Fatalf("live region misses current summary %q: %s", want, liveListText(live))
			}
			for _, id := range ids[1:] {
				if err := s.store.Delete(ctx, ent.Name, id); err != nil {
					t.Fatal(err)
				}
			}
			live = get()
			if liveListNode(live, "data-ob-entity-id", ids[0].String()) == nil {
				t.Fatal("removed last page must show the remaining row")
			}
			want = "Всего: 1"
			if strings.Contains(mode, "feed") {
				want = "Загружено: 1 из 1"
			}
			if !strings.Contains(liveListText(live), want) {
				t.Fatalf("summary after deletion: %s", liveListText(live))
			}
			if err := s.store.Delete(ctx, ent.Name, ids[0]); err != nil {
				t.Fatal(err)
			}
			text := liveListText(get())
			if !strings.Contains(text, "Записей нет") || strings.Contains(text, "из 5") || strings.Contains(text, "Всего:") {
				t.Fatalf("empty live list keeps a stale summary: %s", text)
			}
		})
	}
}
