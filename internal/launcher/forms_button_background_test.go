package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the registered, authenticated configurator routes, including YAML
// persistence and both preview layouts. Inspect the button separately from its
// wrapper so painting the selection/layout container cannot satisfy the test.
func TestConfiguratorButtonBackground_HTTPRoundTrip(t *testing.T) {
	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "base.db")
	db, err := storage.ConnectSQLite(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	user, err := repo.Create(t.Context(), "admin", "Str0ng-Passw0rd!", "Admin", true)
	if err != nil {
		t.Fatal(err)
	}
	token, err := repo.CreateSession(t.Context(), user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{Path: dir, ConfigSource: "file", DBType: "sqlite", DBPath: dbPath}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	t.Cleanup(func() { server.Close(); <-done })
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(t *testing.T, method, path string, form url.Values) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL()+"/bases/"+base.ID+"/configurator/forms/"+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept-Language", "en")
		req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(raw)
	}
	for _, color := range []struct{ name, value, css string }{
		{"hex", "#e4f6d4", "background:#e4f6d4;"},
		{"rgb", "rgb(12, 34, 56)", "background:rgb(12, 34, 56);"},
		{"named", "red", "background:red;"},
		{"empty", "", ""},
		{"invalid", "rgb(,)", ""},
		{"injection", `red;" onmouseover="alert(1);background:url(https://example.invalid)`, ""},
	} {
		for _, layout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/layout=%t", color.name, layout), func(t *testing.T) {
				suffix := ""
				if layout {
					suffix = "    width: 140\n    height: 40\n    halign: right\n    valign: center\n"
				}
				source := "schema: onebase.form/v1\nform:\n  name: ФормаОбъекта\n  kind: object\n  entity: Звонок\nelements:\n  - kind: Кнопка\n    name: Выполнить\n" + suffix
				code, raw := call(t, http.MethodPost, "edit-op", url.Values{
					"op": {"setProp"}, "node": {"elements.0"}, "key": {"background"}, "value": {color.value}, "yaml": {source},
				})
				if code != http.StatusOK {
					t.Fatalf("edit-op %d: %s", code, raw)
				}
				var edited editOpResponse
				if err := json.Unmarshal([]byte(raw), &edited); err != nil {
					t.Fatal(err)
				}
				if !edited.OK || edited.SelectedID != "elements.0" || edited.Model["elements.0"].Background != color.value {
					t.Fatalf("edit-op lost value or selection: %+v", edited)
				}
				button := backgroundTestButton(t, edited.CanvasHTML)
				if got := backgroundTestAttr(button, "style"); got != color.css {
					t.Fatalf("canvas button style %q, want %q", got, color.css)
				}
				wrapper := button.Parent
				if !strings.Contains(backgroundTestAttr(wrapper, "class"), "fc-selected") {
					t.Fatal("canvas selection lost")
				}
				assertBackgroundTestLayout(t, wrapper, layout)
				code, raw = call(t, http.MethodPost, "save", url.Values{"entity": {"Звонок"}, "name": {"ФормаОбъекта"}, "yaml": {edited.YAML}, "os": {""}})
				if code != http.StatusSeeOther {
					t.Fatalf("save %d: %s", code, raw)
				}
				saved, err := loader.NewManagedFormLoader().LoadFormFile(filepath.Join(dir, "forms", "звонок", "формаобъекта.form.yaml"), "Звонок")
				if err != nil {
					t.Fatal(err)
				}
				if len(saved.Elements) != 1 || saved.Elements[0].Background != color.value {
					t.Fatalf("saved value lost: %+v", saved.Elements)
				}
				code, raw = call(t, http.MethodGet, "edit?entity=Звонок&name=ФормаОбъекта&lang=en", nil)
				if code != http.StatusOK || !strings.Contains(raw, `"Background (CSS color)", 'background', info.background`) {
					t.Fatalf("reloaded editor/localized property missing: %d", code)
				}
				if !strings.Contains(raw, "info.kind === 'ГруппаФормы' || info.kind === 'Кнопка'") {
					t.Fatal("button background property unavailable")
				}
				code, raw = call(t, http.MethodPost, "preview", url.Values{"entity": {"Звонок"}, "yaml": {edited.YAML}})
				if code != http.StatusOK {
					t.Fatalf("preview %d: %s", code, raw)
				}
				button = backgroundTestButton(t, raw)
				if got := backgroundTestAttr(button, "style"); got != color.css {
					t.Fatalf("preview button style %q, want %q", got, color.css)
				}
				if layout {
					if backgroundTestAttr(button.Parent, "class") != "btn-layout" {
						t.Fatal("preview layout wrapper lost")
					}
					assertBackgroundTestLayout(t, button.Parent, true)
				} else if button.Parent.Data == "div" && backgroundTestAttr(button.Parent, "class") == "btn-layout" {
					t.Fatal("unexpected preview layout wrapper")
				}
			})
		}
	}
}

func backgroundTestButton(t *testing.T, source string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var button *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "button" {
			button = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if button == nil {
		t.Fatalf("no button in response: %s", source)
	}
	if backgroundTestAttr(button, "onmouseover") != "" {
		t.Fatal("background injected an HTML event attribute")
	}
	return button
}

func backgroundTestAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func assertBackgroundTestLayout(t *testing.T, wrapper *html.Node, layout bool) {
	t.Helper()
	style := backgroundTestAttr(wrapper, "style")
	if strings.Contains(style, "background:") {
		t.Fatalf("background painted wrapper: %s", style)
	}
	if layout {
		for _, declaration := range []string{"width:140px;", "height:40px;", "margin-left:auto;", "align-self:center;"} {
			if !strings.Contains(style, declaration) {
				t.Errorf("wrapper lost %q: %s", declaration, style)
			}
		}
	} else if style != "" {
		t.Fatalf("unexpected wrapper style: %s", style)
	}
}
