package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
	"golang.org/x/net/html"
)

// home_page.yaml side_panel: закреплённая правая панель оболочки с виджетом
// по ролям; тому, кому видна панель, виджет на главной не дублируется
// (hide_on_home), а обновление виджета панели (/ui/_widget) работает и
// тогда. Тест — YAML проекта → project.Load → GET /ui/app и /ui/_widget.
func sidePanelProject(t *testing.T) *project.Project {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G703: rel — литерал теста, каталог — t.TempDir()
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // G703: rel — литерал теста, каталог — t.TempDir()
			t.Fatal(err)
		}
	}
	write("documents/задача.yaml", "name: Задача\nfields:\n  - name: Описание\n    type: string\n")
	write("widgets/моизадачи.yaml", `name: МоиЗадачи
type: list
title: Задачи
query: ВЫБРАТЬ Ссылка, Описание ИЗ Документ.Задача
source:
  entity: Задача
columns:
  - field: Описание
    label: Задача
`)
	write("config/home_page.yaml", `title: Главная
widgets:
  - name: МоиЗадачи
side_panel:
  widget: МоиЗадачи
  width: 300
  except_roles: [ОператорКЦ]
  hide_on_home: true
`)
	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(func() { proj.Close() })
	return proj
}

func elementByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode {
		if v, _ := managedHTMLAttr(n, "id"); v == id {
			return n
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := elementByID(c, id); f != nil {
			return f
		}
	}
	return nil
}

func TestShellSidePanelByRoleThroughHTTP(t *testing.T) {
	proj := sidePanelProject(t)
	s, ctx := newSubmitTestServer(t, proj.Entities)
	s.reg.LoadWidgets(proj.Widgets)
	s.reg.LoadHomePage(proj.HomePage)
	if err := s.store.Upsert(ctx, "Задача", uuid.New(), map[string]any{"Описание": "Проверить жалобу"}, s.reg.GetEntity("Задача")); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	s.Mount(router)
	user := func(role string) *auth.User {
		return &auth.User{Login: role, Roles: []*auth.Role{{Name: role, Permissions: auth.Permission{
			Documents: map[string][]string{"Задача": {"read", "write"}},
		}}}}
	}
	get := func(path string, u *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		role       string
		panel      bool
		widgetHome bool
	}{
		{"СотрудникОТК", true, false},
		{"ОператорКЦ", false, true},
	} {
		t.Run(tc.role, func(t *testing.T) {
			rec := get("/ui/app", user(tc.role))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /ui/app: %d %.400s", rec.Code, rec.Body.String())
			}
			doc, err := html.Parse(strings.NewReader(rec.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			panel := elementByID(doc, "ob-side-panel")
			if (panel != nil) != tc.panel {
				t.Fatalf("side panel present=%t, want %t", panel != nil, tc.panel)
			}
			if panel != nil {
				if style, _ := managedHTMLAttr(panel, "style"); !strings.Contains(style, "width:300px") {
					t.Fatalf("panel width style %q", style)
				}
				if card := refCardNode(panel, "div", "data-ob-widget-card"); card == nil {
					t.Fatal("panel has no refreshable widget card")
				}
				if !strings.Contains(sidePanelText(panel), "Проверить жалобу") {
					t.Fatal("panel widget rows missing")
				}
			}
			home := elementByID(doc, "ob-tabhome")
			if home == nil {
				t.Fatal("no home")
			}
			inHome := refCardNode(home, "div", "data-ob-widget-card") != nil
			if inHome != tc.widgetHome {
				t.Fatalf("widget on home=%t, want %t", inHome, tc.widgetHome)
			}
		})
	}
	// Обновление виджета панели — тем же маршрутом, хотя на главной его нет.
	if rec := get("/ui/_widget/"+url.PathEscape("МоиЗадачи"), user("СотрудникОТК")); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Проверить жалобу") {
		t.Fatalf("panel widget partial: %d %.300s", rec.Code, rec.Body.String())
	}
}

func sidePanelText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
