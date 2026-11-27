package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"golang.org/x/net/html"
)

// Список в правой панели оболочки тянется на всю высоту полосы: строки
// прокручиваются внутри, горизонтальная прокрутка — у нижнего края. Раньше
// заполнение высоты (ob-widget-fill) работало только для главной (.dash), и в
// полосе таблица обрывалась по содержимому, а горизонтальная прокрутка висела
// посередине. Проверка — тем же путём, что у пользователя: GET /ui/app.
func TestShellSidePanelListFillsPanelHeight(t *testing.T) {
	proj := sidePanelProject(t)
	s, ctx := newSubmitTestServer(t, proj.Entities)
	s.reg.LoadWidgets(proj.Widgets)
	s.reg.LoadHomePage(proj.HomePage)
	if err := s.store.Upsert(ctx, "Задача", uuid.New(), map[string]any{"Описание": "Проверить жалобу"}, s.reg.GetEntity("Задача")); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	u := &auth.User{Login: "otk", Roles: []*auth.Role{{Name: "СотрудникОТК", Permissions: auth.Permission{
		Documents: map[string][]string{"Задача": {"read", "write"}},
	}}}}
	req := httptest.NewRequest(http.MethodGet, "/ui/app", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/app: %d %.400s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	panel := elementByID(doc, "ob-side-panel")
	if panel == nil {
		t.Fatal("no side panel")
	}
	// Цепочка, на которую опираются правила: aside > .w-card > [data-ob-widget-body] … .w-list.
	card := childElementWithClass(panel, "w-card")
	if card == nil {
		t.Fatal("panel card is not a direct child of the panel")
	}
	var bodyNode *html.Node
	for c := card.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if _, ok := managedHTMLAttr(c, "data-ob-widget-body"); ok {
				bodyNode = c
			}
		}
	}
	if bodyNode == nil {
		t.Fatal("widget body is not a direct child of the panel card")
	}
	if findElementWithClass(bodyNode, "w-list") == nil {
		t.Fatal("list body missing in panel")
	}
	for _, rule := range []string{
		".ob-side-panel{flex-shrink:0;position:relative;border-left:1px solid #cbd5e1;background:#fff;overflow:auto;min-width:180px;max-width:60vw;display:flex;flex-direction:column}",
		".ob-side-panel>.w-card{flex:1 1 auto;min-height:0;box-sizing:border-box}",
		".ob-side-panel>.w-card>[data-ob-widget-body]{flex:1;min-height:0;display:flex;flex-direction:column}",
		".ob-side-panel>.w-card .w-list{flex:1;min-height:0;overflow:auto}",
		".ob-side-panel>.w-card .w-list th{position:sticky;top:0;z-index:1}",
	} {
		if !strings.Contains(body, rule) {
			t.Errorf("/ui/app styles miss %q", rule)
		}
	}
}

func childElementWithClass(n *html.Node, class string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && nodeHasClass(c, class) {
			return c
		}
	}
	return nil
}

func findElementWithClass(n *html.Node, class string) *html.Node {
	if n.Type == html.ElementNode && nodeHasClass(n, class) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findElementWithClass(c, class); f != nil {
			return f
		}
	}
	return nil
}

func nodeHasClass(n *html.Node, class string) bool {
	v, _ := managedHTMLAttr(n, "class")
	for _, f := range strings.Fields(v) {
		if f == class {
			return true
		}
	}
	return false
}
