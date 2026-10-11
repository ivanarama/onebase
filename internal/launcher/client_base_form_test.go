package launcher

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

// Exercise the real launcher routes so ClientKind comes from the stored base
// or validation error, rather than a handcrafted template data map.
func TestBaseFormInitialFieldVisibilityHTTP(t *testing.T) {
	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	srv, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		srv.Close()
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	})

	request := func(t *testing.T, method, path string, form url.Values) *html.Node {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL()+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", srv.URL())
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: status %d", method, path, resp.StatusCode)
		}
		doc, err := html.Parse(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}

	for _, dbType := range []string{"postgres", "sqlite"} {
		for _, source := range []string{"database", "file"} {
			t.Run("local/"+dbType+"/"+source, func(t *testing.T) {
				b := &Base{Name: "Local", DBType: dbType, ConfigSource: source, DB: "postgres://localhost/test", DBPath: filepath.Join(t.TempDir(), "base.db"), Path: t.TempDir()}
				if err := store.Add(b); err != nil {
					t.Fatal(err)
				}
				doc := request(t, http.MethodGet, "/bases/"+b.ID+"/edit", nil)
				for _, name := range []string{"config_source", "db_type", "port", "host"} {
					assertBaseFormFieldVisible(t, doc, name, true)
				}
				assertBaseFormFieldVisible(t, doc, "path", source == "file")
				assertBaseFormFieldVisible(t, doc, "db", dbType == "postgres")
				assertBaseFormFieldVisible(t, doc, "db_path", dbType == "sqlite")
				assertBaseFormFieldVisible(t, doc, "server_url", false)
			})
		}
	}

	b, err := NewClientBase("Client", "https://server.example:8443")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(b); err != nil {
		t.Fatal(err)
	}
	assertClient := func(t *testing.T, doc *html.Node, isNew bool) {
		t.Helper()
		for _, name := range []string{"config_source", "path", "db_type", "db", "db_path", "port", "host"} {
			assertBaseFormFieldVisible(t, doc, name, false)
		}
		if isNew {
			assertBaseFormFieldVisible(t, doc, "scaffold", false)
		}
		assertBaseFormFieldVisible(t, doc, "server_url", true)
		assertBaseFormFieldVisible(t, doc, "name", true)
		assertBaseFormFieldVisible(t, doc, "base_kind", true)
	}
	t.Run("client/edit", func(t *testing.T) {
		assertClient(t, request(t, http.MethodGet, "/bases/"+b.ID+"/edit", nil), false)
	})
	for _, path := range []string{"/bases", "/bases/" + b.ID} {
		for _, address := range []string{"", "invalid-address"} {
			for _, dbType := range []string{"postgres", "sqlite"} {
				t.Run("client/error/"+path+"/"+address+"/"+dbType, func(t *testing.T) {
					doc := request(t, http.MethodPost, path, url.Values{
						"name": {"Client"}, "base_kind": {baseKindClient}, "server_url": {address},
						"config_source": {"file"}, "db_type": {dbType},
					})
					assertClient(t, doc, path == "/bases")
				})
			}
		}
	}
	t.Run("local/new", func(t *testing.T) {
		doc := request(t, http.MethodGet, "/bases/new", nil)
		for _, name := range []string{"config_source", "path", "db_type", "db_path", "port", "host", "scaffold"} {
			assertBaseFormFieldVisible(t, doc, name, true)
		}
		assertBaseFormFieldVisible(t, doc, "db", false)
		assertBaseFormFieldVisible(t, doc, "server_url", false)
	})
}

// Inspect the server-rendered visibility before any onchange handler runs.
func assertBaseFormFieldVisible(t *testing.T, doc *html.Node, name string, want bool) {
	t.Helper()
	var field *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "select") {
			if v, ok := attr(n, "name"); ok && v == name {
				field = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if field == nil {
		t.Fatalf("field %q missing", name)
	}
	visible := true
	for n := field; n != nil; n = n.Parent {
		style, _ := attr(n, "style")
		for _, declaration := range strings.Split(style, ";") {
			prop, value, ok := strings.Cut(declaration, ":")
			if ok && strings.TrimSpace(prop) == "display" && strings.TrimSpace(value) == "none" {
				visible = false
			}
		}
	}
	if visible != want {
		t.Errorf("field %q initially visible=%v, want %v", name, visible, want)
	}
}
