package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// «Настройка списка» документа за пользователем: порядок, видимость и ширины
// колонок сохраняются POST'ом формы настройки (или fetch из шапки) и
// применяются к его списку, другой пользователь видит стандартный список,
// «Стандартные настройки» возвращают его. Всё — через роутер, как в браузере.
func TestListSettingsPerUserThroughHTTP(t *testing.T) {
	entity := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Клиент", Type: metadata.FieldTypeString},
		{Name: "Сумма", Type: metadata.FieldTypeNumber},
		{Name: "Комментарий", Type: metadata.FieldTypeString},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{entity})
	if err := s.store.Upsert(ctx, "Заказ", uuid.New(), map[string]any{"Клиент": "Ромашка", "Сумма": 100, "Комментарий": "срочно"}, entity); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	user := func(login string) *auth.User {
		return &auth.User{Login: login, Roles: []*auth.Role{{Name: "Менеджер", Permissions: auth.Permission{
			Documents: map[string][]string{"Заказ": {"read"}},
		}}}}
	}
	do := func(method, path string, form url.Values, u *auth.User, header map[string]string) *httptest.ResponseRecorder {
		var req *http.Request
		if form != nil {
			req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	list := func(u *auth.User) (heads []string, widths map[string]string, cells int, doc *html.Node) {
		rec := do(http.MethodGet, "/ui/document/заказ", nil, u, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET list: %d %.400s", rec.Code, rec.Body.String())
		}
		doc, err := html.Parse(strings.NewReader(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		widths = map[string]string{}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode {
				if f, ok := managedHTMLAttr(n, "data-ob-ls-field"); ok && n.Data == "th" {
					heads = append(heads, f)
				}
				if f, ok := managedHTMLAttr(n, "data-ob-ls-col"); ok && n.Data == "col" {
					widths[f], _ = managedHTMLAttr(n, "style")
				}
				if n.Data == "tr" {
					if _, ok := managedHTMLAttr(n, "data-ob-list-row"); ok {
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							if c.Type == html.ElementNode && c.Data == "td" {
								cells++
							}
						}
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		return heads, widths, cells, doc
	}

	anna, igor := user("anna"), user("igor")
	defHeads, defWidths, defCells, _ := list(anna)
	if strings.Join(defHeads, ",") != "Клиент,Сумма,Комментарий" || len(defWidths) != 0 {
		t.Fatalf("default list: heads=%v widths=%v", defHeads, defWidths)
	}

	// Форма «Настройка списка»: Комментарий первым, Клиент шириной 220, Сумма скрыта.
	rec := do(http.MethodPost, "/ui/document/заказ/list-settings/save", url.Values{
		"__list_settings": {`{"columns":[{"field":"Комментарий","visible":true},{"field":"Клиент","visible":true,"width":220},{"field":"Сумма","visible":false},{"field":"НетТакого","visible":true}]}`},
		"__return":        {"/ui/document/заказ?q=Ром"},
	}, anna, nil)
	if loc, _ := url.QueryUnescape(rec.Header().Get("Location")); rec.Code != http.StatusSeeOther || loc != "/ui/document/заказ?q=Ром" {
		t.Fatalf("save: %d location=%q %.300s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	heads, widths, cells, doc := list(anna)
	if strings.Join(heads, ",") != "Комментарий,Клиент" {
		t.Fatalf("anna heads after save: %v", heads)
	}
	if !strings.Contains(widths["Клиент"], "width:220px") || !strings.Contains(widths["Комментарий"], "width:150px") {
		t.Fatalf("anna column widths: %v", widths)
	}
	if cells != defCells-1 {
		t.Fatalf("row cells: got %d, want %d (hidden column must leave rows too)", cells, defCells-1)
	}
	if n := elementByAttr(doc, "table", "data-ob-list-table"); n == nil {
		t.Fatal("no list table")
	} else if cls, _ := managedHTMLAttr(n, "class"); !strings.Contains(cls, "ob-list-fixed") {
		t.Fatalf("list table class %q: widths need fixed layout", cls)
	}

	// Другой пользователь — стандартный список.
	if heads, widths, _, _ := list(igor); strings.Join(heads, ",") != "Клиент,Сумма,Комментарий" || len(widths) != 0 {
		t.Fatalf("igor list must stay default: heads=%v widths=%v", heads, widths)
	}

	// Шапка (fetch): 204, ширина Комментария меняется, внешний __return не уводит.
	rec = do(http.MethodPost, "/ui/document/заказ/list-settings/save", url.Values{
		"__list_settings": {`{"columns":[{"field":"Комментарий","visible":true,"width":90},{"field":"Клиент","visible":true,"width":220},{"field":"Сумма","visible":false}]}`},
	}, anna, map[string]string{"X-Requested-With": "obListSettings"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("fetch save: %d", rec.Code)
	}
	if _, widths, _, _ := list(anna); !strings.Contains(widths["Комментарий"], "width:90px") {
		t.Fatalf("width after header drag: %v", widths)
	}
	rec = do(http.MethodPost, "/ui/document/заказ/list-settings/save", url.Values{
		"__list_settings": {`{"columns":[]}`},
		"__return":        {"https://evil.example/ui/x"},
	}, igor, nil)
	if loc, _ := url.PathUnescape(rec.Header().Get("Location")); rec.Code != http.StatusSeeOther || loc != "/ui/document/заказ" {
		t.Fatalf("external __return: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(http.MethodPost, "/ui/document/заказ/list-settings/save", url.Values{"__list_settings": {"{"}}, anna, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("broken JSON: %d", rec.Code)
	}

	// «Стандартные настройки».
	rec = do(http.MethodPost, "/ui/document/заказ/list-settings/reset", url.Values{"__return": {"/ui/document/заказ"}}, anna, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reset: %d", rec.Code)
	}
	if heads, widths, _, _ := list(anna); strings.Join(heads, ",") != "Клиент,Сумма,Комментарий" || len(widths) != 0 {
		t.Fatalf("after reset: heads=%v widths=%v", heads, widths)
	}
}

// Новые реквизиты после сохранения настройки появляются в конце видимыми,
// удалённые — пропадают; нельзя скрыть все колонки.
func TestListSettingsColumnsCanonical(t *testing.T) {
	entity := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "А", Type: metadata.FieldTypeString},
		{Name: "Б", Type: metadata.FieldTypeString},
		{Name: "В", Type: metadata.FieldTypeString},
	}}
	st := &ListUserSettings{Columns: []ListColumnSetting{{Field: "б", Visible: true, Width: 10}, {Field: "Старое", Visible: true}, {Field: "А", Visible: false, Width: 5000}}}
	cols := listSettingsColumns(entity, st)
	var got []string
	for _, c := range cols {
		got = append(got, c.Field.Name)
	}
	if strings.Join(got, ",") != "Б,А,В" || cols[0].Width != listColumnMinWidth || cols[1].Width != listColumnMaxWidth || !cols[2].Visible || cols[1].Visible {
		t.Fatalf("canonical columns: %v %+v", got, cols)
	}
	none := &ListUserSettings{Columns: []ListColumnSetting{{Field: "А"}, {Field: "Б"}, {Field: "В"}}}
	if vis := visibleListColumns(listSettingsColumns(entity, none)); len(vis) != 1 || vis[0].Field.Name != "А" {
		t.Fatalf("all hidden must keep the first column: %+v", vis)
	}
}

func elementByAttr(n *html.Node, tag, attr string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		if _, ok := managedHTMLAttr(n, attr); ok {
			return n
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := elementByAttr(c, tag, attr); f != nil {
			return f
		}
	}
	return nil
}
